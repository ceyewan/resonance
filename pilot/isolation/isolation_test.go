package isolation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
	"github.com/ceyewan/resonance/pilot/session"
	"github.com/ceyewan/resonance/pilot/toolbroker"
)

const (
	tenantCount   = 8
	runsPerTenant = 8
)

// TestConcurrentTenantIsolation is a bounded production-shape capacity gate:
// each configured tenant owns a distinct Broker secret/socket and Session
// root, while all Runs execute concurrently. It validates both positive
// routing and deliberate cross-tenant replay attempts under the race detector.
func TestConcurrentTenantIsolation(t *testing.T) {
	temporaryRoot, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	root, err := os.MkdirTemp(temporaryRoot, "res-iso-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(root)) })

	harnesses := make([]*tenantHarness, 0, tenantCount)
	allCredentials := make([]runCredential, 0, tenantCount*runsPerTenant)
	for tenantIndex := range tenantCount {
		harness := newTenantHarness(t, root, tenantIndex)
		harnesses = append(harnesses, harness)
		allCredentials = append(allCredentials, harness.credentials...)
	}

	type outcome struct {
		credential runCredential
		staging    session.Staging
		candidate  session.Candidate
		observable string
		err        error
	}
	outcomes := make(chan outcome, len(allCredentials))
	var workers sync.WaitGroup
	for tenantIndex, harness := range harnesses {
		crossBroker := harnesses[(tenantIndex+1)%len(harnesses)]
		for _, credential := range harness.credentials {
			workers.Add(1)
			go func(current, cross *tenantHarness, credential runCredential) {
				defer workers.Done()
				result := outcome{credential: credential}
				result.staging, result.candidate, result.observable, result.err = exerciseRun(current, cross, credential)
				outcomes <- result
			}(harness, crossBroker, credential)
		}
	}
	workers.Wait()
	close(outcomes)

	seenStaging := make(map[string]struct{}, len(allCredentials))
	observed := make(map[string]string, len(allCredentials))
	for result := range outcomes {
		require.NoError(t, result.err)
		if _, exists := seenStaging[result.staging.Snapshot.Directory]; exists {
			t.Fatalf("staging directory reused: %s", result.staging.Snapshot.Directory)
		}
		seenStaging[result.staging.Snapshot.Directory] = struct{}{}
		observed[result.credential.run.RunID] = result.observable

		harness := harnesses[result.credential.tenantIndex]
		stored, readErr := os.ReadFile(filepath.Join(harness.sessionRoot, filepath.FromSlash(result.candidate.SessionRef)))
		require.NoError(t, readErr)
		require.Equal(t, result.credential.sessionPayload, stored)
		require.Equal(t, int64(len(stored)), result.candidate.ByteSize)
	}
	require.Len(t, seenStaging, tenantCount*runsPerTenant)
	require.Len(t, observed, tenantCount*runsPerTenant)

	// Every observable surface may contain its own deliberately non-secret
	// display value, but never another tenant's marker, password, Session bytes,
	// or raw Capability. Secret.String is included to cover accidental fmt/log
	// formatting at the runtime boundary.
	for _, credential := range allCredentials {
		own := observed[credential.run.RunID]
		require.Contains(t, own, credential.username)
		require.NotContains(t, own, credential.password)
		require.NotContains(t, own, credential.capability.Reveal())
		for _, other := range allCredentials {
			if other.run.RunID == credential.run.RunID {
				continue
			}
			require.NotContains(t, own, other.username)
			require.NotContains(t, own, other.password)
			require.NotContains(t, own, string(other.sessionPayload))
			require.NotContains(t, own, other.capability.Reveal())
		}
	}

	// A committed object from another tenant cannot be resumed even when a
	// caller substitutes all opaque Session fields from that tenant.
	first := allCredentials[0]
	second := allCredentials[runsPerTenant]
	firstHarness := harnesses[first.tenantIndex]
	secondOutcome := findOutcomeCandidate(t, second, harnesses[second.tenantIndex])
	_, err = firstHarness.sessions.Start(context.Background(), first.run, &model.AgentSessionBinding{
		TenantID: second.run.TenantID, ConversationID: second.run.ConversationID,
		RuntimeSessionID: secondOutcome.SessionID, SessionRef: secondOutcome.SessionRef,
		Checksum: secondOutcome.Checksum, Generation: 1, LastCommittedEntryID: secondOutcome.LeafEntryID,
		Status: model.AgentSessionBindingStatusActive, RuntimeKind: second.run.RuntimeKind,
		RuntimeVersion: second.run.RuntimeVersion, BridgeVersion: second.run.BridgeVersion,
		ProfileID: second.run.ProfileID, ProfileVersion: second.run.ProfileVersion,
	})
	require.Error(t, err)
}

type tenantHarness struct {
	tenantIndex int
	tenantID    string
	socketPath  string
	sessionRoot string
	broker      *toolbroker.Broker
	client      *http.Client
	sessions    *session.LocalManager
	credentials []runCredential
}

type runCredential struct {
	tenantIndex    int
	run            *model.AgentRun
	username       string
	password       string
	sessionPayload []byte
	capability     pilotruntime.Secret
}

func newTenantHarness(t *testing.T, root string, tenantIndex int) *tenantHarness {
	t.Helper()
	tenantID := fmt.Sprintf("tenant-%02d", tenantIndex)
	tenantRoot := filepath.Join(root, tenantID)
	socketRoot := filepath.Join(tenantRoot, "sockets")
	require.NoError(t, os.MkdirAll(socketRoot, 0o700))
	require.NoError(t, os.Chmod(socketRoot, 0o700))
	socketPath := filepath.Join(socketRoot, "broker.sock")
	sessionRoot := filepath.Join(tenantRoot, "sessions")
	sessions, err := session.NewLocalManager(session.LocalConfig{Root: sessionRoot, MaxSnapshotBytes: 64 << 10})
	require.NoError(t, err)

	runs := &runReader{runs: make(map[string]*model.AgentRun)}
	users := &userReader{users: make(map[string]*model.User)}
	secret := sha256.Sum256([]byte("resonance-isolation-capability\x00" + tenantID))
	capabilities, err := toolbroker.NewCapabilityManager(secret[:], 15*time.Minute)
	require.NoError(t, err)
	broker, err := toolbroker.New(toolbroker.Config{
		Address: "unix://" + socketPath, ProfileID: "user-assistant", ProfileVersion: 1,
		MaxRequestBytes: 4 << 10, MaxResponseBytes: 8 << 10, RequestTimeout: 5 * time.Second,
	}, capabilities, runs, users)
	require.NoError(t, err)
	require.NoError(t, broker.Start())

	harness := &tenantHarness{
		tenantIndex: tenantIndex, tenantID: tenantID, socketPath: socketPath,
		sessionRoot: sessionRoot, broker: broker, sessions: sessions,
		client: unixHTTPClient(socketPath),
	}
	for runIndex := range runsPerTenant {
		username := fmt.Sprintf("t%02d-user-%02d", tenantIndex, runIndex)
		run := &model.AgentRun{
			RunID: fmt.Sprintf("run-t%02d-%02d", tenantIndex, runIndex), TenantID: tenantID,
			ConversationID: fmt.Sprintf("conversation-t%02d-%02d", tenantIndex, runIndex),
			ActorID:        username, ActorUsername: username, ProfileID: "user-assistant", ProfileVersion: 1,
			RuntimeKind: "pi", RuntimeVersion: "0.84.1", BridgeVersion: "0.1.0",
			Status: model.AgentRunStatusRunning,
		}
		password := fmt.Sprintf("password-marker-t%02d-%02d", tenantIndex, runIndex)
		users.users[username] = &model.User{
			Username: username, Nickname: "display-" + username, Password: password, Kind: model.UserKindHuman,
		}
		runs.runs[runKey(tenantID, run.RunID)] = run
		capability, issueErr := capabilities.IssueCapability(context.Background(), run, pilotruntime.ActorPrincipal{
			TenantID: tenantID, ActorID: username, Username: username,
		})
		require.NoError(t, issueErr)
		harness.credentials = append(harness.credentials, runCredential{
			tenantIndex: tenantIndex, run: run, username: username, password: password,
			sessionPayload: fmt.Appendf(nil, "{\"tenant\":%q,\"run\":%q,\"marker\":%q}\n", tenantID, run.RunID, "session-"+username),
			capability:     capability,
		})
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, broker.Close(ctx))
		harness.client.CloseIdleConnections()
		require.NoError(t, sessions.Close())
	})
	return harness
}

