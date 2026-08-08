# Agent Identity and Cross-Replica Replay Verification

## Boundary

The client bearer token terminates at Gateway. Gateway keeps only the locally
verified tenant, username, and membership version, then signs each unary Logic
request over the gRPC method, deterministic protobuf payload hash, tenant,
actor, membership version, timestamp, and random nonce.

Logic verifies the workload signature, atomically consumes the nonce in shared
Redis, and reloads the ACTIVE membership, system roles, scopes, and current
membership version from PostgreSQL. Redis failure, an unknown role, a stale
membership version, or an IAM read failure rejects the request.

Pilot uses the same authoritative membership/role mapping for Run admission and
for every Tool Broker manifest/execute request. Capability claims do not cache
roles or scopes, so a downgrade takes effect on the next Tool request.

## Reproducible checks

```bash
GOCACHE=/tmp/resonance-gocache go test -race ./pkg/serviceauth -count=20
GOCACHE=/tmp/resonance-gocache go test -race ./pkg/iam ./pilot/identity -count=20
GOCACHE=/tmp/resonance-gocache go test ./repo \
  -run 'TestIdentityRepo_(ListTenantMembershipsNeverCrossesTenant|CreateIdentityAndResolveTenantScoped)' \
  -count=1 -v
```

The service-auth contract creates two independent Verifier instances backed by
one atomic SetNX test backend. The first accepts the signed request and the
second returns `ErrReplay`. A backend error returns
`ErrNonceStoreUnavailable`; there is no in-memory production fallback.
Signed user and Pilot calls do not use gRPC transparent retry because a retry
below the signing interceptor would reuse the same nonce. Business-level retry
re-enters the signer with a new nonce and reuses the durable idempotency key.

The PostgreSQL contract proves tenant-qualified membership reads and bounded
tenant member listing never return another tenant's row. The Pilot identity
contract proves disabled members and unknown roles fail closed, and that the
IAM read model contains no password or credential fields.

Loopback Tool Broker tests require an environment that permits binding
`127.0.0.1:0`:

```bash
GOCACHE=/tmp/resonance-gocache go test -race ./pilot/toolbroker -count=3
```
