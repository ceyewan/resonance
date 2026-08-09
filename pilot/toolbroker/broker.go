package toolbroker

import (
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

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

type RunReader interface {
	GetAgentRun(ctx context.Context, tenantID, runID string) (*model.AgentRun, error)
}

type UserReader interface {
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
}

// PrincipalReader resolves the actor's current authoritative authorization state.
// Capability claims deliberately do not cache Roles or Scopes so a downgrade takes
// effect on the next manifest or execute request.
type PrincipalReader interface {
	ResolvePrincipal(ctx context.Context, tenantID, actorID string) (pilotruntime.ActorPrincipal, error)
}

// IAMUser is the deliberately narrow read model exposed to the Tool Broker. It
// excludes password hashes and any repository-specific persistence fields.
type IAMUser struct {
	TenantID string
	Username string
	Nickname string
	Email    string
	Phone    string
	Active   bool
}

// IAMReader must apply tenantID to every authoritative lookup. The Tool Broker
// always supplies tenantID from the verified Capability, never from model args.
type IAMReader interface {
	GetTenantUser(ctx context.Context, tenantID, username string) (*IAMUser, error)
	ListTenantUsers(ctx context.Context, tenantID string, limit int) ([]IAMUser, error)
}

// MutationPreparer 只准备审批事实；它不得在 Tool HTTP 请求内执行 IAM 写入。
type MutationPreparer interface {
	PrepareTenantMembershipStatus(context.Context, MembershipMutationPrepareRequest) (*MembershipMutationPrepareResult, error)
}

type MembershipMutationPrepareRequest struct {
	TenantID        string
	RunID           string
	CallID          string
	RequesterID     string
	TargetUsername  string
	DesiredStatus   string
	ExpectedVersion int64
	DryRun          bool
}

type MembershipMutationPrepareResult struct {
	CallID           string
	ArgsHash         string
	Status           string
	ExecutionStatus  string
	OperationID      string
	ExecutionSummary string
	ExpiresAt        time.Time
	Created          bool
}

type Option func(*brokerOptions)

type brokerOptions struct {
	principals PrincipalReader
	iam        IAMReader
	mutations  MutationPreparer
}

func WithPrincipalReader(reader PrincipalReader) Option {
	return func(options *brokerOptions) {
		if reader != nil {
			options.principals = reader
		}
	}
}

func WithIAMReader(reader IAMReader) Option {
	return func(options *brokerOptions) {
		if reader != nil {
			options.iam = reader
		}
	}
}

func WithMutationPreparer(preparer MutationPreparer) Option {
	return func(options *brokerOptions) {
		if preparer != nil {
			options.mutations = preparer
		}
	}
}

type Config struct {
	Address          string
	ProfileID        string
	ProfileVersion   int64
	MaxRequestBytes  int64
	MaxResponseBytes int
	RequestTimeout   time.Duration
}

type Broker struct {
	config     Config
	capability *CapabilityManager
	runs       RunReader
	users      UserReader
	principals PrincipalReader
	iam        IAMReader
	mutations  MutationPreparer
	registry   toolRegistry
	network    string
	listenPath string
	socketPath string
	endpoint   string

	mu       sync.Mutex
	listener net.Listener
	server   *http.Server
	errors   chan error
}

type Manifest struct {
	ProfileID      string         `json:"profile_id"`
	ProfileVersion int64          `json:"profile_version"`
	ExpiresAt      time.Time      `json:"expires_at"`
	Tools          []ToolManifest `json:"tools"`
}

type ToolManifest struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	InputSchema   map[string]any `json:"input_schema"`
	Risk          string         `json:"risk"`
	SchemaVersion int64          `json:"schema_version"`
}

type executeRequest struct {
	RunID      string          `json:"run_id"`
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Args       json.RawMessage `json:"args"`
}

type ToolResult struct {
	Status         string         `json:"status"`
	CallID         string         `json:"call_id"`
	ModelText      string         `json:"model_text"`
	DisplaySummary string         `json:"display_summary"`
	Data           map[string]any `json:"data"`
	IsError        bool           `json:"is_error"`
}

func New(config Config, capability *CapabilityManager, runs RunReader, users UserReader, opts ...Option) (*Broker, error) {
	if config.Address == "" || config.ProfileID == "" || config.ProfileVersion <= 0 || capability == nil || runs == nil || users == nil {
		return nil, fmt.Errorf("tool broker configuration is incomplete")
	}
	network, listenPath, socketPath, err := brokerListenSpec(config.Address)
	if err != nil {
		return nil, err
	}
	if config.MaxRequestBytes <= 0 {
		config.MaxRequestBytes = 64 << 10
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 64 << 10
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 5 * time.Second
	}
	options := brokerOptions{principals: userBackedPrincipalReader{users: users}}
	for _, option := range opts {
		option(&options)
	}
	broker := &Broker{
		config: config, capability: capability, runs: runs, users: users,
		principals: options.principals, iam: options.iam, errors: make(chan error, 1),
		mutations: options.mutations, network: network, listenPath: listenPath, socketPath: socketPath,
	}
	broker.registry = newToolRegistry(broker)
	return broker, nil
}

