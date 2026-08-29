# Conversation Audit Implementation Plan

- Date: 2026-08-29
- Branch: `feat/my-sub`
- Source specification: `docs/superpowers/specs/2026-08-29-conversation-audit-design.md`
- Delivery style: isolated vertical module, test-first at each integration boundary

## Delivery Invariants

1. Capture remains disabled by default and installs a no-op gateway recorder.
2. Audit failures, overload, panics, shutdown, and database errors never change
   client bytes, status, scheduling, billing, balances, usage, or Prompt Audit.
3. Plaintext payloads never enter settings, logs, metrics, SQL text, or Base64
   storage. Encryption keys come only from deployment configuration.
4. Request and response canonical JSON each stay within their configured hard cap.
5. Gateway admission is non-blocking; memory and queue exhaustion degrade audit only.
6. Existing migrations remain immutable. The feature is removable by deleting its
   isolated module, route/UI mounts, one migration, and small gateway hooks.

## Phase 1: Schema, Keyring, and Domain Contracts

### Files

- Add `backend/migrations/230zb_custom_conversation_audit.sql`.
- Extend `backend/internal/config/config.go` with deployment-owned keyring parsing.
- Add `backend/internal/conversationaudit/types.go`.
- Add focused schema/config/type tests beside the implementation.

### Work

1. Create the partitioned `conversation_audit_records` parent with composite primary
   key `(created_at, audit_id)`, state constraints, payload metadata, and no foreign
   keys to core tables.
2. Create compact `conversation_audit_delete_tombstones`, daily-partition helper
   function, current/next-two-day partitions, and per-partition indexes.
3. Add deployment configuration for active key ID and an ID-to-32-byte-key keyring.
   Reject duplicate IDs, invalid IDs, malformed Base64/hex material, and missing
   active keys. Do not auto-generate a key.
4. Define immutable config snapshots, record state/outcome/capture enums, canonical
   payload structs, gateway recorder/session interfaces, and no-op implementations.

### Tests

- Migration applies on PostgreSQL and verifies partition pruning/conflict key/indexes.
- Keyring accepts rotation and rejects invalid or absent active material.
- No-op recorder allocates no payload buffers and is nil-safe.

### Rollback Point

No runtime behavior is mounted. Revert the migration/config/types commit.

## Phase 2: Canonical Extraction and Payload Codec

### Files

- Add `backend/internal/conversationaudit/extractor_*.go` by client protocol.
- Add `backend/internal/conversationaudit/canonical.go`.
- Add `backend/internal/conversationaudit/codec.go`.
- Add unit tests and fuzz/property tests for truncation and codec boundaries.

### Work

1. Normalize OpenAI Chat/Responses, Claude, Gemini, search/media metadata, TTS/STT,
   embeddings, and realtime semantic events into versioned canonical payloads.
2. Omit media bytes, Base64 payloads, vectors, credentials, hidden reasoning, and raw
   SSE/WebSocket framing. Unsupported input produces an explicit degraded reason and
   never falls back to raw-body storage.
3. Implement cap-aware semantic truncation at valid UTF-8 boundaries, including a
   fixed metadata-only fallback that always fits.
4. Implement `canonical JSON -> Zstd fast -> AES-256-GCM`, per-side key IDs, bounded
   decompression, version rejection, and AAD binding to record identity and side.

### Tests

- Table-driven fixtures for each protocol/content block and omission rule.
- Near-cap, over-cap, invalid UTF-8, single-oversized-block, and minimal-cap tests.
- Round trip, tamper, wrong key, swapped side/record, unknown version, and
  decompression-bomb rejection tests.

## Phase 3: Resource Budget, Workers, Repository, and Retention

### Files

- Add `budget.go`, `worker_pool.go`, `repository.go`, `lease_manager.go`,
  `retention_manager.go`, and focused tests under
  `backend/internal/conversationaudit/`.

### Work

1. Add incremental atomic memory reservations and bounded pooled segments without
   maximum-size preallocation.
