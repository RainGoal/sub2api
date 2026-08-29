# Conversation Audit Design

- Status: Approved for implementation planning
- Date: 2026-08-29
- Target branch: `feat/my-sub`
- Scope: Sub2API backend gateway and embedded admin frontend

## Summary

Add an optional, independently removable conversation audit module that stores the
readable content of authenticated model requests and the final client-visible
responses. The module records successful, failed, timed-out, cancelled, partial,
unknown-outcome, and degraded captures without changing gateway responses, billing,
scheduling, or the existing security audit systems.

The deployment serves more than 100,000 requests per day and may have roughly
1,000 concurrent streaming connections. To bound database and runtime costs, audit
payloads are normalized, capped, compressed with Zstd, encrypted with AES-256-GCM,
stored as binary PostgreSQL values, and retained in daily partitions for seven days
by default.

## Goals

1. Record readable request and response conversations for every classified model
   route after an API key credential resolves to a stable identity, including later
   key-state, IP, user, group, subscription, quota, balance, validation, local, and
   upstream failures.
2. Preserve the response as it was visible to the client after protocol conversion
   and response rewriting.
3. Support OpenAI, Claude, Gemini, SSE, and Responses WebSocket traffic without
   storing protocol framing or binary media.
4. Allow administrators to filter by time, session, request, user, group, API key,
   status, protocol, endpoint, and model.
5. Keep the feature disabled by default and fault-isolated from gateway, billing,
   account scheduling, usage records, and existing security audit behavior.
6. Bound CPU, memory, and database growth under approximately 1,000 concurrent
   requests.
7. Make disabling and later removing the feature straightforward.

## Non-Goals

- Do not search request or response body content.
- Do not store Authorization headers, cookies, upstream credentials, raw SSE frames,
  uploaded media, generated media bytes, Base64 bodies, or embedding vectors.
- Do not replace or merge the existing content moderation, prompt audit, operations
  error logs, or usage records.
- Do not use audit persistence as part of a billing, authorization, or gateway
  decision.
- Do not guarantee payload completeness during resource exhaustion or infrastructure
  failure. Gateway availability takes priority and degraded records must be explicit.
- Do not add user-facing access to conversations in the first version.

## Alternatives Considered

### Extend the existing prompt audit

Rejected. Prompt audit is a Qwen3Guard security-event system with different enablement,
queue, persistence, and retention semantics. Storing every conversation in its event
tables would blur security findings and operational conversation records.

### PostgreSQL metadata and external object storage payloads

Deferred. This minimizes PostgreSQL size but creates hundreds of thousands of small
objects, cross-store deletion consistency, upload compensation, and an additional
runtime dependency. It remains a future option if measured PostgreSQL growth is still
unacceptable.

### Independent PostgreSQL module

Selected. It introduces no new infrastructure, uses existing PostgreSQL, Zstd, AES,
admin authentication, and audit patterns, and can be isolated behind a no-op recorder.

## Architecture

The backend implementation is a vertical module under
`backend/internal/conversationaudit/`:

- `CaptureService`: creates capture sessions, reads the in-memory config snapshot,
  reserves global memory, and determines degraded behavior.
- `RequestExtractor`: converts supported inbound protocols to the canonical readable
  request structure.
- `ResponseObserver`: accepts client-visible semantic response deltas and final state.
- `PayloadCodec`: versioned JSON serialization, Zstd compression, and AES-256-GCM
  encryption/decryption.
- `WorkerPool`: a bounded set of workers for encoding and PostgreSQL writes. There is
  no audit goroutine or Zstd encoder per client connection.
- `LeaseManager`: owns one central active-session registry and renews durable capture
  leases in batches; there is no heartbeat goroutine per connection.
- `Repository`: raw SQL access for the partitioned audit table, keyset listing,
  details, and safe deletion.
- `RetentionManager`: creates future partitions, marks abandoned captures, and drops
  expired partitions under a PostgreSQL advisory lock.
- `AdminHandler`: configuration, runtime, records, detail, and deletion endpoints.

The frontend implementation is isolated under
`frontend/src/features/conversation-audit/` and is mounted through one route and one
navigation entry.

### Gateway boundary

Gateway code depends only on a thin optional recorder interface. A no-op implementation
is installed when the feature is disabled or unavailable. The disabled path must not
allocate audit buffers, enqueue work, or query audit tables.

Conceptually:

