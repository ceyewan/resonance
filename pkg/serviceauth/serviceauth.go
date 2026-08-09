// Package serviceauth signs internal unary gRPC calls with a short-lived,
// payload-bound service identity. It is an application-layer guard while the
// deployment migrates to mTLS; it does not replace transport encryption.
package serviceauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const (
	HeaderServiceID        = "x-resonance-service-id"
	HeaderTenantID         = "x-resonance-tenant-id"
	HeaderActor            = "x-resonance-actor"
	HeaderTimestamp        = "x-resonance-auth-timestamp"
	HeaderNonce            = "x-resonance-auth-nonce"
	HeaderSignature        = "x-resonance-auth-signature"
	HeaderPrincipalVersion = "x-resonance-principal-version"
)

var (
	ErrMissingCredentials    = errors.New("service credentials are missing")
	ErrInvalidCredentials    = errors.New("service credentials are invalid")
	ErrExpiredCredentials    = errors.New("service credentials are expired")
	ErrReplay                = errors.New("service credential nonce was already used")
	ErrNonceStoreUnavailable = errors.New("service credential nonce store is unavailable")
	hexNoncePattern          = regexp.MustCompile(`^[0-9a-f]{32}$`)
	grpcMethodPattern        = regexp.MustCompile(`^/[A-Za-z0-9_.]+/[A-Za-z0-9_]+$`)
)

type Claims struct {
	ServiceID        string
	TenantID         string
	Actor            string
	PrincipalVersion int64
}

type Signer struct {
	serviceID string
	secret    []byte
	now       func() time.Time
	nonce     func() (string, error)
}

type SignerOption func(*Signer)

func WithSignerClock(now func() time.Time) SignerOption {
	return func(signer *Signer) {
		if now != nil {
			signer.now = now
		}
	}
}

func WithNonceSource(source func() (string, error)) SignerOption {
	return func(signer *Signer) {
		if source != nil {
			signer.nonce = source
		}
	}
}

func NewSigner(serviceID string, secret []byte, options ...SignerOption) (*Signer, error) {
	if !validIdentity(serviceID) || len(secret) < 32 {
		return nil, fmt.Errorf("service signer requires a valid id and at least 32 secret bytes")
	}
	signer := &Signer{serviceID: serviceID, secret: append([]byte(nil), secret...), now: time.Now, nonce: randomNonce}
	for _, option := range options {
		option(signer)
	}
	return signer, nil
}

func (s *Signer) AuthenticateServiceCall(
	ctx context.Context,
	tenantID, actor, fullMethod, payloadHash string,
) (context.Context, error) {
	return s.authenticate(ctx, tenantID, actor, "", fullMethod, payloadHash)
}

func (s *Signer) AuthenticateUserCall(
	ctx context.Context,
	tenantID, actor string,
	principalVersion int64,
	fullMethod, payloadHash string,
) (context.Context, error) {
	if principalVersion < 1 {
		return nil, ErrInvalidCredentials
	}
	return s.authenticate(ctx, tenantID, actor, strconv.FormatInt(principalVersion, 10), fullMethod, payloadHash)
}

func (s *Signer) authenticate(
	ctx context.Context,
	tenantID, actor, principalVersion, fullMethod, payloadHash string,
) (context.Context, error) {
	if !validIdentity(tenantID) || !validIdentity(actor) || fullMethod == "" || !validSHA256(payloadHash) {
		return nil, ErrInvalidCredentials
	}
	nonce, err := s.nonce()
	if err != nil {
		return nil, fmt.Errorf("generate service auth nonce: %w", err)
	}
	if !hexNoncePattern.MatchString(nonce) {
		return nil, fmt.Errorf("generated service auth nonce is invalid")
	}
	timestamp := strconv.FormatInt(s.now().UTC().UnixMilli(), 10)
	signature := signature(s.secret, s.serviceID, fullMethod, tenantID, actor, principalVersion, timestamp, nonce, payloadHash)
	values := []string{
		HeaderServiceID, s.serviceID,
		HeaderTenantID, tenantID,
		HeaderActor, actor,
		HeaderTimestamp, timestamp,
		HeaderNonce, nonce,
		HeaderSignature, signature,
	}
	if principalVersion != "" {
		values = append(values, HeaderPrincipalVersion, principalVersion)
	}
	return metadata.AppendToOutgoingContext(
		ctx,
		values...,
	), nil
}

