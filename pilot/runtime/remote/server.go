package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type ServerConfig struct {
	SocketPath      string
	SessionRoot     string
	MaxRequestBytes int64
	MaxFrameBytes   int
	HeaderTimeout   time.Duration
}

type Server struct {
	config       ServerConfig
	runtime      pilotruntime.AgentRuntime
	errors       chan error
	shutdowns    chan struct{}
	shutdownOnce sync.Once

	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
}

func NewServer(config ServerConfig, runtime pilotruntime.AgentRuntime) (*Server, error) {
	if runtime == nil {
		return nil, fmt.Errorf("remote runtime server requires a runtime")
	}
	if err := setServerDefaultsAndValidate(&config); err != nil {
		return nil, err
	}
	return &Server{
		config: config, runtime: runtime, errors: make(chan error, 1),
		shutdowns: make(chan struct{}),
	}, nil
}

func setServerDefaultsAndValidate(config *ServerConfig) error {
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = 1 << 20
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = 1 << 20
	}
	if config.HeaderTimeout == 0 {
		config.HeaderTimeout = 2 * time.Second
	}
	if err := validateSocketPath(config.SocketPath); err != nil {
		return err
	}
	if config.SessionRoot == "" || !filepath.IsAbs(config.SessionRoot) ||
		filepath.Clean(config.SessionRoot) != config.SessionRoot {
		return fmt.Errorf("remote runtime server requires an absolute clean session root")
	}
	if config.MaxRequestBytes < 1 || config.MaxRequestBytes > 8<<20 ||
		config.MaxFrameBytes < 1 || config.MaxFrameBytes > 8<<20 || config.HeaderTimeout <= 0 {
		return fmt.Errorf("remote runtime server limits are invalid")
	}
	return nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return fmt.Errorf("remote runtime server already started")
	}
	if err := prepareSocketPath(s.config.SocketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.config.SocketPath)
	if err != nil {
		return fmt.Errorf("listen remote runtime socket: %w", err)
	}
	if err := os.Chmod(s.config.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = removeSocket(s.config.SocketPath)
		return fmt.Errorf("protect remote runtime socket: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/probe", s.handleProbe)
	mux.HandleFunc("POST /v1/run", s.handleRun)
	mux.HandleFunc("POST /v1/abort", s.handleAbort)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: s.config.HeaderTimeout,
		ReadTimeout:       s.config.HeaderTimeout,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	s.listener = listener
	s.http = server
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case s.errors <- fmt.Errorf("remote runtime serve: %w", serveErr):
			default:
			}
		}
	}()
	return nil
}

func (s *Server) Errors() <-chan error { return s.errors }

// Shutdowns closes after the private control plane has successfully drained
// the runtime. The host uses it to drop readiness and exit, so a later Pilot
// restart cannot be sent to an Adapter that has already been permanently shut
// down while the sidecar still advertises healthy.
func (s *Server) Shutdowns() <-chan struct{} { return s.shutdowns }