func exerciseRun(current, cross *tenantHarness, credential runCredential) (session.Staging, session.Candidate, string, error) {
	staging, err := current.sessions.Start(context.Background(), credential.run, nil)
	if err != nil {
		return session.Staging{}, session.Candidate{}, "", err
	}
	sessionFile := filepath.Join(staging.Snapshot.Directory, "session.jsonl")
	if err := os.WriteFile(sessionFile, credential.sessionPayload, 0o600); err != nil {
		return staging, session.Candidate{}, "", err
	}
	candidate, err := current.sessions.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: "pi-" + credential.run.RunID, SessionFile: sessionFile, LeafEntryID: "leaf-" + credential.run.RunID,
	})
	if err != nil {
		return staging, session.Candidate{}, "", err
	}

	status, ownBody, err := executeProfile(current.client, credential.run.RunID, credential.capability.Reveal(), "call-"+credential.run.RunID)
	if err != nil {
		return staging, candidate, "", err
	}
	if status != http.StatusOK {
		return staging, candidate, "", fmt.Errorf("own Broker returned %d: %s", status, ownBody)
	}
	var result toolbroker.ToolResult
	if err := json.Unmarshal(ownBody, &result); err != nil {
		return staging, candidate, "", err
	}
	if result.Data["username"] != credential.username {
		return staging, candidate, "", fmt.Errorf("own Tool Result identity mismatch")
	}

	crossStatus, crossBody, err := executeProfile(cross.client, credential.run.RunID, credential.capability.Reveal(), "cross-"+credential.run.RunID)
	if err != nil {
		return staging, candidate, "", err
	}
	if crossStatus != http.StatusUnauthorized {
		return staging, candidate, "", fmt.Errorf("cross-tenant Capability returned %d: %s", crossStatus, crossBody)
	}
	observable := string(ownBody) + "\n" + string(crossBody) + "\n" + credential.capability.String()
	return staging, candidate, observable, nil
}