type ServicePolicy struct {
	Secret                  []byte
	AllowedActors           map[string]struct{}
	AllowAnyActor           bool
	AllowedMethods          map[string]struct{}
	AllowAnyMethod          bool
	AllowedTenants          map[string]struct{}
	AllowAnyTenant          bool
	RequirePrincipalVersion bool
}

type VerifierConfig struct {
	MaxSkew  time.Duration
	Services map[string]ServicePolicy
}

type Verifier struct {
	maxSkew  time.Duration
	services map[string]ServicePolicy
	now      func() time.Time
	nonces   NonceStore
}

// NonceStore atomically consumes a signed request nonce until expiresAt. A
// production verifier must use a store shared by every replica; the in-memory
// implementation is retained for unit tests and single-process callers.
type NonceStore interface {
	Consume(ctx context.Context, serviceID, nonce string, now, expiresAt time.Time) (bool, error)
}

type memoryNonceStore struct {
	mu     sync.Mutex
	nonces map[string]time.Time
}

type VerifierOption func(*Verifier)

func WithVerifierClock(now func() time.Time) VerifierOption {
	return func(verifier *Verifier) {
		if now != nil {
			verifier.now = now
		}
	}
}

func WithNonceStore(store NonceStore) VerifierOption {
	return func(verifier *Verifier) {
		if store != nil {
			verifier.nonces = store
		}
	}
}

func NewVerifier(config VerifierConfig, options ...VerifierOption) (*Verifier, error) {
	if config.MaxSkew <= 0 || len(config.Services) == 0 {
		return nil, fmt.Errorf("service verifier configuration is incomplete")
	}
	services := make(map[string]ServicePolicy, len(config.Services))
	for serviceID, policy := range config.Services {
		if !validIdentity(serviceID) || len(policy.Secret) < 32 ||
			policy.AllowAnyActor == (len(policy.AllowedActors) > 0) ||
			policy.AllowAnyMethod == (len(policy.AllowedMethods) > 0) ||
			policy.AllowAnyTenant == (len(policy.AllowedTenants) > 0) {
			return nil, fmt.Errorf("service verifier policy for %q is invalid", serviceID)
		}
		actors := make(map[string]struct{}, len(policy.AllowedActors))
		for actor := range policy.AllowedActors {
			if !validIdentity(actor) {
				return nil, fmt.Errorf("service verifier actor is invalid")
			}
			actors[actor] = struct{}{}
		}
		methods := make(map[string]struct{}, len(policy.AllowedMethods))
		for method := range policy.AllowedMethods {
			if !grpcMethodPattern.MatchString(method) {
				return nil, fmt.Errorf("service verifier method is invalid")
			}
			methods[method] = struct{}{}
		}
		tenants := make(map[string]struct{}, len(policy.AllowedTenants))
		for tenantID := range policy.AllowedTenants {
			if !validIdentity(tenantID) {
				return nil, fmt.Errorf("service verifier tenant is invalid")
			}
			tenants[tenantID] = struct{}{}
		}
		services[serviceID] = ServicePolicy{
			Secret:                  append([]byte(nil), policy.Secret...),
			AllowedActors:           actors,
			AllowAnyActor:           policy.AllowAnyActor,
			AllowedMethods:          methods,
			AllowAnyMethod:          policy.AllowAnyMethod,
			AllowedTenants:          tenants,
			AllowAnyTenant:          policy.AllowAnyTenant,
			RequirePrincipalVersion: policy.RequirePrincipalVersion,
		}
	}
	verifier := &Verifier{
		maxSkew:  config.MaxSkew,
		services: services,
		now:      time.Now,
		nonces:   &memoryNonceStore{nonces: make(map[string]time.Time)},
	}
	for _, option := range options {
		option(verifier)
	}
	return verifier, nil
}