func (b *Broker) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listener != nil {
		return fmt.Errorf("tool broker already started")
	}
	if b.socketPath != "" {
		if err := prepareBrokerSocket(b.socketPath); err != nil {
			return err
		}
	}
	listener, err := net.Listen(b.network, b.listenPath)
	if err != nil {
		return fmt.Errorf("listen tool broker: %w", err)
	}
	if b.socketPath != "" {
		if err := os.Chmod(b.socketPath, 0o600); err != nil {
			_ = listener.Close()
			_ = removeBrokerSocket(b.socketPath)
			return fmt.Errorf("protect tool broker socket: %w", err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/manifest", b.handleManifest)
	mux.HandleFunc("POST /v1/execute", b.handleExecute)
	b.server = &http.Server{
		Handler: mux, ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: b.config.RequestTimeout, WriteTimeout: b.config.RequestTimeout, IdleTimeout: 30 * time.Second,
	}
	b.listener = listener
	if b.socketPath != "" {
		b.endpoint = "unix://" + b.socketPath
	} else {
		b.endpoint = "http://" + listener.Addr().String()
	}
	server := b.server
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case b.errors <- fmt.Errorf("tool broker serve: %w", serveErr):
			default:
			}
		}
	}()
	return nil
}

func (b *Broker) Errors() <-chan error { return b.errors }

func (b *Broker) Endpoint() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listener == nil {
		return ""
	}
	return b.endpoint
}

func (b *Broker) Close(ctx context.Context) error {
	b.mu.Lock()
	server := b.server
	listener := b.listener
	b.server = nil
	b.listener = nil
	b.endpoint = ""
	b.mu.Unlock()
	if server == nil {
		return nil
	}
	// Close the listener explicitly before removing the Unix socket. Start and
	// Close may race before http.Server.Serve has registered its listener; in
	// that window Shutdown alone can return while the Serve goroutine still owns
	// the socket and may later unlink a replacement path.
	var shutdownErr error
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		shutdownErr = errors.Join(shutdownErr, err)
	}
	if b.socketPath != "" {
		shutdownErr = errors.Join(shutdownErr, removeBrokerSocket(b.socketPath))
	}
	return shutdownErr
}

func brokerListenSpec(address string) (network, listenPath, socketPath string, err error) {
	if strings.HasPrefix(address, "unix://") {
		path := strings.TrimPrefix(address, "unix://")
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 240 {
			return "", "", "", fmt.Errorf("tool broker Unix socket must be a bounded absolute clean path")
		}
		return "unix", path, path, nil
	}
	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil {
		return "", "", "", fmt.Errorf("tool broker address: %w", splitErr)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", "", "", fmt.Errorf("tool broker must bind an explicit loopback IP or private Unix socket")
	}
	return "tcp", address, "", nil
}

func prepareBrokerSocket(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect tool broker socket directory: %w", err)
	}
	resolved, resolveErr := filepath.EvalSymlinks(parent)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 ||
		resolveErr != nil || resolved != parent {
		return fmt.Errorf("tool broker socket directory must be private and contain no symbolic links")
	}
	info, err = os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect tool broker socket: %w", err)
	case info.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("refusing to replace a non-socket tool broker path")
	default:
		return removeBrokerSocket(path)
	}
}

func removeBrokerSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect tool broker socket before removal: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove a non-socket tool broker path")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove tool broker socket: %w", err)
	}
	return nil
}

func (b *Broker) handleManifest(writer http.ResponseWriter, request *http.Request) {
	authorization, ok := b.authorize(writer, request)
	if !ok {
		return
	}
	manifest := Manifest{
		ProfileID: authorization.claims.ProfileID, ProfileVersion: authorization.claims.ProfileVersion,
		ExpiresAt: time.Unix(authorization.claims.ExpiresAt, 0).UTC(),
		Tools:     b.registry.manifests(authorization),
	}
	b.writeJSON(writer, http.StatusOK, manifest)
}

