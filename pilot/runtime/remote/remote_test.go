package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestRemoteRuntime_RoundTripsControlledEventsResultAndCapability(t *testing.T) {
	release := make(chan struct{})
	stream := newFakeStream([]pilotruntime.RuntimeEvent{{Kind: pilotruntime.EventStarted, Sequence: 1}}, release)
	stream.result = pilotruntime.RunResult{
		FinalText: "answer", SessionID: "session-1", SessionFile: "/sessions/one.jsonl", LeafEntryID: "leaf-1",
		Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateExact, TotalTokens: 30, CostMicros: 25},
	}
	runtime := &fakeRuntime{runFn: func(_ context.Context, request pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
		require.Equal(t, "run-1", request.RunID)
		require.Equal(t, "capability-secret", request.Capability.Reveal())
		return stream, nil
	}}
	client := startRemoteFixture(t, runtime)

	remoteStream, err := client.Run(context.Background(), validRunRequest())
	require.NoError(t, err)
	event := <-remoteStream.Events()
	require.Equal(t, pilotruntime.EventStarted, event.Kind)
	close(release)
	result, err := remoteStream.Wait()
	require.NoError(t, err)
	require.Equal(t, "answer", result.FinalText)
	require.Equal(t, int64(25), result.Usage.CostMicros)
	_, open := <-remoteStream.Events()
	require.False(t, open)
}

func TestRemoteRuntime_PreservesStartupAndTerminalUsageState(t *testing.T) {
	t.Run("startup not started", func(t *testing.T) {
		runtime := &fakeRuntime{runFn: func(context.Context, pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
			return nil, pilotruntime.NewRunError(errors.New("prompt was not sent"), &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
		}}
		client := startRemoteFixture(t, runtime)
		_, err := client.Run(context.Background(), validRunRequest())
		var runError *pilotruntime.RunError
		require.ErrorAs(t, err, &runError)
		require.Equal(t, pilotruntime.UsageStateNotStarted, runError.Usage.State)
	})

	t.Run("terminal unknown", func(t *testing.T) {
		release := make(chan struct{})
		stream := newFakeStream(nil, release)
		stream.result = pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown}}
		stream.err = errors.New("process exited before final stats")
		runtime := &fakeRuntime{runFn: func(context.Context, pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
			return stream, nil
		}}
		client := startRemoteFixture(t, runtime)
		remoteStream, err := client.Run(context.Background(), validRunRequest())
		require.NoError(t, err)
		close(release)
		result, err := remoteStream.Wait()
		require.Error(t, err)
		require.Equal(t, pilotruntime.UsageStateUnknown, result.Usage.State)
	})
}

func TestRemoteRuntime_ProbeAbortAndShutdownArePrivateControlCalls(t *testing.T) {
	runtime := &fakeRuntime{}
	client, server := startRemoteFixtureWithServer(t, runtime)
	require.NoError(t, client.Probe(context.Background()))
	require.NoError(t, client.Abort(context.Background(), "run-1"))
	require.NoError(t, client.Shutdown(context.Background()))
	select {
	case <-server.Shutdowns():
	case <-time.After(time.Second):
		t.Fatal("successful private shutdown did not notify the runtime host")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	require.Equal(t, 1, runtime.probes)
	require.Equal(t, []string{"run-1"}, runtime.aborts)
	// Client Shutdown plus fixture Server.Close both call the idempotent runtime boundary.
	require.GreaterOrEqual(t, runtime.shutdowns, 1)
}

func TestRemoteRuntime_SocketIsPrivateAndNeverReplacesRegularFile(t *testing.T) {
	directory := privateTempDir(t)
	socket := filepath.Join(directory, "runtime.sock")
	server, err := NewServer(ServerConfig{SocketPath: socket, SessionRoot: "/sessions"}, &fakeRuntime{})
	require.NoError(t, err)
	require.NoError(t, server.Start())
	info, err := os.Lstat(socket)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSocket)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, server.Close(context.Background()))

	require.NoError(t, os.WriteFile(socket, []byte("do not replace"), 0o600))
	server, err = NewServer(ServerConfig{SocketPath: socket, SessionRoot: "/sessions"}, &fakeRuntime{})
	require.NoError(t, err)
	require.Error(t, server.Start())
	payload, err := os.ReadFile(socket)
	require.NoError(t, err)
	require.Equal(t, "do not replace", string(payload))
}

func TestRemoteRuntime_RejectsNonPrivateOrSymlinkSocketDirectory(t *testing.T) {
	publicDirectory := filepath.Join(t.TempDir(), "public")
	require.NoError(t, os.Mkdir(publicDirectory, 0o755))
	server, err := NewServer(ServerConfig{SocketPath: filepath.Join(publicDirectory, "runtime.sock"), SessionRoot: "/sessions"}, &fakeRuntime{})
	require.NoError(t, err)
	require.Error(t, server.Start())

	realDirectory := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.Mkdir(realDirectory, 0o700))
	linkDirectory := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(realDirectory, linkDirectory))
	server, err = NewServer(ServerConfig{SocketPath: filepath.Join(linkDirectory, "runtime.sock"), SessionRoot: "/sessions"}, &fakeRuntime{})
	require.NoError(t, err)
	require.Error(t, server.Start())
}

