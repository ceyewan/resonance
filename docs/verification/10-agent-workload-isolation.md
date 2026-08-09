# Agent Workload Isolation Verification

## Scope

This slice turns Profile separation into an authorization boundary. It covers
the Gateway, user-assistant Pilot and iam-admin Pilot service identities at the
Logic ingress, plus exact AI Session Profile binding for Bot Chat/History calls.

## Invariants

- Every service policy explicitly chooses actor, gRPC method and tenant
  boundaries. An omitted boundary is a configuration error.
- Gateway can read or decide approvals, but cannot create an approval or call
  an IAM mutation.
- user-assistant Pilot can only read authoritative history and commit the Bot
  final message for its configured tenant.
- iam-admin Pilot has a distinct ID and secret and is the only Pilot allowed to
  create approvals or call IAM mutation RPCs.
- A profiled service principal can access only an AI Session whose tenant,
  profile ID and profile version exactly match its Logic-owned mapping.
- Capability signing keys and Logic service-auth keys are distinct per Profile.

## Failure coverage

| Case | Evidence |
| --- | --- |
| user Pilot signs IAM Mutation | verifier rejects unlisted FullMethod |
| Pilot signs another tenant | verifier rejects unlisted tenant |
| user Pilot accesses iam-admin Session | `requireSessionTenant` returns `PermissionDenied` |
| same Profile, wrong version | `requireSessionTenant` returns `PermissionDenied` |
| unprofiled service accesses AI Session | fail closed |
| reused Gateway/Pilot/Profile ID or secret | Logic configuration validation fails |
| distributed replay | shared Redis nonce test in verification 08 |

## Reproducible commands

```bash
go test -race ./pkg/serviceauth ./logic ./logic/config ./logic/server ./logic/service -count=5
go vet ./pkg/serviceauth ./logic ./logic/config ./logic/server ./logic/service
docker compose --env-file .env -p resonance -f deploy/base.yaml -f deploy/services.yaml config -q
git diff --check
```

The targeted unit and race commands passed on 2026-08-09. Full repository and
Compose verification are repeated at the final release gate after the parallel
budget, approval UI and runtime-isolation slices settle.
