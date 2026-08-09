package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type ClientConfig struct {
	SocketPath      string
	MaxRequestBytes int
	MaxFrameBytes   int
	EventQueueSize  int
	DialTimeout     time.Duration
}

type Client struct {
	config    ClientConfig
	http      *http.Client
	transport *http.Transport
}

func NewClient(config ClientConfig) (*Client, error) {
	if err := setClientDefaultsAndValidate(&config); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: config.DialTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", config.SocketPath)
		},
		DisableCompression:  true,
		DisableKeepAlives:   false,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		MaxConnsPerHost:     64,
		IdleConnTimeout:     30 * time.Second,
	}
	return &Client{
		config: config, transport: transport,
		http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("remote runtime redirects are disabled")
		}},
	}, nil
}

func setClientDefaultsAndValidate(config *ClientConfig) error {
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = 1 << 20
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = 1 << 20
	}
	if config.EventQueueSize == 0 {
		config.EventQueueSize = 128
	}
	if config.DialTimeout == 0 {
		config.DialTimeout = 2 * time.Second
	}
	if err := validateSocketPath(config.SocketPath); err != nil {
		return err
	}
	if config.MaxRequestBytes < 1 || config.MaxRequestBytes > 8<<20 ||
		config.MaxFrameBytes < 1 || config.MaxFrameBytes > 8<<20 ||
		config.EventQueueSize < 1 || config.EventQueueSize > 4096 || config.DialTimeout <= 0 {
		return fmt.Errorf("remote runtime client limits are invalid")
	}
	return nil
}

