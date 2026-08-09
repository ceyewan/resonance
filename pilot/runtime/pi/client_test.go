package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRPCClient_CommandAndEventsInterleave(t *testing.T) {
	h := newRPCHarness(t, ClientConfig{})

	stateResult := make(chan State, 1)
	stateErr := make(chan error, 1)
	go func() {
		state, err := h.client.GetState(context.Background())
		stateResult <- state
		stateErr <- err
	}()
	promptErr := make(chan error, 1)
	go func() { promptErr <- h.client.Prompt(context.Background(), "hello") }()

	first := h.readCommand(t)
	second := h.readCommand(t)
	commands := map[string]map[string]any{
		first["type"].(string):  first,
		second["type"].(string): second,
	}
	h.writeJSON(t, map[string]any{"type": "agent_start"})
	h.writeResponse(t, commands["prompt"], true, "")
	h.writeJSON(t, map[string]any{
		"type":    "response",
		"id":      commands["get_state"]["id"],
		"command": "get_state",
		"success": true,
		"data": map[string]any{
			"model":                 map[string]any{"id": "model", "provider": "provider"},
			"isStreaming":           false,
			"isCompacting":          false,
			"sessionFile":           "/tmp/session.jsonl",
			"sessionId":             "session-1",
			"autoCompactionEnabled": true,
			"messageCount":          0,
			"pendingMessageCount":   0,
		},
	})

	require.NoError(t, <-promptErr)
	require.NoError(t, <-stateErr)
	require.Equal(t, "model", (<-stateResult).Model.ID)
	event := <-h.client.Events()
	require.Equal(t, "agent_start", event.Type)
}

func TestRPCClient_PromptAckDoesNotSettleAndUnknownEventSurvives(t *testing.T) {
	h := newRPCHarness(t, ClientConfig{})
	errCh := make(chan error, 1)
	go func() { errCh <- h.client.Prompt(context.Background(), "hello") }()

	command := h.readCommand(t)
	h.writeResponse(t, command, true, "")
	require.NoError(t, <-errCh)

	h.writeJSON(t, map[string]any{"type": "future_event", "newField": true})
	select {
	case event := <-h.client.Events():
		require.Equal(t, "future_event", event.Type)
	case <-time.After(time.Second):
		t.Fatal("prompt ACK 后 client 不应自行 settled 或停止读取事件")
	}
}

func TestRPCClient_ResponseFailureAndMismatchedIDFailClosed(t *testing.T) {
	t.Run("command failure", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		errCh := make(chan error, 1)
		go func() { errCh <- h.client.Prompt(context.Background(), "hello") }()
		command := h.readCommand(t)
		h.writeResponse(t, command, false, "provider unavailable")

		var commandErr *CommandError
		require.ErrorAs(t, <-errCh, &commandErr)
		require.Equal(t, "prompt", commandErr.Command)
	})

	t.Run("unknown response id", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		h.writeJSON(t, map[string]any{
			"type": "response", "id": "unknown", "command": "prompt", "success": true,
		})
		err := h.client.Wait()
		require.ErrorIs(t, err, ErrMalformedJSON)
	})

	t.Run("missing response id", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		h.writeJSON(t, map[string]any{
			"type": "response", "command": "prompt", "success": true,
		})
		require.ErrorIs(t, h.client.Wait(), ErrMalformedJSON)
	})
}

func TestRPCClient_EventBackpressureFailsBoundedly(t *testing.T) {
	h := newRPCHarness(t, ClientConfig{EventQueueSize: 1, EventOfferTimeout: 20 * time.Millisecond})
	h.writeJSON(t, map[string]any{"type": "agent_start"})
	h.writeJSON(t, map[string]any{"type": "agent_start"})

	done := make(chan error, 1)
	go func() { done <- h.client.Wait() }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrEventBackpressure)
	case <-time.After(time.Second):
		t.Fatal("事件消费者阻塞时 RPC reader 必须有界失败")
	}
}

type rpcHarness struct {
	client       *RPCClient
	commandRead  *bufio.Reader
	stdoutWriter *io.PipeWriter
}

func newRPCHarness(t *testing.T, cfg ClientConfig) *rpcHarness {
	t.Helper()
	commandRead, commandWrite := io.Pipe()
	stdoutRead, stdoutWrite := io.Pipe()
	client := NewRPCClient(commandWrite, stdoutRead, cfg)
	h := &rpcHarness{client: client, commandRead: bufio.NewReader(commandRead), stdoutWriter: stdoutWrite}
	t.Cleanup(func() {
		_ = commandWrite.Close()
		_ = commandRead.Close()
		_ = stdoutWrite.Close()
		_ = stdoutRead.Close()
	})
	return h
}