func executeProfile(client *http.Client, runID, capability, callID string) (int, []byte, error) {
	payload, err := json.Marshal(map[string]any{
		"run_id": runID, "tool_call_id": callID, "tool_name": string(toolbroker.ToolGetMyProfile),
		"args": map[string]any{},
	})
	if err != nil {
		return 0, nil, err
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://unix/v1/execute", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+capability)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	return response.StatusCode, body, err
}

func unixHTTPClient(socketPath string) *http.Client {
	transport := &http.Transport{
		MaxConnsPerHost: tenantCount * runsPerTenant,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func findOutcomeCandidate(t *testing.T, credential runCredential, harness *tenantHarness) session.Candidate {
	t.Helper()
	staging, err := harness.sessions.Start(context.Background(), credential.run, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, harness.sessions.Discard(context.Background(), staging)) })
	path := filepath.Join(staging.Snapshot.Directory, "session.jsonl")
	require.NoError(t, os.WriteFile(path, credential.sessionPayload, 0o600))
	candidate, err := harness.sessions.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: "pi-" + credential.run.RunID, SessionFile: path, LeafEntryID: "leaf-" + credential.run.RunID,
	})
	require.NoError(t, err)
	return candidate
}

type runReader struct {
	mu   sync.RWMutex
	runs map[string]*model.AgentRun
}

func (r *runReader) GetAgentRun(_ context.Context, tenantID, runID string) (*model.AgentRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run := r.runs[runKey(tenantID, runID)]
	if run == nil {
		return nil, errors.New("run not found")
	}
	clone := *run
	return &clone, nil
}

type userReader struct {
	mu    sync.RWMutex
	users map[string]*model.User
}

func (r *userReader) GetUserByUsername(_ context.Context, username string) (*model.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user := r.users[username]
	if user == nil {
		return nil, errors.New("user not found")
	}
	clone := *user
	return &clone, nil
}

func runKey(tenantID, runID string) string { return tenantID + "\x00" + runID }