func (c *Client) Run(ctx context.Context, request pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, pilotruntime.NewRunError(err, &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
	}
	wire := runRequestWire{
		ProtocolVersion: protocolVersion,
		RunID:           request.RunID, ConversationID: request.ConversationID, Prompt: request.Prompt,
		Session: request.Session, Profile: request.Profile, Actor: request.Actor,
		Capability: request.Capability.Reveal(), Limits: request.Limits,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, pilotruntime.NewRunError(fmt.Errorf("encode remote runtime request"), &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
	}
	if len(payload) > c.config.MaxRequestBytes {
		return nil, pilotruntime.NewRunError(fmt.Errorf("remote runtime request exceeds limit"), &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://runtime/v1/run", bytes.NewReader(payload))
	if err != nil {
		return nil, pilotruntime.NewRunError(fmt.Errorf("create remote runtime request"), &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		// Once RoundTrip starts, the server may have received the prompt even if
		// the response header was lost. Budgeting must retain the reservation.
		return nil, pilotruntime.NewRunError(fmt.Errorf("remote runtime unavailable: %w", err), &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
	}
	if response.StatusCode != http.StatusOK {
		defer func() { _ = response.Body.Close() }()
		remoteErr := readHTTPError(response.Body, c.config.MaxFrameBytes)
		return nil, pilotruntime.NewRunError(remoteErr, remoteErr.usage())
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/x-ndjson") {
		_ = response.Body.Close()
		return nil, pilotruntime.NewRunError(fmt.Errorf("remote runtime returned an invalid stream"), &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
	}
	decoder := newFrameDecoder(response.Body, c.config.MaxFrameBytes)
	first, err := decoder.Next()
	if err != nil || first.ProtocolVersion != protocolVersion || first.Type != frameAccepted ||
		first.Event != nil || first.Result != nil || first.Error != nil {
		_ = response.Body.Close()
		return nil, pilotruntime.NewRunError(fmt.Errorf("remote runtime did not accept the run"), &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
	}
	stream := newRemoteStream(c.config.EventQueueSize, response.Body)
	go stream.receive(ctx, decoder)
	return stream, nil
}

func (c *Client) Abort(ctx context.Context, runID string) error {
	if !validRunID(runID) {
		return fmt.Errorf("invalid remote runtime run id")
	}
	return c.callJSON(ctx, "/v1/abort", runControlWire{RunID: runID})
}

func (c *Client) Probe(ctx context.Context) error {
	return c.callEmpty(ctx, "/v1/probe")
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.callEmpty(ctx, "/v1/shutdown")
}

func (c *Client) Close() {
	if c != nil && c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

func (c *Client) callEmpty(ctx context.Context, path string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://runtime"+path, http.NoBody)
	if err != nil {
		return err
	}
	return c.doStatus(request)
}

func (c *Client) callJSON(ctx context.Context, path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(payload) > c.config.MaxRequestBytes {
		return fmt.Errorf("remote runtime control request exceeds limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://runtime"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return c.doStatus(request)
}

func (c *Client) doStatus(request *http.Request) error {
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return readHTTPError(response.Body, c.config.MaxFrameBytes)
	}
	limited := io.LimitReader(response.Body, int64(c.config.MaxFrameBytes)+1)
	var status statusWire
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil || !status.OK {
		return fmt.Errorf("remote runtime returned an invalid status")
	}
	return nil
}

type wireHTTPError struct {
	errWire runtimeErrorWire
}

func (e *wireHTTPError) Error() string {
	if e == nil || e.errWire.Message == "" {
		return "remote runtime request failed"
	}
	return e.errWire.Message
}

func (e *wireHTTPError) usage() *pilotruntime.Usage {
	if e != nil && e.errWire.Usage != nil {
		copy := *e.errWire.Usage
		return &copy
	}
	return &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
}

func readHTTPError(reader io.Reader, limit int) *wireHTTPError {
	limited := io.LimitReader(reader, int64(limit)+1)
	payload, err := io.ReadAll(limited)
	if err != nil || len(payload) == 0 || len(payload) > limit {
		return &wireHTTPError{errWire: runtimeErrorWire{Kind: "runtime", Message: "remote runtime request failed", Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}}
	}
	var wire runtimeErrorWire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.Message == "" {
		return &wireHTTPError{errWire: runtimeErrorWire{Kind: "runtime", Message: "remote runtime request failed", Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}}
	}
	return &wireHTTPError{errWire: wire}
}

type remoteStream struct {
	events chan pilotruntime.RuntimeEvent
	done   chan struct{}
	body   io.Closer
	once   sync.Once
	mu     sync.Mutex
	result pilotruntime.RunResult
	err    error
}

func newRemoteStream(queueSize int, body io.Closer) *remoteStream {
	return &remoteStream{events: make(chan pilotruntime.RuntimeEvent, queueSize), done: make(chan struct{}), body: body}
}

func (s *remoteStream) Events() <-chan pilotruntime.RuntimeEvent { return s.events }

func (s *remoteStream) Wait() (pilotruntime.RunResult, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result, s.err
}

func (s *remoteStream) receive(ctx context.Context, decoder *frameDecoder) {
	for {
		frame, err := decoder.Next()
		if err != nil {
			if ctx.Err() != nil {
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, ctx.Err())
			} else {
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime stream ended before a terminal frame"))
			}
			return
		}
		if frame.ProtocolVersion != protocolVersion {
			s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime protocol version mismatch"))
			return
		}
		switch frame.Type {
		case frameEvent:
			if frame.Event == nil || frame.Result != nil || frame.Error != nil {
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime returned an invalid event frame"))
				return
			}
			select {
			case s.events <- *frame.Event:
			case <-ctx.Done():
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, ctx.Err())
				return
			}
		case frameResult:
			if frame.Result == nil || frame.Event != nil || frame.Error != nil || frame.Result.Usage == nil {
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime returned an invalid result frame"))
				return
			}
			s.finish(*frame.Result, nil)
			return
		case frameError:
			if frame.Error == nil || frame.Event != nil || frame.Result != nil {
				s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime returned an invalid error frame"))
				return
			}
			usage := frame.Error.Usage
			if usage == nil {
				usage = &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}
			}
			s.finish(pilotruntime.RunResult{Usage: usage}, &remoteError{kind: frame.Error.Kind, message: frame.Error.Message})
			return
		default:
			s.finish(pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}, fmt.Errorf("remote runtime returned an unknown frame"))
			return
		}
	}
}

func (s *remoteStream) finish(result pilotruntime.RunResult, err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.result = result
		s.err = err
		s.mu.Unlock()
		_ = s.body.Close()
		close(s.events)
		close(s.done)
	})
}

type frameDecoder struct {
	reader *bufio.Reader
	limit  int
}

func newFrameDecoder(reader io.Reader, limit int) *frameDecoder {
	return &frameDecoder{reader: bufio.NewReaderSize(reader, min(limit+1, 64<<10)), limit: limit}
}

func (d *frameDecoder) Next() (runFrame, error) {
	buffer := make([]byte, 0, min(d.limit, 4<<10))
	for {
		part, err := d.reader.ReadSlice('\n')
		if len(buffer)+len(part) > d.limit {
			return runFrame{}, fmt.Errorf("remote runtime frame exceeds limit")
		}
		buffer = append(buffer, part...)
		switch {
		case err == nil:
			buffer = buffer[:len(buffer)-1]
			if len(buffer) > 0 && buffer[len(buffer)-1] == '\r' {
				buffer = buffer[:len(buffer)-1]
			}
			return decodeFrame(buffer)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(buffer) > 0:
			return decodeFrame(buffer)
		default:
			return runFrame{}, err
		}
	}
}

func decodeFrame(payload []byte) (runFrame, error) {
	if len(payload) == 0 {
		return runFrame{}, fmt.Errorf("remote runtime returned an empty frame")
	}
	var frame runFrame
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&frame); err != nil {
		return runFrame{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return runFrame{}, fmt.Errorf("remote runtime frame contains trailing JSON")
	}
	return frame, nil
}
