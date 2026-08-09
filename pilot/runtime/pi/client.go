package pi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultEventQueueSize   = 128
	defaultEventOfferTimout = 2 * time.Second
)

// ClientConfig 控制 RPC Client 的有界资源。
type ClientConfig struct {
	MaxFrameBytes     int
	MaxOutputBytes    int64
	EventQueueSize    int
	EventOfferTimeout time.Duration
}

type responseResult struct {
	response wireResponse
	err      error
}

type pendingRequest struct {
	command string
	result  chan responseResult
}

// RPCClient 实现 Pi stdin/stdout JSONL 请求相关性与单读泵。
type RPCClient struct {
	stdin   io.WriteCloser
	decoder *Decoder

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]pendingRequest
	done    chan struct{}
	err     error

	events       chan WireEvent
	offerTimeout time.Duration
	requestID    atomic.Uint64
	finishOnce   sync.Once
}

// NewRPCClient 创建并启动 stdout reader。调用方应持续消费 Events。
func NewRPCClient(stdin io.WriteCloser, stdout io.Reader, cfg ClientConfig) *RPCClient {
	queueSize := cfg.EventQueueSize
	if queueSize <= 0 {
		queueSize = defaultEventQueueSize
	}
	offerTimeout := cfg.EventOfferTimeout
	if offerTimeout <= 0 {
		offerTimeout = defaultEventOfferTimout
	}

	c := &RPCClient{
		stdin:        stdin,
		decoder:      NewDecoder(stdout, cfg.MaxFrameBytes, cfg.MaxOutputBytes),
		pending:      make(map[string]pendingRequest),
		done:         make(chan struct{}),
		events:       make(chan WireEvent, queueSize),
		offerTimeout: offerTimeout,
	}
	go c.readLoop()
	return c
}

// Events 返回 Pi 原始事件。Channel 在 stdout reader 结束后关闭。
func (c *RPCClient) Events() <-chan WireEvent {
	return c.events
}

// Wait 等待 stdout reader 终止。EOF 在进程正常关闭时返回 nil。
func (c *RPCClient) Wait() error {
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close 关闭 stdin；进程退出和 stdout reader 回收仍由 Adapter 负责等待。
func (c *RPCClient) Close() error {
	return c.stdin.Close()
}

// Prompt 发送用户消息。成功只表示 Pi 接受了命令，不表示 Run 已成功。
func (c *RPCClient) Prompt(ctx context.Context, message string) error {
	return c.do(ctx, "prompt", map[string]any{"message": message}, nil)
}

// Abort 请求 Pi 终止当前 Agent operation。
func (c *RPCClient) Abort(ctx context.Context) error {
	return c.do(ctx, "abort", nil, nil)
}

// GetState 获取启动核验所需状态。
func (c *RPCClient) GetState(ctx context.Context) (State, error) {
	var state State
	err := c.do(ctx, "get_state", nil, &state)
	return state, err
}

// GetCommands obtains the extension command set used for the trusted Bridge
// pre-Prompt readiness proof. Pi 0.84.1 does not expose registered Tools in
// get_state, so the Bridge registers one inert command after all Tool setup.
func (c *RPCClient) GetCommands(ctx context.Context) (commandsData, error) {
	var commands commandsData
	err := c.do(ctx, "get_commands", nil, &commands)
	return commands, err
}

// GetLastAssistantText 获取 settled 后的权威最终文本。
func (c *RPCClient) GetLastAssistantText(ctx context.Context) (*string, error) {
	var data lastAssistantTextData
	if err := c.do(ctx, "get_last_assistant_text", nil, &data); err != nil {
		return nil, err
	}
	return data.Text, nil
}

// GetSessionStats 获取 settled 后的 Session 和用量数据。
func (c *RPCClient) GetSessionStats(ctx context.Context) (SessionStats, error) {
	var stats SessionStats
	err := c.do(ctx, "get_session_stats", nil, &stats)
	return stats, err
}

// GetLeafEntryID 获取当前 Session branch 的 leaf cursor。
func (c *RPCClient) GetLeafEntryID(ctx context.Context) (string, error) {
	var data entriesData
	if err := c.do(ctx, "get_entries", nil, &data); err != nil {
		return "", err
	}
	if data.LeafID == nil {
		return "", nil
	}
	return *data.LeafID, nil
}

func (c *RPCClient) do(ctx context.Context, command string, fields map[string]any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	id := fmt.Sprintf("pilot-%d", c.requestID.Add(1))
	pending := pendingRequest{command: command, result: make(chan responseResult, 1)}
	request := make(map[string]any, len(fields)+2)
	request["id"] = id
	request["type"] = command
	maps.Copy(request, fields)
	frame, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode pi rpc command %q: %w", command, err)
	}
	frame = append(frame, '\n')

	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return err
	}
	select {
	case <-c.done:
		err := c.err
		c.mu.Unlock()
		if err == nil {
			return io.EOF
		}
		return err
	default:
	}
	c.pending[id] = pending
	c.mu.Unlock()

	c.writeMu.Lock()
	if err := ctx.Err(); err != nil {
		c.writeMu.Unlock()
		c.removePending(id)
		return err
	}
	written, err := writeAllCount(c.stdin, frame)
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(id)
		if written > 0 {
			return &CommandOutcomeUnknownError{Command: command, Cause: err}
		}
		return fmt.Errorf("write pi rpc command %q: %w", command, err)
	}

	// 优先接收已经缓冲的响应，避免 ACK 与 deadline 同时到达时误报未知结果。
	select {
	case result := <-pending.result:
		return decodeResponse(command, result, out)
	default:
	}
	select {
	case result := <-pending.result:
		return decodeResponse(command, result, out)
	case <-ctx.Done():
		// 保留 pending，直到晚到响应或 Client 关闭，避免把合法晚响应误判为未知 ID。
		return &CommandOutcomeUnknownError{Command: command, Cause: ctx.Err()}
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			return io.EOF
		}
		return err
	}
}