2. Add two default workers and a non-blocking 2048-job queue; jobs carry absolute
   two-minute deadlines and bounded retry/backoff.
3. Implement tombstone-aware request/response upserts with deterministic transaction
   advisory locks and immutable `(created_at, audit_id)` identity.
4. Implement keyset list/detail queries with partition date constraints, strict
   filters, three-second statement timeout, and independently decoded payload sides.
5. Implement one lease registry and a 30-second batched renewal loop with two-minute
   durable leases.
6. Create future partitions and expire stale captures/drop retained partitions under
   advisory and partition locks. Never drop a partition containing a live lease.
7. Implement single delete and preview/confirm deletion with fixed cutoff/high-water,
   compact tombstones, 5,000-row cap, and whole-closed-day DDL exception.

### Tests

- Atomic budget races, queue saturation, worker panic, retry deadline, and release.
- Out-of-order upserts and finalized-state monotonicity.
- Transaction-barrier race proving delete cannot be resurrected.
- Cross-partition pagination/filter bounds and deletion idempotency.
- Lease expiry/late owner finish and retention lock races.

## Phase 4: Configuration, Lifecycle, Metrics, and Dependency Injection

### Files

- Add `config.go`, `config_manager.go`, `service.go`, `metrics.go`, `module.go`.
- Mount providers in `backend/cmd/server/wire.go` and regenerate
  `backend/cmd/server/wire_gen.go`.
- Start/shutdown the service in `backend/cmd/server/main.go` and application cleanup.

### Work

1. Store versioned admin configuration under `conversation_audit_config` using a
   database advisory lock, CAS version, Redis invalidation, and periodic reload.
2. Validate all documented ranges and require PostgreSQL/keyring/partition readiness
   before enabling.
3. Implement `enabled -> disabling -> disabled`: immediate no-op admission, 30-second
   active-session grace, forced unknown/degraded detach, then bounded worker drain.
4. Reject re-enable or worker/queue shape changes while disabling; only allow pool
   shape changes while disabled and drained.
5. Expose bounded-label runtime counters and health without content or identity labels.

### Tests

- Defaults/range validation, CAS conflict, invalidation, stale reload, and readiness.
- Fake-clock enable/disable/grace/detach/drain lifecycle.
- Runtime counters and disabled hot-path allocation benchmark.

## Phase 5: Gateway Capture Boundaries

### Files

- Add a thin context/hook adapter in `backend/internal/server/middleware/`.
- Minimally update `api_key_auth.go` and `api_key_auth_google.go` after stable key
  resolution.
- Add response observation helpers near existing gateway handlers/services.
- Extend `backend/internal/handler/security_audit_helper.go` to share already-read
  request bodies without coupling Conversation Audit to Prompt Audit policy.
- Add a Conversation Audit route-coverage manifest test.

### Work

1. Begin metadata capture immediately after API key resolution and operations fallback
   identity installation; do not capture unknown credentials or pre-lookup rejects.
2. Attach metadata as it becomes known without changing middleware decisions.
3. Observe HTTP/SSE semantic client-visible output after conversion/rewriting. Never
   retain raw transport frames.
4. Finish exactly once for success, local/upstream error, timeout, cancellation,
   partial output, and recovered panic.
5. Classify every gateway route as captured model execution or explicit exclusion.

### Tests

- Disabled/enabled byte-for-byte status/header/body equality.
- Equal scheduling, billing, balance, usage, and Prompt Audit results.
- Failure matrix for audit panic, queue/budget/database/partition/codec failures.
- Manifest fails when a route is unclassified.

## Phase 6: Responses WebSocket, Live, and Realtime

### Files

- Minimally update `backend/internal/handler/openai_gateway_handler.go`.
- Minimally update `backend/internal/handler/openai_live.go`.
- Minimally update `backend/internal/handler/grok_audio.go`.
- Add semantic turn aggregators inside `backend/internal/conversationaudit/`.

