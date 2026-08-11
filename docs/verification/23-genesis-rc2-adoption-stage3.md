# Genesis RC2 adoption: Stage 3 final verification

## Boundary

The existing `docs/verification/evidence/genesis-v1.0.0-rc.2-stage3.json` is
the pre-release Genesis handoff. It binds a Resonance consumer that still used
Genesis RC1 and must remain unchanged. It proves compatibility input to the
RC2 release; it does not prove public RC2 adoption by Resonance.

The adoption proof uses a distinct path:

```text
artifacts/local-v1/rc2-adoption-final/<run-id>/
docs/verification/evidence/resonance-stage3-genesis-v1.0.0-rc.2.json
```

## Frozen identity

- Genesis version: `v1.0.0-rc.2`
- annotated tag object: `c759bb0b961bbdd685f5176520fea872084dbe17`
- source commit: `f78d7860849019ae5a35c6473420b5e7db2269a0`
- module sum: `h1:YtB2IJHqJ5ZucCDL7KfDPU3pyM9/yAotI0xUNlFEIaA=`
- go.mod sum: `h1:Uysrd3364pkU2OguYEWKyMVkgGvq3x/4dpC/QD8v8OA=`

`deploy/scripts/verify-genesis-rc2-identity.sh` rejects workspaces, module
replacements, a repository-local Genesis tree, and `vendor/`. Docker build
contexts also exclude all of those paths. All local commands run with
`GOWORK=off`.

## Final execution

The implementation PR is not itself final evidence. After it is merged and
all nine required checks succeed on the resulting `main` commit, synchronize a
clean local `main`, stop the `resonance-v1` project, and run:

```bash
make finalize-stage3-rc2
```

The command refuses a non-`main`, stale, dirty, or already-running input. It
then runs the Compose, business E2E, Agent negative-path, telemetry,
recovery, alert and benchmark matrix. It records the final Resonance SHA,
Genesis module identity, Hosted CI checks, Compose checksums, local image IDs,
environment data, raw evidence hashes and the evidence locator. It never
pushes an image or contacts a deployment host.

The generated adoption manifest becomes valid only when every check completed
on the same input set. A change to the Resonance SHA, Genesis identity,
Compose file, image identity, required Hosted CI result, bundle bytes, or
bundle availability invalidates the entire result and requires a new run ID.

### Two-commit closure model

The implementation commit `T` is the immutable tested SHA. It must already be
merged to `main` with the exact nine required checks before the matrix starts.
The finalizer rechecks `T`, its Git tree, the fully rendered Compose checksum,
the `.env` checksum, and the RC2 module identity after the long matrix and
before it emits a PASS manifest.

The generated bundle and manifest are then submitted as a separate
evidence-only commit `E`. `E` does not become a new tested application SHA:
its manifest keeps `tested_resonance_sha: T`. Before merging `E`, run
`deploy/scripts/verify-stage3-evidence-only-commit.sh T E`; it rejects every
change outside the one final bundle and adoption manifest and verifies every
bundle hash. Stage 3 closes only after `E` is merged and its commit SHA is
recorded in the handoff. Any runtime or build-input edit requires a new tested
SHA and a full new matrix, never an evidence-only update.

## Telemetry and recovery assertions

The RC2 closeout captures JSON logs after the E2E, not before it. It requires
JSON log records from Gateway, Logic, Task and Pilot; Loki trace IDs shared by
the IM and Agent service paths; and matching Tempo traces with parent/span
structure. The stored log and trace evidence is a field allowlist, so transport
headers, container environments and credentials cannot enter the bundle.

Recovery starts with durable facts, restarts each dependency without deleting
volumes, and proves PostgreSQL Outbox/Inbox growth; exact NATS stream/durable
names and effective configuration; non-regressing consumer and stream delivery
plus acknowledgement positions; and a post-restart IM probe with one durable
event for an idempotent client message, online delivery and Inbox recovery.
NATS `created` metadata is recorded separately and is not treated as durable
identity: RC2 can report a new value after an unconditional no-op consumer
update followed by a server restart. That compatibility defect is tracked in
[Genesis #67](https://github.com/ceyewan/genesis/issues/67) and must be fixed
before stable Genesis v1, but a `created` change alone does not prove consumer
replacement or a data-plane failure.

The same recovery run also proves the Redis sequencer and exact allocator
leases, exact etcd registrations plus a service-specific watch event, graceful
zero-exit shutdown, port release and lease removal. Each graceful stop must
also produce that service's exact `stage3.shutdown.flush` span in Tempo, bound
through its Loki log trace ID. Benchmark evidence fixes the seed, concurrency,
sample count and timeouts, and samples container and Prometheus resources for
the full workload window rather than after it.