func (v *Verifier) VerifyIncoming(ctx context.Context, fullMethod string, request proto.Message) (Claims, error) {
	if request == nil || fullMethod == "" {
		return Claims{}, ErrInvalidCredentials
	}
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Claims{}, ErrMissingCredentials
	}
	serviceID, okService := singleHeader(incoming, HeaderServiceID)
	tenantID, okTenant := singleHeader(incoming, HeaderTenantID)
	actor, okActor := singleHeader(incoming, HeaderActor)
	timestampText, okTimestamp := singleHeader(incoming, HeaderTimestamp)
	nonce, okNonce := singleHeader(incoming, HeaderNonce)
	providedSignature, okSignature := singleHeader(incoming, HeaderSignature)
	if !okService || !okTenant || !okActor || !okTimestamp || !okNonce || !okSignature {
		return Claims{}, ErrMissingCredentials
	}
	policy, ok := v.services[serviceID]
	if !ok || !validIdentity(tenantID) || !validIdentity(actor) || !hexNoncePattern.MatchString(nonce) {
		return Claims{}, ErrInvalidCredentials
	}
	if !policy.AllowAnyActor {
		if _, ok := policy.AllowedActors[actor]; !ok {
			return Claims{}, ErrInvalidCredentials
		}
	}
	if !policy.AllowAnyMethod {
		if _, ok := policy.AllowedMethods[fullMethod]; !ok {
			return Claims{}, ErrInvalidCredentials
		}
	}
	if !policy.AllowAnyTenant {
		if _, ok := policy.AllowedTenants[tenantID]; !ok {
			return Claims{}, ErrInvalidCredentials
		}
	}
	principalVersionText := ""
	if values := incoming.Get(HeaderPrincipalVersion); len(values) > 0 {
		if len(values) != 1 {
			return Claims{}, ErrInvalidCredentials
		}
		principalVersionText = values[0]
	}
	principalVersion := int64(0)
	if principalVersionText != "" {
		var err error
		principalVersion, err = strconv.ParseInt(principalVersionText, 10, 64)
		if err != nil || principalVersion < 1 {
			return Claims{}, ErrInvalidCredentials
		}
	}
	if policy.RequirePrincipalVersion && principalVersion < 1 {
		return Claims{}, ErrInvalidCredentials
	}
	timestampMillis, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}
	now := v.now().UTC()
	timestamp := time.UnixMilli(timestampMillis)
	if timestamp.Before(now.Add(-v.maxSkew)) || timestamp.After(now.Add(v.maxSkew)) {
		return Claims{}, ErrExpiredCredentials
	}
	payloadHash, err := PayloadHash(request)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}
	expected := signature(policy.Secret, serviceID, fullMethod, tenantID, actor, principalVersionText, timestampText, nonce, payloadHash)
	provided, err := hex.DecodeString(providedSignature)
	if err != nil {
		return Claims{}, ErrInvalidCredentials
	}
	expectedBytes, _ := hex.DecodeString(expected)
	if len(provided) != len(expectedBytes) || subtle.ConstantTimeCompare(provided, expectedBytes) != 1 {
		return Claims{}, ErrInvalidCredentials
	}
	// Retain the nonce through both the signed credential's validity window and
	// a full verifier window after first acceptance. Using timestamp+skew alone
	// would expire a nonce immediately near the lower acceptance boundary;
	// using now+skew alone would expire a future-dated credential too early.
	nonceExpiresAt := now.Add(v.maxSkew)
	if credentialExpiresAt := timestamp.Add(v.maxSkew); credentialExpiresAt.After(nonceExpiresAt) {
		nonceExpiresAt = credentialExpiresAt
	}
	consumed, err := v.nonces.Consume(ctx, serviceID, nonce, now, nonceExpiresAt)
	if err != nil {
		return Claims{}, ErrNonceStoreUnavailable
	}
	if !consumed {
		return Claims{}, ErrReplay
	}
	return Claims{ServiceID: serviceID, TenantID: tenantID, Actor: actor, PrincipalVersion: principalVersion}, nil
}

func (s *memoryNonceStore) Consume(
	ctx context.Context,
	serviceID, nonce string,
	now, expiresAt time.Time,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, expires := range s.nonces {
		if !expires.After(now) {
			delete(s.nonces, key)
		}
	}
	key := serviceID + "\x00" + nonce
	if _, exists := s.nonces[key]; exists {
		return false, nil
	}
	s.nonces[key] = expiresAt
	return true, nil
}

func PayloadHash(message proto.Message) (string, error) {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func HasIncomingCredentials(ctx context.Context) bool {
	metadataValue, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, header := range []string{
		HeaderServiceID,
		HeaderTenantID,
		HeaderActor,
		HeaderTimestamp,
		HeaderNonce,
		HeaderSignature,
		HeaderPrincipalVersion,
	} {
		if len(metadataValue.Get(header)) > 0 {
			return true
		}
	}
	return false
}

func signature(secret []byte, values ...string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("resonance-service-auth-v1\n"))
	_, _ = mac.Write([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

func singleHeader(values metadata.MD, name string) (string, bool) {
	items := values.Get(name)
	returnValue := ""
	if len(items) == 1 {
		returnValue = items[0]
	}
	return returnValue, len(items) == 1 && returnValue != ""
}

func randomNonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validIdentity(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
