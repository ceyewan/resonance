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
replacements and repository-local Genesis source. All local commands run with
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

## Telemetry and recovery assertions

The RC2 closeout captures JSON logs after the E2E, not before it. It requires
JSON log records from Gateway, Logic, Task and Pilot; Loki trace IDs shared by
the IM and Agent service paths; and matching Tempo traces with parent/span
structure. The stored log and trace evidence is a field allowlist, so transport
headers, container environments and credentials cannot enter the bundle.

Recovery starts with durable facts, restarts each dependency without deleting
volumes, and proves PostgreSQL Outbox/Inbox growth, NATS JetStream consumer
position continuity, Redis allocator keys, etcd registry recovery, graceful
zero-exit shutdown, port release and lease removal. Benchmark evidence fixes
the seed, concurrency, sample count and timeouts and binds results to the host,
Compose inputs and image IDs.
