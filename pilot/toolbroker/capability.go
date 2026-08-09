package toolbroker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ceyewan/resonance/model"
	"github.com/ceyewan/resonance/pilot/runtime"
)

var (
	ErrCapabilityInvalid = errors.New("agent capability is invalid")
	ErrCapabilityExpired = errors.New("agent capability is expired")
)

type CapabilityClaims struct {
	Version        int    `json:"v"`
	RunID          string `json:"run_id"`
	TenantID       string `json:"tenant_id"`
	ActorID        string `json:"actor_id"`
	Username       string `json:"username"`
	ProfileID      string `json:"profile_id"`
	ProfileVersion int64  `json:"profile_version"`
	ExpiresAt      int64  `json:"expires_at"`
	Nonce          string `json:"nonce"`
}

type CapabilityManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
	nonce  func() (string, error)
}

type CapabilityOption func(*CapabilityManager)

func WithCapabilityClock(now func() time.Time) CapabilityOption {
	return func(manager *CapabilityManager) {
		if now != nil {
			manager.now = now
		}
	}
}

func WithCapabilityNonceSource(source func() (string, error)) CapabilityOption {
	return func(manager *CapabilityManager) {
		if source != nil {
			manager.nonce = source
		}
	}
}

func NewCapabilityManager(secret []byte, ttl time.Duration, options ...CapabilityOption) (*CapabilityManager, error) {
	if len(secret) < 32 || ttl <= 0 || ttl > time.Hour {
		return nil, fmt.Errorf("capability requires at least 32 secret bytes and a TTL no greater than one hour")
	}
	manager := &CapabilityManager{secret: append([]byte(nil), secret...), ttl: ttl, now: time.Now, nonce: capabilityNonce}
	for _, option := range options {
		option(manager)
	}
	return manager, nil
}

func (m *CapabilityManager) IssueCapability(_ context.Context, run *model.AgentRun, principal runtime.ActorPrincipal) (runtime.Secret, error) {
	if run == nil || run.RunID == "" || run.TenantID == "" || run.ActorID == "" || run.ActorUsername == "" ||
		run.ProfileID == "" || run.ProfileVersion <= 0 {
		return runtime.Secret{}, ErrCapabilityInvalid
	}
	if principal.TenantID != run.TenantID || principal.ActorID != run.ActorID || principal.Username != run.ActorUsername {
		return runtime.Secret{}, ErrCapabilityInvalid
	}
	nonce, err := m.nonce()
	if err != nil {
		return runtime.Secret{}, fmt.Errorf("generate capability nonce: %w", err)
	}
	claims := CapabilityClaims{
		Version: 1, RunID: run.RunID, TenantID: run.TenantID, ActorID: run.ActorID, Username: run.ActorUsername,
		ProfileID: run.ProfileID, ProfileVersion: run.ProfileVersion,
		ExpiresAt: m.now().UTC().Add(m.ttl).Unix(), Nonce: nonce,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return runtime.Secret{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte("resonance-agent-capability-v1." + encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return runtime.NewSecret(encoded + "." + signature), nil
}

func (m *CapabilityManager) Verify(token string) (CapabilityClaims, error) {
	payloadText, signatureText, ok := strings.Cut(token, ".")
	if !ok || payloadText == "" || signatureText == "" || strings.Contains(signatureText, ".") {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte("resonance-agent-capability-v1." + payloadText))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var claims CapabilityClaims
	if err := decoder.Decode(&claims); err != nil {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	if claims.Version != 1 || claims.RunID == "" || claims.TenantID == "" || claims.ActorID == "" ||
		claims.Username == "" || claims.ProfileID == "" || claims.ProfileVersion <= 0 || claims.Nonce == "" || len(claims.Nonce) > 64 {
		return CapabilityClaims{}, ErrCapabilityInvalid
	}
	if !time.Unix(claims.ExpiresAt, 0).After(m.now().UTC()) {
		return CapabilityClaims{}, ErrCapabilityExpired
	}
	return claims, nil
}

func capabilityNonce() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce[:]), nil
}