func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	server := s.http
	listener := s.listener
	s.http = nil
	s.listener = nil
	s.mu.Unlock()
	var result error
	if err := s.runtime.Shutdown(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("shutdown runtime: %w", err))
	}
	// Close the listener even if Serve has not yet registered it with the HTTP
	// server. This prevents a late Serve goroutine from retaining or unlinking a
	// replacement Unix socket after Close returns.
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	if server != nil {
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, fmt.Errorf("shutdown remote runtime server: %w", err))
		}
	}
	if err := removeSocket(s.config.SocketPath); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (s *Server) handleProbe(writer http.ResponseWriter, request *http.Request) {
	if !validEmptyRequest(request) {
		writeHTTPError(writer, http.StatusBadRequest, "invalid probe request", &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
		return
	}
	if err := s.runtime.Probe(request.Context()); err != nil {
		writeHTTPError(writer, http.StatusServiceUnavailable, "runtime probe failed", &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
		return
	}
	writeJSON(writer, http.StatusOK, statusWire{OK: true})
}

func (s *Server) handleAbort(writer http.ResponseWriter, request *http.Request) {
	var command runControlWire
	if err := decodeRequest(writer, request, s.config.MaxRequestBytes, &command); err != nil || !validRunID(command.RunID) {
		writeHTTPError(writer, http.StatusBadRequest, "invalid abort request", &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
		return
	}
	if err := s.runtime.Abort(request.Context(), command.RunID); err != nil {
		writeHTTPError(writer, http.StatusConflict, "runtime abort failed", &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
		return
	}
	writeJSON(writer, http.StatusOK, statusWire{OK: true})
}

func (s *Server) handleShutdown(writer http.ResponseWriter, request *http.Request) {
	if !validEmptyRequest(request) {
		writeHTTPError(writer, http.StatusBadRequest, "invalid shutdown request", &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
		return
	}
	if err := s.runtime.Shutdown(request.Context()); err != nil {
		writeHTTPError(writer, http.StatusServiceUnavailable, "runtime shutdown failed", &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
		return
	}
	writeJSON(writer, http.StatusOK, statusWire{OK: true})
	s.shutdownOnce.Do(func() { close(s.shutdowns) })
}

func (s *Server) handleRun(writer http.ResponseWriter, request *http.Request) {
	var wire runRequestWire
	if err := decodeRequest(writer, request, s.config.MaxRequestBytes, &wire); err != nil || wire.ProtocolVersion != protocolVersion {
		writeHTTPError(writer, http.StatusBadRequest, "invalid run request", &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
		return
	}
	if !sessionSnapshotInsideRoot(s.config.SessionRoot, wire.Session) || !wire.Limits.Valid() {
		writeHTTPError(writer, http.StatusBadRequest, "invalid run boundary", &pilotruntime.Usage{State: pilotruntime.UsageStateNotStarted})
		return
	}
	runRequest := pilotruntime.RunRequest{
		RunID: wire.RunID, ConversationID: wire.ConversationID, Prompt: wire.Prompt,
		Session: wire.Session, Profile: wire.Profile, Actor: wire.Actor,
		Capability: pilotruntime.NewSecret(wire.Capability), Limits: wire.Limits,
	}
	stream, err := s.runtime.Run(request.Context(), runRequest)
	if err != nil {
		usage := usageFromError(err, pilotruntime.UsageStateUnknown)
		writeHTTPError(writer, http.StatusBadGateway, "runtime rejected run", usage)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		_ = s.runtime.Abort(context.Background(), wire.RunID)
		writeHTTPError(writer, http.StatusInternalServerError, "streaming unavailable", &pilotruntime.Usage{State: pilotruntime.UsageStateUnknown})
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if err := writeFrame(writer, runFrame{ProtocolVersion: protocolVersion, Type: frameAccepted}, s.config.MaxFrameBytes); err != nil {
		_ = s.runtime.Abort(context.Background(), wire.RunID)
		return
	}
	flusher.Flush()

	resultCh := make(chan runWaitResult, 1)
	go func() {
		result, waitErr := stream.Wait()
		resultCh <- runWaitResult{result: result, err: waitErr}
	}()
	events := stream.Events()
	for {
		select {
		case <-request.Context().Done():
			_ = s.runtime.Abort(context.Background(), wire.RunID)
			return
		case event, open := <-events:
			if !open {
				events = nil
				continue
			}
			copy := event
			if err := writeFrame(writer, runFrame{ProtocolVersion: protocolVersion, Type: frameEvent, Event: &copy}, s.config.MaxFrameBytes); err != nil {
				_ = s.runtime.Abort(context.Background(), wire.RunID)
				return
			}
			flusher.Flush()
		case waited := <-resultCh:
			if waited.err != nil {
				frame := runFrame{ProtocolVersion: protocolVersion, Type: frameError, Error: &runtimeErrorWire{
					Kind: errorKind(waited.err), Message: "remote runtime execution failed", Usage: waited.result.Usage,
				}}
				_ = writeFrame(writer, frame, s.config.MaxFrameBytes)
				flusher.Flush()
				return
			}
			result := waited.result
			_ = writeFrame(writer, runFrame{ProtocolVersion: protocolVersion, Type: frameResult, Result: &result}, s.config.MaxFrameBytes)
			flusher.Flush()
			return
		}
	}
}

func sessionSnapshotInsideRoot(root string, snapshot pilotruntime.SessionSnapshot) bool {
	if snapshot.Directory == "" || !filepath.IsAbs(snapshot.Directory) || filepath.Clean(snapshot.Directory) != snapshot.Directory {
		return false
	}
	relative, err := filepath.Rel(root, snapshot.Directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	if snapshot.FilePath == "" {
		return true
	}
	if !filepath.IsAbs(snapshot.FilePath) || filepath.Clean(snapshot.FilePath) != snapshot.FilePath {
		return false
	}
	fileRelative, err := filepath.Rel(snapshot.Directory, snapshot.FilePath)
	return err == nil && fileRelative != "." && fileRelative != ".." &&
		!strings.HasPrefix(fileRelative, ".."+string(filepath.Separator))
}

type runWaitResult struct {
	result pilotruntime.RunResult
	err    error
}

func validEmptyRequest(request *http.Request) bool {
	return request.Body != nil && request.ContentLength == 0 && len(request.TransferEncoding) == 0 &&
		request.Header.Get("Content-Encoding") == ""
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, limit int64, target any) error {
	if request.Header.Get("Content-Encoding") != "" ||
		!strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		return fmt.Errorf("invalid content type")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request must contain one JSON object")
	}
	return nil
}

func writeFrame(writer io.Writer, frame runFrame, limit int) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload)+1 > limit {
		return fmt.Errorf("remote runtime frame exceeds limit")
	}
	payload = append(payload, '\n')
	_, err = io.Copy(writer, bytes.NewReader(payload))
	return err
}

func writeHTTPError(writer http.ResponseWriter, status int, message string, usage *pilotruntime.Usage) {
	writeJSON(writer, status, runtimeErrorWire{Kind: "runtime", Message: message, Usage: usage})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func usageFromError(err error, fallback pilotruntime.UsageState) *pilotruntime.Usage {
	var runError *pilotruntime.RunError
	if errors.As(err, &runError) && runError.Usage != nil {
		copy := *runError.Usage
		return &copy
	}
	return &pilotruntime.Usage{State: fallback}
}

func validRunID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func validateSocketPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 240 {
		return fmt.Errorf("remote runtime socket path must be a bounded absolute clean path")
	}
	if filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return fmt.Errorf("remote runtime socket path is invalid")
	}
	return nil
}

func prepareSocketPath(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect remote runtime socket directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("remote runtime socket directory must be a private real directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || resolvedParent != parent {
		return fmt.Errorf("remote runtime socket directory must not traverse symbolic links")
	}
	info, err = os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect remote runtime socket: %w", err)
	case info.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("refusing to replace a non-socket runtime path")
	default:
		return removeSocket(path)
	}
}

func removeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect runtime socket before removal: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove a non-socket runtime path")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove runtime socket: %w", err)
	}
	return nil
}