```go
type Recorder interface {
	Begin(ctx context.Context, input BeginInput) Session
}

type Session interface {
	Observe(event ResponseEvent)
	Finish(result FinishResult)
}
```

The final signatures must follow the repository's existing context and handler types,
but the boundary must retain these ownership rules:

- `Begin` never changes request admission.
- `Observe` never changes bytes sent to the client.
- `Finish` never changes status, billing, or usage persistence.
- A no-op session is valid and nil-safe.

## Data Flow

1. A route-coverage manifest classifies the request as a model execution, streaming
   continuation, or explicit exclusion.
2. In both standard and Google API-key middleware, immediately after `GetByKey`
   succeeds and the resolved identity is stored for operations fallback, a shared
   hook starts a metadata-only audit session. It runs before key-state, IP, user,
   group, subscription, quota, balance, composite target, semantic body validation,
   account selection, billing, or upstream checks.
3. Downstream middleware and handlers attach group, account, effective model, and
   other metadata when those values become available. Failure before any of them is
   known still produces a valid record with nullable fields.
4. The request extractor creates a capped canonical request payload. A worker
   compresses, encrypts, and upserts the request side of the record.
5. Existing security checks, account selection, billing, and forwarding continue
   unchanged.
6. The response observer receives semantic data at the final client-facing protocol
   boundary, after upstream-to-client conversion and response rewriting.
7. A finish-once boundary observes every downstream return, abort, timeout,
   cancellation, and recovered panic. `Finish` submits the response payload and
   terminal metadata to the worker pool without replacing the original error.
8. Workers upsert by application-generated audit ID and immutable `created_at`, so
   request and response writes are safe if they complete out of order.
9. The central lease manager registers active `(created_at, audit_id)` pairs, renews
   them every 30 seconds in bounded batches, and extends `lease_expires_at` to two
   minutes in the future. Initial and terminal writes also carry the owner instance
   ID and current lease state.
10. Under an advisory lock, the retention manager finalizes only `capturing` rows
   whose durable lease expired. A later valid terminal job from the same owner may
   refine the unknown outcome only before the row's `mutable_until`; no update may
   return a finalized row to `capturing`.

Missing/unknown API keys, lookup overload/failure, health checks, admin APIs, and
requests rejected before stable API key identity is established do not create
conversation records. This explicitly includes the current pre-lookup body-size
rejection path, even when the request supplied a key that was never validated.
Known but disabled or otherwise unauthorized keys are recorded. Existing operations
logging continues to handle all of those requests.

## Canonical Payloads

Request and response are stored separately so the request can be released before a
long streaming response finishes.

### Request payload

```json
{
  "version": 1,
  "messages": [
    {
      "role": "system|developer|user|assistant|tool",
      "content": [
        { "type": "text", "text": "..." },
        { "type": "tool_call", "name": "...", "arguments": "..." },
        { "type": "tool_result", "name": "...", "content": "..." },
        { "type": "media_omitted", "media_type": "...", "encoded_bytes": 0 }
      ]
    }
  ],
  "omitted_messages": 0,
  "truncated": false
}
```

### Response payload

```json
{
  "version": 1,
  "messages": [
    {
      "role": "assistant|tool",
      "content": [
        { "type": "text", "text": "..." },
        { "type": "reasoning_summary", "text": "..." },
        { "type": "tool_call", "name": "...", "arguments": "..." },
        { "type": "resource", "resource_type": "...", "id": "...", "url": "..." }
      ]
    }
  ],
  "error": { "code": "", "message": "" },
  "truncated": false
}
```

Only reasoning content actually sent to the client is eligible. Hidden model
chain-of-thought is neither requested nor persisted.

### Size limits

- Request canonical JSON: 1 MiB by default.
- Response canonical JSON: 1 MiB by default.
- Both limits are administrator-configurable with server-side bounds.
- Request truncation keeps system/developer content and the newest complete message
  blocks, recording omitted message and byte counts.
- Response truncation keeps the beginning and end at valid semantic block boundaries
  and inserts an explicit truncation marker.
- Preservation priority never overrides the byte cap. If one text, reasoning, tool
  argument, or tool result block cannot fit, it is shortened at a valid UTF-8
  boundary and records omitted bytes. If even the shortened block cannot fit, it is
  replaced by a fixed-size `content_omitted` item containing only type and byte count.
- Serialization reserves space for the version, truncation flags, and counters before
  appending content, then verifies the final JSON length. A fixed minimal
  metadata-only payload is the last fallback, so stored canonical JSON never exceeds
  its configured cap.
