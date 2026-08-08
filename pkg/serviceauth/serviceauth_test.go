package serviceauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	commonv1 "github.com/ceyewan/resonance/api/gen/go/common/v1"
	logicv1 "github.com/ceyewan/resonance/api/gen/go/logic/v1"
)

func TestServiceAuth_BindsServiceMethodTenantActorAndPayloadAndRejectsReplay(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewSigner("pilot-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }),
	)
	require.NoError(t, err)
	verifier, err := NewVerifier(VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)

	request := serviceAuthTestRequest("hello")
	payloadHash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(
		context.Background(), "tenant-a", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, payloadHash,
	)
	require.NoError(t, err)
	incoming := outgoingAsIncoming(t, outgoing)
	claims, err := verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.NoError(t, err)
	require.Equal(t, Claims{ServiceID: "pilot-service", TenantID: "tenant-a", Actor: "resonance-agent"}, claims)

	_, err = verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.ErrorIs(t, err, ErrReplay)
}

func TestServiceAuth_RejectsPayloadMethodActorAndTimestampTampering(t *testing.T) {
	now := time.Date(2026, 8, 8, 13, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	newPair := func(verifierNow time.Time, actor string) (*Signer, *Verifier) {
		signer, err := NewSigner("pilot-service", secret,
			WithSignerClock(func() time.Time { return now }),
			WithNonceSource(func() (string, error) { return "fedcba9876543210fedcba9876543210", nil }),
		)
		require.NoError(t, err)
		verifier, err := NewVerifier(VerifierConfig{
			MaxSkew: 30 * time.Second,
			Services: map[string]ServicePolicy{
				"pilot-service": {
					Secret: secret, AllowedActors: map[string]struct{}{actor: {}},
					AllowAnyMethod: true, AllowAnyTenant: true,
				},
			},
		}, WithVerifierClock(func() time.Time { return verifierNow }))
		require.NoError(t, err)
		return signer, verifier
	}

	tests := map[string]struct {
		verifierNow time.Time
		actor       string
		method      string
		request     *logicv1.SendEventRequest
		target      error
	}{
		"payload": {now, "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, serviceAuthTestRequest("tampered"), ErrInvalidCredentials},
		"method":  {now, "resonance-agent", "/other.Service/Method", serviceAuthTestRequest("hello"), ErrInvalidCredentials},
		"actor":   {now, "other-actor", logicv1.ChatService_SendEvent_FullMethodName, serviceAuthTestRequest("hello"), ErrInvalidCredentials},
		"expired": {now.Add(time.Minute), "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, serviceAuthTestRequest("hello"), ErrExpiredCredentials},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			signer, verifier := newPair(test.verifierNow, test.actor)
			original := serviceAuthTestRequest("hello")
			hash, err := PayloadHash(original)
			require.NoError(t, err)
			outgoing, err := signer.AuthenticateServiceCall(
				context.Background(), "tenant-a", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, hash,
			)
			require.NoError(t, err)
			_, err = verifier.VerifyIncoming(outgoingAsIncoming(t, outgoing), test.method, test.request)
			require.ErrorIs(t, err, test.target)
		})
	}
}

func TestServiceAuth_PolicyRejectsUnlistedMethodAndTenant(t *testing.T) {
	now := time.Date(2026, 8, 8, 13, 15, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)

	verify := func(tenantID, method, nonce string) error {
		signer, signerErr := NewSigner("pilot-service", secret,
			WithSignerClock(func() time.Time { return now }),
			WithNonceSource(func() (string, error) { return nonce, nil }),
		)
		require.NoError(t, signerErr)
		verifier, verifierErr := NewVerifier(VerifierConfig{
			MaxSkew: 30 * time.Second,
			Services: map[string]ServicePolicy{
				"pilot-service": {
					Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
					AllowedMethods: map[string]struct{}{logicv1.ChatService_SendEvent_FullMethodName: {}},
					AllowedTenants: map[string]struct{}{"tenant-a": {}},
				},
			},
		}, WithVerifierClock(func() time.Time { return now }))
		require.NoError(t, verifierErr)
		outgoing, signerErr := signer.AuthenticateServiceCall(
			context.Background(), tenantID, "resonance-agent", method, hash,
		)
		require.NoError(t, signerErr)
		_, verifyErr := verifier.VerifyIncoming(outgoingAsIncoming(t, outgoing), method, request)
		return verifyErr
	}

	require.NoError(t, verify("tenant-a", logicv1.ChatService_SendEvent_FullMethodName, "10101010101010101010101010101010"))
	require.ErrorIs(t, verify("tenant-a", logicv1.SessionService_GetHistoryEvents_FullMethodName, "20202020202020202020202020202020"), ErrInvalidCredentials)
	require.ErrorIs(t, verify("tenant-b", logicv1.ChatService_SendEvent_FullMethodName, "30303030303030303030303030303030"), ErrInvalidCredentials)
}

func TestServiceAuth_PolicyRequiresExplicitActorMethodAndTenantBoundaries(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	base := ServicePolicy{
		Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
		AllowedMethods: map[string]struct{}{logicv1.ChatService_SendEvent_FullMethodName: {}},
		AllowedTenants: map[string]struct{}{"tenant-a": {}},
	}
	mutations := []func(*ServicePolicy){
		func(policy *ServicePolicy) { policy.AllowedActors = nil },
		func(policy *ServicePolicy) { policy.AllowedMethods = nil },
		func(policy *ServicePolicy) { policy.AllowedTenants = nil },
		func(policy *ServicePolicy) { policy.AllowAnyMethod = true },
		func(policy *ServicePolicy) { policy.AllowAnyTenant = true },
	}
	for index, mutate := range mutations {
		policy := base
		mutate(&policy)
		_, err := NewVerifier(VerifierConfig{
			MaxSkew:  time.Second,
			Services: map[string]ServicePolicy{"pilot-service": policy},
		})
		require.Error(t, err, index)
	}
}

func TestServiceAuth_FutureTimestampNonceIsRetainedForItsEntireValidityWindow(t *testing.T) {
	base := time.Date(2026, 8, 8, 13, 30, 0, 0, time.UTC)
	verifierNow := base
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewSigner("pilot-service", secret,
		WithSignerClock(func() time.Time { return base.Add(30 * time.Second) }),
		WithNonceSource(func() (string, error) { return "11111111111111111111111111111111", nil }),
	)
	require.NoError(t, err)
	verifier, err := NewVerifier(VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, WithVerifierClock(func() time.Time { return verifierNow }))
	require.NoError(t, err)
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(context.Background(), "tenant-a", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, hash)
	require.NoError(t, err)
	incoming := outgoingAsIncoming(t, outgoing)
	_, err = verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.NoError(t, err)

	verifierNow = base.Add(31 * time.Second)
	_, err = verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.ErrorIs(t, err, ErrReplay)
}

func TestServiceAuth_PastBoundaryNonceCannotReplayInSameTick(t *testing.T) {
	now := time.Date(2026, 8, 8, 13, 45, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewSigner("pilot-service", secret,
		WithSignerClock(func() time.Time { return now.Add(-30 * time.Second) }),
		WithNonceSource(func() (string, error) { return "12121212121212121212121212121212", nil }),
	)
	require.NoError(t, err)
	verifier, err := NewVerifier(VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(
		context.Background(), "tenant-a", "resonance-agent", logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	incoming := outgoingAsIncoming(t, outgoing)
	_, err = verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.NoError(t, err)
	_, err = verifier.VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.ErrorIs(t, err, ErrReplay)
}

func TestServiceAuth_AnyPartialCredentialCommitsToServiceAuthPath(t *testing.T) {
	headers := []string{
		HeaderServiceID, HeaderTenantID, HeaderActor, HeaderTimestamp,
		HeaderNonce, HeaderSignature, HeaderPrincipalVersion,
	}
	for _, header := range headers {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(header, "value"))
		require.True(t, HasIncomingCredentials(ctx), header)
	}
}

func TestServiceAuth_UserCallBindsPrincipalVersionAndAllowsGatewayActor(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 0, 0, 0, time.UTC)
	secret := []byte("gateway-secret-0123456789abcdef01")
	signer, err := NewSigner("gateway-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "22222222222222222222222222222222", nil }),
	)
	require.NoError(t, err)
	verifier, err := NewVerifier(VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]ServicePolicy{
			"gateway-service": {
				Secret:                  secret,
				AllowAnyActor:           true,
				AllowAnyMethod:          true,
				AllowAnyTenant:          true,
				RequirePrincipalVersion: true,
			},
		},
	}, WithVerifierClock(func() time.Time { return now }))
	require.NoError(t, err)

	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateUserCall(
		context.Background(), "tenant-a", "alice", 7,
		logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	claims, err := verifier.VerifyIncoming(
		outgoingAsIncoming(t, outgoing), logicv1.ChatService_SendEvent_FullMethodName, request,
	)
	require.NoError(t, err)
	require.Equal(t, Claims{
		ServiceID:        "gateway-service",
		TenantID:         "tenant-a",
		Actor:            "alice",
		PrincipalVersion: 7,
	}, claims)
}

func TestServiceAuth_UserPolicyRejectsMissingOrTamperedPrincipalVersion(t *testing.T) {
	now := time.Date(2026, 8, 8, 14, 30, 0, 0, time.UTC)
	secret := []byte("gateway-secret-0123456789abcdef01")
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)

	newVerifier := func() *Verifier {
		verifier, verifierErr := NewVerifier(VerifierConfig{
			MaxSkew: 30 * time.Second,
			Services: map[string]ServicePolicy{
				"gateway-service": {
					Secret:                  secret,
					AllowAnyActor:           true,
					AllowAnyMethod:          true,
					AllowAnyTenant:          true,
					RequirePrincipalVersion: true,
				},
			},
		}, WithVerifierClock(func() time.Time { return now }))
		require.NoError(t, verifierErr)
		return verifier
	}

	serviceSigner, err := NewSigner("gateway-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "33333333333333333333333333333333", nil }),
	)
	require.NoError(t, err)
	withoutVersion, err := serviceSigner.AuthenticateServiceCall(
		context.Background(), "tenant-a", "alice", logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	_, err = newVerifier().VerifyIncoming(
		outgoingAsIncoming(t, withoutVersion), logicv1.ChatService_SendEvent_FullMethodName, request,
	)
	require.ErrorIs(t, err, ErrInvalidCredentials)

	userSigner, err := NewSigner("gateway-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "44444444444444444444444444444444", nil }),
	)
	require.NoError(t, err)
	withVersion, err := userSigner.AuthenticateUserCall(
		context.Background(), "tenant-a", "alice", 3,
		logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	metadataValue, ok := metadata.FromOutgoingContext(withVersion)
	require.True(t, ok)
	metadataValue = metadataValue.Copy()
	metadataValue.Set(HeaderPrincipalVersion, "4")
	tampered := metadata.NewIncomingContext(context.Background(), metadataValue)
	_, err = newVerifier().VerifyIncoming(tampered, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestServiceAuth_SharedRedisNonceStoreRejectsReplayAcrossVerifierInstances(t *testing.T) {
	now := time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	backend := &testRedisSetNXClient{keys: make(map[string]struct{})}
	newVerifier := func() *Verifier {
		store, err := NewRedisNonceStore(backend, "test:serviceauth:nonce:")
		require.NoError(t, err)
		verifier, err := NewVerifier(VerifierConfig{
			MaxSkew: 30 * time.Second,
			Services: map[string]ServicePolicy{
				"gateway-service": {
					Secret: secret, AllowAnyActor: true, AllowAnyMethod: true, AllowAnyTenant: true,
					RequirePrincipalVersion: true,
				},
			},
		}, WithVerifierClock(func() time.Time { return now }), WithNonceStore(store))
		require.NoError(t, err)
		return verifier
	}
	signer, err := NewSigner("gateway-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "55555555555555555555555555555555", nil }),
	)
	require.NoError(t, err)
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateUserCall(
		context.Background(), "tenant-a", "alice", 4,
		logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	incoming := outgoingAsIncoming(t, outgoing)

	_, err = newVerifier().VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.NoError(t, err)
	_, err = newVerifier().VerifyIncoming(incoming, logicv1.ChatService_SendEvent_FullMethodName, request)
	require.ErrorIs(t, err, ErrReplay)
}

func TestServiceAuth_DistributedNonceStoreFailureFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 8, 15, 30, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	store, err := NewRedisNonceStore(
		&testRedisSetNXClient{err: errors.New("redis unavailable")},
		"test:serviceauth:nonce:",
	)
	require.NoError(t, err)
	verifier, err := NewVerifier(VerifierConfig{
		MaxSkew: 30 * time.Second,
		Services: map[string]ServicePolicy{
			"pilot-service": {
				Secret: secret, AllowedActors: map[string]struct{}{"resonance-agent": {}},
				AllowAnyMethod: true, AllowAnyTenant: true,
			},
		},
	}, WithVerifierClock(func() time.Time { return now }), WithNonceStore(store))
	require.NoError(t, err)
	signer, err := NewSigner("pilot-service", secret,
		WithSignerClock(func() time.Time { return now }),
		WithNonceSource(func() (string, error) { return "66666666666666666666666666666666", nil }),
	)
	require.NoError(t, err)
	request := serviceAuthTestRequest("hello")
	hash, err := PayloadHash(request)
	require.NoError(t, err)
	outgoing, err := signer.AuthenticateServiceCall(
		context.Background(), "tenant-a", "resonance-agent",
		logicv1.ChatService_SendEvent_FullMethodName, hash,
	)
	require.NoError(t, err)
	_, err = verifier.VerifyIncoming(
		outgoingAsIncoming(t, outgoing), logicv1.ChatService_SendEvent_FullMethodName, request,
	)
	require.ErrorIs(t, err, ErrNonceStoreUnavailable)
}

type testRedisSetNXClient struct {
	mu   sync.Mutex
	keys map[string]struct{}
	err  error
}

func (c *testRedisSetNXClient) SetNX(
	_ context.Context,
	key string,
	_ any,
	_ time.Duration,
) *redis.BoolCmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return redis.NewBoolResult(false, c.err)
	}
	if _, exists := c.keys[key]; exists {
		return redis.NewBoolResult(false, nil)
	}
	c.keys[key] = struct{}{}
	return redis.NewBoolResult(true, nil)
}

func serviceAuthTestRequest(content string) *logicv1.SendEventRequest {
	return &logicv1.SendEventRequest{
		SessionId: "conversation-a",
		Payload: &logicv1.SendEventRequest_Message{Message: &commonv1.Message{
			Type: commonv1.MessageType_MESSAGE_TYPE_TEXT, Content: content, ClientMsgId: "agent:run-1:final",
		}},
	}
}

func outgoingAsIncoming(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	values, ok := metadata.FromOutgoingContext(ctx)
	require.True(t, ok)
	return metadata.NewIncomingContext(context.Background(), values.Copy())
}