func (b *Broker) handleExecute(writer http.ResponseWriter, request *http.Request) {
	authorization, ok := b.authorize(writer, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, b.config.MaxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var execution executeRequest
	if err := decoder.Decode(&execution); err != nil || execution.RunID != authorization.claims.RunID ||
		execution.ToolCallID == "" || len(execution.ToolCallID) > 128 || execution.ToolName == "" || len(execution.ToolName) > 64 {
		b.writeError(writer, http.StatusBadRequest, "invalid tool request")
		return
	}
	if err := ensureSingleJSONValue(decoder); err != nil {
		b.writeError(writer, http.StatusBadRequest, "invalid tool request")
		return
	}
	output, err := b.registry.execute(request.Context(), ToolName(execution.ToolName), execution.ToolCallID, authorization, execution.Args)
	if err != nil {
		switch {
		case errors.Is(err, errToolNotAuthorized):
			b.writeError(writer, http.StatusForbidden, "tool is not authorized")
		case errors.Is(err, errToolArgumentsInvalid):
			b.writeError(writer, http.StatusBadRequest, "invalid tool arguments")
		default:
			b.writeError(writer, http.StatusServiceUnavailable, "tool result unavailable")
		}
		return
	}
	resultStatus := output.status
	if resultStatus == "" {
		resultStatus = "ok"
	}
	if !validToolResultStatus(resultStatus) {
		b.writeError(writer, http.StatusInternalServerError, "tool response unavailable")
		return
	}
	result := ToolResult{
		Status: resultStatus, CallID: execution.ToolCallID, ModelText: output.modelText,
		DisplaySummary: output.displaySummary, Data: output.data,
		IsError: resultStatus == "denied" || resultStatus == "retryable_error" || resultStatus == "final_error",
	}
	b.writeJSON(writer, http.StatusOK, result)
}

func validToolResultStatus(value string) bool {
	switch value {
	case "ok", "approval_required", "execution_pending", "executed", "denied", "retryable_error", "final_error":
		return true
	default:
		return false
	}
}

type requestAuthorization struct {
	claims    CapabilityClaims
	principal pilotruntime.ActorPrincipal
}

func (b *Broker) authorize(writer http.ResponseWriter, request *http.Request) (requestAuthorization, bool) {
	const prefix = "Bearer "
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, prefix) {
		b.writeError(writer, http.StatusUnauthorized, "missing capability")
		return requestAuthorization{}, false
	}
	claims, err := b.capability.Verify(strings.TrimPrefix(authorization, prefix))
	if err != nil || claims.ProfileID != b.config.ProfileID || claims.ProfileVersion != b.config.ProfileVersion {
		b.writeError(writer, http.StatusUnauthorized, "invalid capability")
		return requestAuthorization{}, false
	}
	run, err := b.runs.GetAgentRun(request.Context(), claims.TenantID, claims.RunID)
	if err != nil || run == nil || run.TenantID != claims.TenantID || run.RunID != claims.RunID ||
		run.ActorID != claims.ActorID || run.ActorUsername != claims.Username ||
		run.ProfileID != claims.ProfileID || run.ProfileVersion != claims.ProfileVersion ||
		(run.Status != model.AgentRunStatusClaimed && run.Status != model.AgentRunStatusStartingRuntime && run.Status != model.AgentRunStatusRunning) {
		b.writeError(writer, http.StatusUnauthorized, "capability is no longer active")
		return requestAuthorization{}, false
	}
	principal, err := b.principals.ResolvePrincipal(request.Context(), claims.TenantID, claims.ActorID)
	if err != nil || principal.TenantID != claims.TenantID || principal.ActorID != claims.ActorID ||
		principal.Username != claims.Username {
		b.writeError(writer, http.StatusUnauthorized, "capability is no longer active")
		return requestAuthorization{}, false
	}
	return requestAuthorization{claims: claims, principal: principal}, true
}

type userBackedPrincipalReader struct{ users UserReader }

func (r userBackedPrincipalReader) ResolvePrincipal(ctx context.Context, tenantID, actorID string) (pilotruntime.ActorPrincipal, error) {
	user, err := r.users.GetUserByUsername(ctx, actorID)
	if err != nil || user == nil || user.Kind != model.UserKindHuman || user.Username != actorID {
		return pilotruntime.ActorPrincipal{}, fmt.Errorf("principal is unavailable")
	}
	return pilotruntime.ActorPrincipal{
		TenantID: tenantID, ActorID: actorID, Username: user.Username,
	}, nil
}

func (b *Broker) writeJSON(writer http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > b.config.MaxResponseBytes {
		b.writeError(writer, http.StatusInternalServerError, "tool response unavailable")
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}

func (b *Broker) writeError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"error":`+strconvQuote(message)+`}`)
}

func ensureSingleJSONValue(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values")
}

func strconvQuote(value string) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}