func decodeResponse(command string, result responseResult, out any) error {
	if result.err != nil {
		return result.err
	}
	if result.response.Success == nil {
		return &ProtocolError{Kind: ErrMalformedJSON, Preview: "response missing success"}
	}
	if !*result.response.Success {
		return &CommandError{Command: command, Message: result.response.Error}
	}
	if out == nil {
		return nil
	}
	decoded, err := decodeData[json.RawMessage](result.response.Data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(decoded, out); err != nil {
		return fmt.Errorf("decode pi rpc %q response: %w", command, err)
	}
	return nil
}

func (c *RPCClient) readLoop() {
	var terminalErr error
	defer func() {
		if errors.Is(terminalErr, io.EOF) {
			terminalErr = nil
		}
		c.finish(terminalErr)
		close(c.events)
	}()

	for {
		raw, err := c.decoder.Next()
		if err != nil {
			terminalErr = err
			return
		}

		var header wireHeader
		if err := json.Unmarshal(raw, &header); err != nil || header.Type == "" {
			terminalErr = &ProtocolError{Kind: ErrMalformedJSON, Preview: "missing event type", Cause: err}
			return
		}

		if header.Type == "response" {
			if err := c.dispatchResponse(raw); err != nil {
				terminalErr = err
				return
			}
			continue
		}

		timer := time.NewTimer(c.offerTimeout)
		select {
		case c.events <- WireEvent{Type: header.Type, Raw: raw}:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			terminalErr = ErrEventBackpressure
			return
		case <-c.done:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (c *RPCClient) dispatchResponse(raw json.RawMessage) error {
	var response wireResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return &ProtocolError{Kind: ErrMalformedJSON, Preview: "invalid response", Cause: err}
	}
	if response.ID == "" {
		return &ProtocolError{Kind: ErrMalformedJSON, Preview: "response missing id"}
	}

	c.mu.Lock()
	pending, ok := c.pending[response.ID]
	if ok {
		delete(c.pending, response.ID)
	}
	c.mu.Unlock()
	if !ok {
		return &ProtocolError{Kind: ErrMalformedJSON, Preview: "unknown response id"}
	}
	if response.Command != pending.command {
		return &ProtocolError{Kind: ErrMalformedJSON, Preview: "response command mismatch"}
	}
	pending.result <- responseResult{response: response}
	return nil
}

func (c *RPCClient) finish(err error) {
	c.finishOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		pending := c.pending
		c.pending = make(map[string]pendingRequest)
		close(c.done)
		c.mu.Unlock()

		if err == nil {
			err = io.EOF
		}
		for _, request := range pending {
			request.result <- responseResult{err: err}
		}
	})
}

func (c *RPCClient) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func writeAllCount(w io.Writer, data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n, err := w.Write(data)
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
		data = data[n:]
	}
	return written, nil
}