- No payload buffer preallocates the configured maximum.

## Protocol Coverage

The first version must cover all current client-facing model paths through reusable
protocol adapters rather than account/provider-specific copies.

| Protocol or endpoint | Request capture | Response capture |
| --- | --- | --- |
| OpenAI Chat Completions | roles, text, tools | text, tools, errors |
| OpenAI Responses HTTP/SSE | instructions, input items, tools | output items, text, tools, errors |
| Responses WebSocket V2 | one record per `response.create` turn | client-visible events aggregated per turn |
| Claude Messages | system, roles, text, tools | text deltas, tool use, errors |
| Gemini generate/stream | system instruction, contents, parts | text, function calls, errors |
| Cross-protocol forwarding | original client protocol | final client protocol after conversion |
| Image/video generation | text prompt, readable options | task ID, status, URLs; no media bytes |
| TTS | input text and readable options | metadata only; no audio |
| STT | media omission metadata | transcript text and errors |
| Embeddings | readable input text | count and dimensions; no vectors |
| Alpha/web/X search | query and readable options | text, result titles/URLs, errors |
| Live/realtime sessions | text, transcript, and tool events; media omitted | one record per model response turn |

SSE frame names, keepalives, usage-only frames, and transport framing are not stored.
Unsupported payloads become metadata-only records with reason
`unsupported_payload`; raw bodies must never be used as a fallback.

### Required route classification manifest

The implementation must add a route-coverage test based on the existing prompt-audit
manifest. It scans every registered gateway method/path and requires each current or
future route to be classified. Aliases under `/v1`, bare root, Codex direct,
Antigravity, and Gemini prefixes share the same classification.

Model execution routes captured in the first version are:

- `POST /messages`, `/responses`, `/responses/*subpath`, `/chat/completions`,
  `/embeddings`, and `/alpha/search`;
- `POST /images/generations`, `/images/edits`, their asynchronous submit aliases,
  and `/images/batches`;
- `POST /videos`, `/videos/generations`, `/videos/edits`, and
  `/videos/extensions`;
- `POST /tts`, `/stt`, `/web_search`, and `/x_search`;
- `POST /models/*modelAction` for Gemini generate/stream actions;
- `POST /live` and `/realtime/calls` for live-session setup; and
- `GET /responses`, `/realtime`, `/live/:call_id`, and the Codex direct
  `GET /:call_id` only when they upgrade to a model streaming session.

Responses and realtime WebSockets create one audit record per client
`response.create` turn. Live sideband sessions use the call ID as correlation and
create one record per model response turn; setup handshakes record only readable
options, IDs, status, and textual errors. Audio, video, SDP, and other transport data
are always omitted.

Explicit exclusions are token-count, model-list, usage/billing, asynchronous
task-status/content/download, cancel/delete, and custom-voice management routes.
They do not execute a new model request. The manifest stores a non-empty reason for
every exclusion and fails tests when a route is added, removed, or changes method
without a corresponding decision.

Session correlation uses only the repository's existing validated explicit session
header behavior. Session IDs are not inferred from prompt content, cache keys, or
other heuristics.

## Storage Model

Create a new PostgreSQL table `conversation_audit_records` partitioned by UTC
`created_at` day. It has no foreign keys and adds no columns to core tables.
A compact `conversation_audit_delete_tombstones` table contains only
`(created_at, audit_id, deleted_at)` for manually deleted rows and has a composite
primary key. It has no payload or foreign key and prevents any late worker from
recreating a deleted record.

### Metadata columns

- `audit_id UUID` generated by the application; `(created_at, audit_id)` is the
  partition-compatible primary key.
- `created_at`, `completed_at`, `updated_at`, `mutable_until`, `owner_instance_id`,
  and `lease_expires_at`.
- `request_id` and nullable `session_id`.
- User, API key, group, and account IDs plus short display-name snapshots.
- Protocol, inbound endpoint, requested model, effective model, and stream/WS mode.
- HTTP status, stable error code, `record_state`, `outcome_status`, and
  `capture_status`.
- Request/response original, stored, compressed, and encrypted byte counts.
- Request/response truncation flags and omitted counts.
- Per-side payload codec version and encryption key ID, plus an optional stable
  degraded reason.
- `request_payload BYTEA` and `response_payload BYTEA`.

`record_state` is `capturing` or `finalized`. Finalized `outcome_status` values are:

- `completed`: a complete successful client response.
- `error`: a local or upstream error response.
- `timeout`: timeout before a complete response.
- `partial`: some client-visible output was sent before disconnection or failure.
- `cancelled`: the client or request context cancelled before any output was sent.
- `unknown`: the owner lease expired or capture detached before the client outcome
  could be observed.

`capture_status` independently describes audit fidelity:

- `complete`: all eligible semantic content was stored;
- `truncated`: content reached an administrator-configured cap;
- `metadata_only`: the route intentionally has no eligible readable payload; and
- `degraded`: resource exhaustion, unsupported content, or an audit failure caused
  partial or unavailable payloads.

An owner-lease expiry finalizes as `outcome_status=unknown`,
`capture_status=degraded`, and reason `owner_lease_expired`; it never invents a
client failure. This separation preserves a successful client outcome when capture
is degraded.
Request and response jobs use exactly
`ON CONFLICT (created_at, audit_id) DO UPDATE`. Before checking tombstones or writing,
every worker transaction acquires the same transaction-scoped PostgreSQL advisory
lock derived deterministically from `(created_at, audit_id)`. A row deletion acquires
that lock before revalidating eligibility, inserting its tombstone, and deleting the
row in one transaction. Therefore an upsert either completes before deletion or sees
the committed tombstone afterward; it cannot pass the check and recreate the row.
Hash collisions may serialize unrelated IDs but cannot weaken correctness. Batch
deletion acquires record locks in `(created_at, audit_id)` order to avoid deadlocks.

Both insert and update execute only when no matching delete tombstone exists.
`created_at` is immutable and carried by every job. Updates may only advance known
fields and must not move a finalized row back to `capturing`. Normal and lease-expiry
finalization set `mutable_until = completed_at + 2 minutes`; a finalized-row update
requires the same owner and transaction time at or before `mutable_until`. Workers
drop later updates before touching payload columns and increment a bounded metric.

### Payload encoding

The exact order is mandatory:

```text
versioned canonical JSON -> Zstd fast compression -> AES-256-GCM -> BYTEA
```

Each request and response envelope stores its own codec version, bounded key ID, and
raw nonce/ciphertext/tag bytes. It is not Base64 encoded. The implementation may adapt
the existing AES-GCM primitive, but it does not use the current string/Base64
`SecretEncryptor` API.

Payload keys are a deployment-owned persistent keyring, not administrator settings
and not an auto-generated per-process TOTP key. Every instance must receive the same
key IDs and 32-byte keys through secret-backed configuration. One key ID is active
for new writes; retained keys remain decrypt-only. Rotation adds a new key and makes
it active without rewriting old rows. Request and response jobs select their active
key independently, persist `request_key_id`/`response_key_id`, and may use different
keys across a rotation. An old key may be removed only after neither key-ID column in
any retained partition references it; otherwise detail reads report payload
unavailable.
Enabling fails when the active key is absent, invalid, duplicated, or unavailable.
Keys and plaintext are never persisted in ordinary settings or logs. AES-GCM
additional authenticated data binds ciphertext to `audit_id`, `created_at`, payload
side, codec version, and key ID so payloads cannot be swapped between rows or sides.

Decompression is attempted only after successful authenticated decryption, and
decoded size is bounded to prevent decompression bombs.

### Partitioning

- One range partition per UTC calendar day.
- On startup and periodically, create today's and at least the next two days'
  partitions under an advisory lock.
- Default retention is seven days and is administrator-configurable.
- Whole expired days are removed by dropping partitions, not row-by-row deletion.
- Retention first obtains an access-exclusive lock on the candidate child partition
  inside its transaction, finalizes rows whose owner leases already expired, and then
  checks again for `record_state=capturing` with `lease_expires_at` at or after the
  transaction timestamp. If any live lease remains, the partition is skipped and a
  bounded `retention_blocked_live_capture` metric is emitted. The same transaction
  drops the partition only when no live capture remains, closing the heartbeat/write
  race. Long-running streams may therefore retain an old partition beyond the normal
  retention boundary until they finish or their leases expire.
- Missing-partition writes degrade audit only and never fail gateway traffic.
- No default partition silently accumulates unbounded data.
- Request and response jobs have an absolute two-minute persistence deadline. Once
  expired they are discarded and cannot write or retry later.
- Tombstones are removed only after the corresponding daily data partition no longer
  exists, so their lifetime covers every possible late capture write.

### Indexes

Each partition receives only the indexes required by approved filters:

- `(created_at DESC, audit_id DESC)`
- `(session_id, created_at DESC)` where session ID is not null
- `(request_id)`
- `(user_id, created_at DESC)`
- `(api_key_id, created_at DESC)`
- `(group_id, created_at DESC)`
- `(outcome_status, created_at DESC)`
- `(capture_status, created_at DESC)`

Payload columns are never indexed and list queries must not select them.

## Configuration

Store a versioned JSON configuration in the existing settings system under
`conversation_audit_config`. The server owns defaults and validation.

| Field | Default | Purpose |
| --- | --- | --- |
| `enabled` | `false` | Global capture switch |
| `retention_days` | `7` | Daily partition retention |
| `request_max_bytes` | `1048576` | Canonical request cap |
| `response_max_bytes` | `1048576` | Canonical response cap |
| `memory_budget_bytes` | `268435456` | Per-instance uncompressed audit buffer budget |
| `worker_count` | `2` | Compression/encryption/persistence workers |
| `queue_capacity` | `2048` | Bounded payload work queue |

Compression mode is intentionally fixed to a fast Zstd setting in the first version;
it is not an administrator tuning surface. Configuration updates use the existing
multi-instance-safe settings update pattern and install an immutable in-memory
snapshot. Server validation enforces retention `1..365` days, payload caps
`4096..4194304` bytes, memory budget `67108864..2147483648` bytes, worker count
`1..8`, and queue capacity `128..8192`. Enabling is rejected unless encryption,
PostgreSQL, and current/future partitions are ready.

Enabled, retention, and payload-cap changes apply through an atomic config snapshot.
A lower memory budget immediately blocks new reservations until usage falls below the
new limit. Worker-count and queue-capacity changes are accepted only while capture is
disabled and the old queue is drained; this avoids live pool replacement semantics.

The effective lifecycle is `enabled`, `disabling`, or `disabled`. A successful update
to `enabled=false` installs the no-op recorder before the admin request returns, so
new gateway requests stop capturing immediately. Existing sessions receive a fixed
30-second grace period to finish normally. At the deadline, the capture service
atomically detaches every remaining session, stops accepting its events, releases its
payload buffers, and queues terminal metadata with `outcome_status=unknown`,
`capture_status=degraded`, and reason `disabled_during_capture`; the client stream
continues unchanged without auditing.

The lease manager renews sessions through the grace period and unregisters them when
they finish or detach. Workers then drain already accepted jobs only until each job's
existing two-minute absolute deadline, after which remaining jobs are discarded and
resources released. Runtime becomes `disabled` when the queue and active registry are
empty. Re-enabling or changing worker/queue shape while `disabling` returns a config
conflict; retention cleanup continues in every lifecycle state.

## Resource Isolation and Overload

- Memory is reserved incrementally against a per-instance atomic global budget.
- Buffers grow in pooled bounded segments and never preallocate 1 MiB per connection.
- Queue jobs and active buffers both count against the global budget until ownership
  is released.
- Workers and encoders are pooled and fixed in number.
- No audit-specific goroutine, encoder, or database transaction is kept per client
  connection.
- Audit work does not share billing or usage transactions.
- Queue admission is non-blocking on the gateway path.

When the memory budget or payload queue is exhausted, gateway traffic wins. The
session stops accepting additional payload bytes, records the available data where
possible, and finalizes with `capture_status=degraded` while preserving the client
`outcome_status`. A small reserved metadata lane may be used to
preserve degraded metadata, but its own exhaustion must also remain non-blocking.

## Failure Handling

| Failure | Required behavior |
| --- | --- |
| Feature disabled | gateway request path performs no buffer allocation, queue admission, or audit DB query; bounded drain and retention work may continue in background |
| Extractor rejects valid but unsupported content | metadata-only `degraded` record |
| Queue or memory full | partial/metadata `degraded` record; gateway unchanged |
| Compression or encryption failure | no plaintext fallback; metric and degraded metadata |
| Database temporarily unavailable | bounded retry with backoff while within queue budget |
| Database outage persists | release payload, increment loss/degraded metrics, gateway unchanged |
| Partition missing | audit write failure/degradation; trigger partition health alert |
| Client disconnect after output | preserve collected output and mark `partial` |
| Client/context cancellation before output | mark `cancelled`; after output mark `partial` |
| Timeout before complete response | mark `timeout`, or `partial` if output was sent |
| Process crash or expired owner lease | finalize as outcome `unknown`, capture `degraded`; a later valid owner finish may refine it before the write deadline |
| Payload decryption failure in admin detail | return stable admin error without logging payload |
| Module panic | recover at audit boundary; original response and billing remain unchanged |