func (h *rpcHarness) readCommand(t *testing.T) map[string]any {
	t.Helper()
	line, err := h.commandRead.ReadBytes('\n')
	require.NoError(t, err)
	var command map[string]any
	require.NoError(t, json.Unmarshal(line, &command))
	return command
}

func (h *rpcHarness) writeJSON(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	data = append(data, '\n')
	_, err = h.stdoutWriter.Write(data)
	require.NoError(t, err)
}

func (h *rpcHarness) writeResponse(t *testing.T, command map[string]any, success bool, message string) {
	t.Helper()
	response := map[string]any{
		"type": "response", "id": command["id"], "command": command["type"], "success": success,
	}
	if message != "" {
		response["error"] = message
	}
	h.writeJSON(t, response)
}

func TestRPCClient_ContextCancellationKeepsLateResponseValid(t *testing.T) {
	h := newRPCHarness(t, ClientConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- h.client.Prompt(ctx, "hello") }()
	command := h.readCommand(t)
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)

	// 晚响应仍应被合法消费，不能被当成 unknown response id 污染整个 Client。
	h.writeResponse(t, command, true, "")
	h.writeJSON(t, map[string]any{"type": "future_event"})
	select {
	case event := <-h.client.Events():
		require.Equal(t, "future_event", event.Type)
	case <-time.After(time.Second):
		t.Fatal("late response should not terminate client")
	}
}

func TestRPCClient_CanceledBeforeCommandDoesNotWrite(t *testing.T) {
	stdin := &recordingWriteCloser{}
	stdoutReader, stdoutWriter := io.Pipe()
	client := NewRPCClient(stdin, stdoutReader, ClientConfig{})
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.Prompt(ctx, "must-not-be-written")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, stdin.Bytes())
}

func TestRPCClient_PartialWriteMarksOutcomeUnknown(t *testing.T) {
	stdin := &partialFailureWriteCloser{}
	stdoutReader, stdoutWriter := io.Pipe()
	client := NewRPCClient(stdin, stdoutReader, ClientConfig{})
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})

	err := client.Prompt(context.Background(), "hello")
	var uncertain *CommandOutcomeUnknownError
	require.ErrorAs(t, err, &uncertain)
	require.Equal(t, "prompt", uncertain.Command)
}

func TestRPCClient_ResponsePresenceAndCommandAreValidated(t *testing.T) {
	t.Run("missing success", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		errCh := make(chan error, 1)
		go func() { errCh <- h.client.Prompt(context.Background(), "hello") }()
		command := h.readCommand(t)
		h.writeJSON(t, map[string]any{
			"type": "response", "id": command["id"], "command": "prompt",
		})
		require.ErrorIs(t, <-errCh, ErrMalformedJSON)
	})

	t.Run("command mismatch", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		errCh := make(chan error, 1)
		go func() { errCh <- h.client.Prompt(context.Background(), "hello") }()
		command := h.readCommand(t)
		h.writeJSON(t, map[string]any{
			"type": "response", "id": command["id"], "command": "abort", "success": true,
		})
		require.ErrorIs(t, h.client.Wait(), ErrMalformedJSON)
		require.ErrorIs(t, <-errCh, ErrMalformedJSON)
	})

	t.Run("get_state missing required fields", func(t *testing.T) {
		h := newRPCHarness(t, ClientConfig{})
		errCh := make(chan error, 1)
		go func() {
			_, err := h.client.GetState(context.Background())
			errCh <- err
		}()
		command := h.readCommand(t)
		h.writeJSON(t, map[string]any{
			"type": "response", "id": command["id"], "command": "get_state", "success": true,
			"data": map[string]any{"model": map[string]any{"id": "model", "provider": "provider"}},
		})
		require.ErrorContains(t, <-errCh, "missing required")
	})
}

type recordingWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *recordingWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(data)
}

func (w *recordingWriteCloser) Close() error { return nil }

func (w *recordingWriteCloser) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

type partialFailureWriteCloser struct{}

func (*partialFailureWriteCloser) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return 1, errors.New("partial write failure")
}

func (*partialFailureWriteCloser) Close() error { return nil }