func TestRemoteRuntime_RejectsSessionOutsideConfiguredRoot(t *testing.T) {
	runtime := &fakeRuntime{runFn: func(context.Context, pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
		t.Fatal("out-of-root session must not reach Pi")
		return nil, nil
	}}
	client := startRemoteFixture(t, runtime)
	request := validRunRequest()
	request.Session.Directory = "/etc"
	_, err := client.Run(context.Background(), request)
	var runError *pilotruntime.RunError
	require.ErrorAs(t, err, &runError)
	require.Equal(t, pilotruntime.UsageStateNotStarted, runError.Usage.State)
}

func TestRemoteRuntime_RunTransportAmbiguityIsUnknownButPreCanceledIsNotStarted(t *testing.T) {
	directory := t.TempDir()
	client, err := NewClient(ClientConfig{SocketPath: filepath.Join(directory, "missing.sock"), DialTimeout: 50 * time.Millisecond})
	require.NoError(t, err)
	_, err = client.Run(context.Background(), validRunRequest())
	var runError *pilotruntime.RunError
	require.ErrorAs(t, err, &runError)
	require.Equal(t, pilotruntime.UsageStateUnknown, runError.Usage.State)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Run(ctx, validRunRequest())
	require.ErrorAs(t, err, &runError)
	require.Equal(t, pilotruntime.UsageStateNotStarted, runError.Usage.State)
}

func TestFrameDecoder_FragmentedAndBounded(t *testing.T) {
	payload := []byte(`{"protocol_version":1,"type":"accepted"}` + "\n")
	decoder := newFrameDecoder(&oneByteReader{data: payload}, len(payload))
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.Equal(t, frameAccepted, frame.Type)

	decoder = newFrameDecoder(bytes.NewReader(append(bytes.Repeat([]byte("x"), 32), '\n')), 16)
	_, err = decoder.Next()
	require.Error(t, err)
}

func startRemoteFixture(t *testing.T, runtime *fakeRuntime) *Client {
	client, _ := startRemoteFixtureWithServer(t, runtime)
	return client
}

func startRemoteFixtureWithServer(t *testing.T, runtime *fakeRuntime) (*Client, *Server) {
	t.Helper()
	socket := filepath.Join(privateTempDir(t), "runtime.sock")
	server, err := NewServer(ServerConfig{SocketPath: socket, SessionRoot: "/sessions", MaxFrameBytes: 64 << 10}, runtime)
	require.NoError(t, err)
	require.NoError(t, server.Start())
	client, err := NewClient(ClientConfig{SocketPath: socket, MaxFrameBytes: 64 << 10})
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Close(shutdownContext))
	})
	return client, server
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	tempRoot, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	directory, err := os.MkdirTemp(tempRoot, "resonance-remote-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)) })
	require.NoError(t, os.Chmod(directory, 0o700))
	return directory
}

func validRunRequest() pilotruntime.RunRequest {
	return pilotruntime.RunRequest{
		RunID: "run-1", ConversationID: "conversation-1", Prompt: "hello",
		Session:    pilotruntime.SessionSnapshot{SessionID: "session-1", Directory: "/sessions/staging/run-1"},
		Profile:    pilotruntime.ProfileSnapshot{ID: "user-assistant", Version: 1, Provider: "anthropic", Model: "claude", SystemPrompt: "assist"},
		Actor:      pilotruntime.ActorPrincipal{TenantID: "tenant-a", ActorID: "alice", Username: "alice"},
		Capability: pilotruntime.NewSecret("capability-secret"),
		Limits:     pilotruntime.ExecutionLimits{MaxTotalTokens: 20000, MaxCostMicros: 100000, MaxProviderCalls: 8},
	}
}

type fakeRuntime struct {
	mu        sync.Mutex
	runFn     func(context.Context, pilotruntime.RunRequest) (pilotruntime.EventStream, error)
	probes    int
	aborts    []string
	shutdowns int
}

func (r *fakeRuntime) Run(ctx context.Context, request pilotruntime.RunRequest) (pilotruntime.EventStream, error) {
	if r.runFn != nil {
		return r.runFn(ctx, request)
	}
	release := make(chan struct{})
	close(release)
	stream := newFakeStream(nil, release)
	stream.result = pilotruntime.RunResult{Usage: &pilotruntime.Usage{State: pilotruntime.UsageStateExact}}
	return stream, nil
}

func (r *fakeRuntime) Abort(_ context.Context, runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aborts = append(r.aborts, runID)
	return nil
}

func (r *fakeRuntime) Probe(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes++
	return nil
}

func (r *fakeRuntime) Shutdown(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdowns++
	return nil
}

type fakeStream struct {
	events  chan pilotruntime.RuntimeEvent
	release <-chan struct{}
	result  pilotruntime.RunResult
	err     error
}

func newFakeStream(events []pilotruntime.RuntimeEvent, release <-chan struct{}) *fakeStream {
	channel := make(chan pilotruntime.RuntimeEvent, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	return &fakeStream{events: channel, release: release}
}

func (s *fakeStream) Events() <-chan pilotruntime.RuntimeEvent { return s.events }
func (s *fakeStream) Wait() (pilotruntime.RunResult, error) {
	<-s.release
	return s.result, s.err
}

type oneByteReader struct{ data []byte }

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	buffer[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}