### Work

1. Create one record per `response.create` turn for Responses WebSocket V2.
2. Create one record per model response turn for Live/realtime sessions, correlated by
   validated existing IDs.
3. Capture only text, transcripts, tool events, readable options, IDs/status/URLs,
   and textual errors; omit audio/video/SDP/transport data.
4. Detach audit after disable grace while allowing the client session to continue.

### Tests

- Duplicate/out-of-order event aggregation and concurrent turns.
- Disconnect/timeout/partial output and disabled-during-stream behavior.
- Exact WebSocket event equality with and without audit.

## Phase 7: Admin API and Operation Auditing

### Files

- Add `backend/internal/handler/conversation_audit_handler.go` and tests.
- Mount routes in `backend/internal/server/routes/admin.go`.
- Register providers in `backend/internal/handler/wire.go` and handler aggregate.

### Work

1. Add config/runtime/list/detail/single-delete/preview/confirm endpoints under
   `/api/v1/admin/conversation-audit` with existing admin and compliance guards.
2. Enforce 31-day general and 24-hour protocol/endpoint/model windows, maximum 100
   list rows, stable errors, no-cache detail responses, and bounded request deadlines.
3. Audit sensitive detail reads, all deletes, and configuration changes using the
   existing admin operation audit mechanism.

### Tests

- Authentication/compliance/operation-audit coverage.
- Cursor/filter/deadline/error contracts and payload-side availability.
- Delete eligibility, confirmation binding, retry, cap, and partition-drop behavior.

## Phase 8: Admin Frontend and Localization

### Files

- Add `frontend/src/features/conversation-audit/` API, types, view model, view,
  components, and tests.
- Add Chinese/English `admin/conversationAudit.ts` locale modules and exports.
- Mount `/admin/conversation-audit` in `frontend/src/router/index.ts`.
- Update `frontend/src/components/layout/AppSidebar.vue` so the Security Audit parent
  remains visible independently of the Risk Control feature flag.

### Work

1. Build Records and Configuration/Runtime tabs using existing tables, dialogs,
   pagination, notifications, controls, and permission conventions.
2. Load decrypted payload only on explicit detail action and render a stable,
   collapsible role transcript.
3. Handle loading, empty, error, unauthorized, narrow screen, stale config, and
   duplicate-submit states.
4. Keep content moderation/Prompt Audit children under their existing feature gate;
   Conversation Audit visibility depends only on administrator access.

### Tests

- API/cursor, filters/list, detail payload states, CAS conflict, delete flows,
  navigation permission, and Chinese/English integration.

## Phase 9: Full Validation and Load Report

Run from the repository root or documented subdirectory:

```powershell
gofmt -w <changed-go-files>
go test ./...
go test -tags embed ./internal/web
pnpm --dir frontend run typecheck
pnpm --dir frontend run test:run
pnpm --dir frontend run build
git diff --check
git status --short
```

Add a repeatable load harness/report for the approved 1,000-connection profile. Record
raw disabled/enabled latency, CPU, RSS, goroutines, queue depth, audit degradation,
client errors, and PostgreSQL query behavior. Delivery is blocked if auditing changes
client behavior, exceeds the global memory budget, increases P95 latency over 5%, or
increases process CPU by more than 10 percentage points in the same environment.

## Commit Sequence

1. `docs(custom): plan conversation audit implementation`
2. `feat(custom): add conversation audit storage foundation`
3. `feat(custom): add conversation audit codec and workers`
4. `feat(custom): add conversation audit repository and lifecycle`
5. `feat(custom): capture gateway conversations`
6. `feat(custom): capture realtime conversation turns`
7. `feat(custom): add conversation audit admin api`
8. `feat(custom): add conversation audit admin page`
9. `test(custom): validate conversation audit isolation and load`

Each implementation commit must compile and pass its focused tests. No commit may
contain generated frontend output, local secrets, data directories, or unrelated
working-tree changes.
