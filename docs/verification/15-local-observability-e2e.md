# Local observability and v1 end-to-end verification

Date: 2026-08-09. Compose project: `resonance-v1`.

> Historical RC1 evidence only. This record must not be used as proof that
> Resonance adopted the public Genesis RC2 module. The RC2 adoption closeout
> has a separate immutable bundle and manifest defined in
> `23-genesis-rc2-adoption-stage3.md`.

## Release binding

- Resonance base: `69f02a11319e2adb58b20d7671647f523c18b8b2`.
- Genesis: `github.com/ceyewan/genesis v1.0.0-rc.1`, resolved by Go modules with no `replace` directive.
- The final Resonance commit is recorded by the PR and replaces the base SHA as the deployable candidate.

## Reproducible commands

```bash
make up-observability
make verify-local-v1
make recovery-local-v1
make alerts-local-v1
make benchmark-local
make down-observability
```

`down-observability` preserves every named volume. Only Web, Gateway and Grafana bind host ports, all on `127.0.0.1`. The isolated defaults are Web `14173`, Gateway `18080`, and Grafana `13000`.

## Final evidence

- Cold-start Compose, health, IM E2E, deterministic Agent/Pilot contract E2E, Prometheus, Loki and Tempo: `artifacts/local-v1/final/`.
- Controlled firing and recovery evidence for ServiceDown, APIHighErrorRate, OutboxBacklog and TelemetryPipelineDown: `artifacts/local-v1/alerts-final/`. The API error-rate run injects real HTTP 404 responses while the same Gateway container remains running and healthy; its status snapshot is an explicit field allowlist and contains no environment variables.
- Machine-readable business and Agent contract baseline plus host/Docker/release metadata: `artifacts/local-v1/benchmark-20260809T092636Z/`.
- Full repository regression: `go test ./... -count=1` passed, including PostgreSQL/Testcontainers and the IM integration suite.

The final Prometheus snapshot has zero unhealthy targets, the Loki snapshot contains logs from the isolated project, and the Tempo snapshot contains 20 traces. Five dashboards and the Prometheus/Loki/Tempo data sources are provisioned from files. Loki derived fields and Tempo traces-to-logs are configured in both directions.

The curated API alert snapshot records an observed error-rate value of `0.9939759036144578` (99.40%) in `api-high-error-rate-firing.json`. At the same time, `api-high-error-rate-gateway-status.json` records the unchanged Gateway as `running` and `healthy` with zero restarts, and `api-high-error-rate-gateway-ready.json` records a successful readiness response. The matching recovered snapshot contains no firing API error-rate alert.

## Baseline snapshot

The fixed seed is `20260809`, concurrency is `1`, and the online message sample count is `20`:

| Path | P50 | P95 | P99 |
| --- | ---: | ---: | ---: |
| Online send to WebSocket push | 3.524 ms | 10.064 ms | 922.730 ms |
| Offline Inbox recovery | 515.179 ms | 515.179 ms | 515.179 ms |
| WebSocket connection | 1.650 ms | 1.650 ms | 1.650 ms |

Message delivery success was 100%. These values are a local baseline, not a production SLO. Agent Tool/Approval behavior is timed by the deterministic contract suite so the default gate performs no paid model call and consumes no provider credential.

## Failure and recovery evidence

- Fixed Compose `container_name` values initially conflicted with an existing project; the overlay resets them so project isolation is real.
- The project uses isolated host ports instead of assuming `8080/4173/3000` are free.
- Alloy uses the v1.8 Docker discovery `name`/`values` filter form.
- Core readiness remained available while Alloy, Loki and Tempo were paused, and all three accepted new telemetry after recovery.
- Logic, Task, Gateway, Pilot, NATS, Redis, etcd and PostgreSQL were restarted without deleting volumes and recovered.
- Generated Compose evidence redacts keys containing password, secret or API key material.
- Alert evidence is rejected before completion if its content contains a sensitive field name, credential assignment, private key, JWT, or common provider-token pattern. Timestamped diagnostic runs are ignored by Git; only the curated, scanned `alerts-final/` directory is versioned.