Logs and errors may contain audit ID, request ID, sizes, status, latency, and stable
reason codes. They must not contain plaintext conversation content, ciphertext,
authentication material, or decryption keys.

## Admin API

All routes are under `/api/v1/admin/conversation-audit`, require existing admin
authentication and compliance guards, and participate in existing admin operation
auditing. Sensitive detail reads and all deletes are audited.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/config` | Read effective configuration |
| `PUT` | `/config` | CAS-style configuration update |
| `GET` | `/runtime` | Read queue, memory, partition, and error health |
| `GET` | `/records` | Keyset-paginated metadata list |
| `GET` | `/records/:date/:id` | Read and decrypt one record detail |
| `DELETE` | `/records/:date/:id` | Delete one record |
| `POST` | `/delete-preview` | Preview a bounded filtered deletion |
| `POST` | `/delete-by-filter` | Confirm a previewed filtered deletion |

### List filters

- Required UTC time window, defaulting in the UI to 24 hours and never exceeding 31
  days in one request.
- User ID, group ID, API key ID.
- Session ID and request ID exact match.
- Outcome status, capture status, protocol, inbound endpoint, and requested model.
- Cursor and limit, with a server maximum of 100 rows.

The list API returns `items` and `next_cursor`; it does not return an exact total by
default. The cursor is opaque, authenticated or strictly validated, and represents
`created_at + audit_id`.

To minimize index storage, protocol, endpoint, and model do not receive dedicated
indexes. Any query using one of them is limited to a 24-hour window and scans the
`created_at` index in keyset order after partition pruning. List and detail statements
use a three-second PostgreSQL statement timeout; a timeout returns a stable admin
error and never falls back to an unbounded query.

### Detail behavior

The detail route includes the UTC partition date so PostgreSQL can prune partitions.
It returns metadata even if one payload is unavailable and reports request and
response payload status independently. Decrypted content is never cached by shared
HTTP caches.

### Deletion behavior

- Single deletion uses partition date and audit ID. Only finalized records with
  `completed_at` at least five minutes old are eligible; this is longer than the
  normal asynchronous write deadline and avoids deleting an actively settling row.
  In the same transaction, deletion inserts a tombstone before removing the row;
  every later upsert checks that tombstone, which is the authoritative no-resurrection
  guarantee.
- Filter deletion requires an explicit valid time range no longer than 31 days. The
  24-hour rule also applies when filtering by protocol, endpoint, or model.
- Preview fixes `eligibility_cutoff = preview_started_at - 5 minutes` and returns the
  matched count, normalized filters, high-water mark, cutoff, operation type, and a
  short-lived confirmation token bound to the current administrator. Preview and
  confirmation both require `record_state=finalized` and
  `completed_at <= eligibility_cutoff`; records cannot age into the approved set.
- Preview uses a five-second request deadline and three-second PostgreSQL statement
  timeout. Timeout returns no confirmation token.
- Row confirmation deletes only rows at or below the preview high-water mark and at
  or before the fixed eligibility cutoff. Finalized metadata is immutable after the
  two-minute write deadline and five-minute cutoff, so concurrent activity can only
  remove rows from this set, never add new eligible rows.
- Preview counts stop at 5,001 and return `count=5000, has_more=true` when capped.
  One confirmation deletes at most 5,000 eligible rows in batches of 500 inside one
  transaction with a ten-second end-to-end request deadline. Any batch error or
  timeout rolls back the entire transaction, so partial success is never returned.
  Each batch inserts its tombstones before deleting rows. Larger sets require another
  preview/confirmation.
- A confirmation token remains retryable until expiry and always carries the same
  normalized filters, high-water mark, and eligibility cutoff. Retrying after an
  unknown commit is idempotent: already-deleted rows stay absent and the response
  reports the remaining deleted count, which may be zero.
- A whole-day filter becomes a separate `drop_partition` operation only when it
  covers one complete closed UTC partition, has no additional row filter, all rows
  satisfy the fixed cutoff, and no live capture lease remains. Preview performs a
  bounded exact count and returns no token on timeout. Confirmation revalidates those
  conditions under the partition access-exclusive lock, then drops the partition
  transactionally. Because this is constant-size DDL rather than row deletion, it is
  an explicit exception to the 5,000-row confirmation limit. The current or future
  partition is never eligible.
- Capturing, recently finalized, or otherwise ineligible records return
  `conversation_audit_record_busy`; workers discard every expired job before any
  database call.

Stable error codes include:

- `conversation_audit_invalid_config`
- `conversation_audit_config_conflict`
- `conversation_audit_encryption_unavailable`
- `conversation_audit_record_not_found`
- `conversation_audit_payload_unavailable`
- `conversation_audit_payload_decode_failed`
- `conversation_audit_invalid_cursor`
- `conversation_audit_query_limit_exceeded`
- `conversation_audit_delete_time_range_required`
- `conversation_audit_delete_confirmation_invalid`
- `conversation_audit_record_busy`

## Admin Frontend

Add `/admin/conversation-audit` under the existing Security Audit navigation group.
The page has two tabs:

1. `Records`: metadata filters, keyset pagination, record status, sizes, compression
   ratio, and detail/delete actions.
2. `Configuration and Runtime`: global switch, retention, caps, memory budget,
   workers, queue capacity, and runtime health.

The detail dialog loads payloads only after explicit admin action and renders an
unframed role-oriented transcript. Large messages remain collapsible and text must
not resize the dialog layout. The page must handle loading, empty, error,
unauthorized, narrow-screen, stale configuration, and duplicate-submit states.

All user-visible text is provided in Chinese and English. Existing layout, table,
modal, notification, permission, and icon components are reused.

## Observability

Runtime and metrics must expose, at minimum:

- feature enabled/effective status and configuration version;
- active capture count;
- current buffered bytes and configured budget;
- payload and reserved metadata queue depth/capacity;
- worker active count and processing latency;
- completed, error, timeout, cancelled, partial, and unknown outcome counts, plus
  complete, truncated, metadata-only, and degraded capture counts;
- queue-full, budget-full, extractor-unsupported, encode-failed, and write-failed
  counts;
- request/response original, stored, compressed, and encrypted bytes;
- compression ratio and payload processing latency;
- PostgreSQL and current/future partition health;
- last stable error code and timestamp.

Metrics must use bounded labels and never use request ID, session ID, user ID, model
names, or content as metric labels.

## Performance Acceptance

The implementation is not accepted until representative before/after measurements
show:

1. With auditing disabled and no pre-existing drain work, gateway requests perform no
   audit DB queries, queue admission, or payload buffer allocations. Background
   retention is measured separately and is excluded from this hot-path assertion.
2. Approximately 1,000 concurrent streaming connections complete without audit-caused
   disconnects, response mutation, goroutine leaks, or unbounded memory growth.
3. Audit-owned uncompressed buffering remains within the configured global budget,
   allowing only documented bounded worker/codec overhead.
4. Under representative enabled load, P95 request latency increases by no more than
   5 percent and process CPU by no more than 10 percentage points compared with the
   same build and traffic with auditing disabled.
5. Queue saturation and database outage do not change client-visible status, bytes,
   billing, or usage records.
6. Listing and detail queries remain bounded across at least seven daily partitions
   containing at least 100,000 records per day.

If the hardware or test environment makes percentage results noisy, the test report
must include repeated runs and raw measurements rather than weakening the gate without
explicit approval.

The repeatable minimum load profile is a 15-minute warm-up followed by a 30-minute
measurement, 1,000 concurrent connections, at least 80 percent streaming traffic,
and a documented mix containing OpenAI Chat, Responses HTTP/WebSocket, Claude, and
Gemini. It includes typical 8 KiB request/32 KiB response payloads, one percent
near-cap payloads, a 60-second two-times arrival burst, and PostgreSQL preloaded with
seven days at 100,000 records per day. Reports include hardware, protocol mix,
payload distribution, stream duration, arrival rate, CPU, RSS, goroutines, queue
depth, P50/P95/P99 latency, error rate, and raw repeated-run results.

## Testing Strategy

### Unit tests

- Canonical extraction for every supported protocol and content block.
- SSE and WebSocket incremental aggregation, duplicate events, and partial frames.
- Truncation at UTF-8 and semantic block boundaries.
- Binary/Base64/vector omission.
- Codec round trips, tamper rejection, wrong key, version rejection, and bounded
  decompression.
- Atomic memory accounting, queue saturation, and no-op allocation behavior.
- Record-state, outcome, and capture-status transitions plus out-of-order
  request/response upserts.
- Cursor and filter validation.

### Integration tests

- Migration schema, composite primary key/conflict target, daily partition indexes,
  partition creation, and closed-partition drop protection.
- Repository filtering and keyset pagination across partition boundaries.
- Configuration CAS and multi-instance invalidation.
- Detail decrypt/decompress and independent payload availability.
- Safe single and preview/confirm filtered deletion, write-deadline expiry, and a
  transaction-barrier race test proving concurrent upsert/tombstone deletion is
  serialized and cannot resurrect a row.
- Preview cutoff tests where records age past five minutes after preview, repeated
  confirmation after an unknown commit, and explicit 5,000-row versus whole-partition
  operation behavior.
- Owner lease renewal/expiry and stale `capturing` transition to
  `unknown`/`degraded`, including a late valid owner finish.
- Retention races against heartbeat and terminal writes under a locked old partition:
  a live lease blocks drop, while a finalized or expired lease permits the next run.
- Database, partition, and encryption failure isolation, persistent keyring restart,
  rotation, retained-key reads, and premature key removal.

### Gateway regression tests

- Byte-for-byte response equality with auditing disabled and enabled.
- Equal HTTP status, headers, SSE events, and WebSocket events.
- Equal account selection, billing amount, balance change, and usage row content.
- Success, local validation error, upstream error, timeout, cancellation, and panic
  recovery.
- All current protocol and cross-protocol forwarding paths.
- A route-manifest test classifies every gateway method/path and fails closed for new
  unclassified routes.
- A parameterized failure matrix covers disabled no-op behavior, extractor
  rejection/unsupported content, memory exhaustion, queue saturation,
  compression/encryption failure, transient and persistent database outage, missing
  partition, panic in synchronous `Begin`/`Observe`/`Finish`, worker panic,
  shutdown/drain, lease-renewal failure, and stale capture recovery. Every case
  asserts unchanged client bytes/status/headers, account scheduling, billing amount,
  balance, usage rows, and existing prompt/security audit behavior. Admin-only detail
  decryption failures are verified separately and never enter the gateway matrix.
- Fake-clock shutdown tests assert immediate no-op admission, normal completion during
  the 30-second grace, forced audit detachment afterward, two-minute bounded worker
  drain, re-enable conflict while disabling, and unchanged long-running client bytes.

### Frontend tests

- API contract and cursor behavior.
- Filters and list states.
- Detail loading and transcript rendering.
- Payload unavailable/decode failure states.
- Configuration conflict and duplicate-submit protection.
- Single and preview/confirm deletion.
- Chinese/English integration and navigation permission.

### Required project validation

Implementation delivery must run the repository-required commands, including:

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

Any skipped command must be reported with its concrete reason.

## Rollout and Rollback

1. Deploy additive migrations and code with `enabled=false`.
2. Verify encryption readiness, current/future partition health, and admin APIs.
3. Run representative disabled/enabled load tests and compare CPU, memory, P95, and
   response/billing invariants.
4. Enable globally only after the performance gates pass.
5. Monitor audit queue, memory, degradation, write failures, compression ratio,
   partition health, PostgreSQL size, and normal gateway SLOs.

Rollback admission is immediate through the global switch and requires no service
restart: no new request is captured after the disable update succeeds. Runtime may
remain `disabling` for the 30-second active-session grace plus at most the remaining
two-minute job deadlines. Detachment never closes or mutates client streams. Disable
does not change or delete existing records; retention continues according to
configuration.

## Removal Plan

1. Disable the feature and wait for in-flight work to drain.
2. Export or allow retention to remove remaining audit records as required.
3. Remove the frontend route/navigation and backend admin route/Wire providers.
4. Remove `internal/conversationaudit/` and its small optional recorder mount points,
   or leave the generic no-op boundary if another observer uses it.
5. Add a new forward migration to drop conversation audit partitions, the parent
   table, delete tombstones, and its setting. Never edit the original applied
   migration.

No core user, API key, group, account, usage, billing, payment, or security audit
schema requires rollback.

## Approved Decisions

- Store readable structured conversations, not raw HTTP protocol bodies.
- Record success, failure, timeout, cancellation, partial output, and process-unknown
  outcomes.
- Default retention is seven days and administrators can change it or manually
  delete records.
- Request and response each default to a 1 MiB canonical payload cap.
- Payloads are compressed and encrypted and are not searchable.
- Expected volume exceeds 100,000 requests per day with about 1,000 concurrent
  requests.
- Gateway availability takes priority under audit overload.
- Isolation, default-off behavior, and easy removal are hard requirements.
