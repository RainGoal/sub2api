# Seedance Video API

Sub2API exposes one public asynchronous video contract for the `seedance`
platform. The upstream account protocol is an implementation detail: callers
must not depend on provider-specific task fields, status names, URL paths, or
response headers.

## Endpoints

The gateway accepts both `/v1` paths and the existing no-prefix aliases:

```text
POST   /v1/videos/generations
GET    /v1/videos/{id}
GET    /v1/videos/{id}/content
DELETE /v1/videos/jobs/{id}
```

`POST /v1/videos` is an alias of the create endpoint. Status and content also
accept the existing `/videos/generations/{id}` and `/videos/jobs/{id}` forms.
The `id` in every public response is the task identifier for the request. In
the current release it is the identifier returned by the selected upstream
adapter; callers should treat it as opaque and use it only with the gateway's
status, content, and cancellation endpoints. A gateway-owned opaque ID can be
introduced later without changing the response fields.

## Account Models and Sales Prices

Create and edit account forms offer an explicit choice between all built-in
models supported by the selected protocol and an allowlist of selected models.
Existing accounts without `credentials.model_mapping` remain unrestricted
within their protocol's built-in catalog. A selection is stored as same-model
entries in `model_mapping`; arbitrary model rewrites and wildcards are not
supported for video accounts. An empty restricted selection cannot be saved.
Changing protocols retains only compatible selections and requires choosing
again if none remain.

| Model | Protocols | Configurable resolutions |
| --- | --- | --- |
| `seedance-2.0` | bblabu V1, fflink V1 | 480p, 720p, 1080p, 4k |
| `seedance-2.0-fast` | fflink V1 | 480p, 720p |
| `seedance-2.0-mini` | fflink V1 | 480p, 720p, 1080p |
| `seedance-2.5` | bblabu V1, fflink V1 | 480p, 720p |

This is Sub2API's built-in compatibility catalog, not a guarantee that an
upstream API key has access to every listed model. Select the models available
to the actual upstream key. Mini retains its existing adapter support.

Configure sales prices under **Channels → edit/create channel → Seedance →
Model Pricing** and associate the customer groups. Each model has independent
USD/second prices and only its supported resolution tiers are displayed.
Groups retain their access, account association, and video sales multiplier.
Unpriced tiers cannot create tasks. Unknown or unsupported Seedance resolutions
never borrow the 480p price. `bytedance/seedance-2.5` and `Seedance-2.5` are aliases
of `seedance-2.5` for model selection and pricing. Duplicate model aliases cannot
be saved as conflicting channel entries. Running tasks retain their frozen price
snapshot. `/api/v1/groups/available` keeps its existing `video_model_prices`
response field and projects the effective channel sales prices into it.
If that projection fails, only the affected group's prices are omitted; the
group list remains available. Task creation still validates the actual price.

Legacy group prices remain effective until a channel takes ownership by saving
Seedance video model pricing. Old channel Token/image/per-request rows do not
take ownership and can be retained unchanged during ordinary channel edits.
Editing Seedance prices requires the new USD/second video format; no old units
are converted automatically. The group dialog shows old prices as read-only.
Use **Import legacy group prices** in the channel dialog to preview existing
prices, review the rows, then save normally. All selected Seedance groups must
have identical legacy price maps, including which tiers are missing. Conflicts
require separate channels or a manually reviewed common price. Removing prices
or disabling a channel that has taken ownership does not revive legacy prices.
Ownership follows the channel association: deleting the channel or detaching a
group restores that group's legacy compatibility behavior. Keep the association
and disable the channel when stopping sales without using legacy prices.

The preview endpoint is `POST /api/v1/admin/channels/seedance-pricing/import-preview`
with `{ "group_ids": [1, 2] }`. It uses the existing admin authentication and
standard response envelope, returning `{ "model_pricing": [...] }`. It performs
no writes. Invalid selections return HTTP 400; conflicting maps return HTTP 409
with `SEEDANCE_LEGACY_PRICING_CONFLICT`. Channel saves remain on the existing
admin channel create/update routes. Seedance sales pricing does not use time
pricing.

Deployment needs the backend and rebuilt admin frontend. Normal startup
automatically applies the embedded `238_seedance_account_cost_snapshot.sql`
migration for upstream cost snapshots. For online upgrades, wait for the tag's
Release build, install it through the existing update flow, and restart as
prompted; no manual SQL or immediate price reconfiguration is required. Verify
account selection persists after reopening, `/v1/models`
reflects account selections, and a configured model/resolution completes the
existing create/status/content flow. Check missing prices are rejected and
completed tasks are billed once. The public video routes remain unchanged.

## Upstream Account Costs and Profit

Open **Accounts → create/edit account → Model Purchase Cost**. Each supplier
account stores its own model prices in `extra.model_cost_pricing`, using the
existing `ChannelModelPricing[]` structure. Choose video billing and enter
USD/second for each resolution, or per-request billing for a fixed USD/task
cost. An explicit default video price covers tiers without their own price;
`0` is a valid free price. Model matching uses the selected upstream account
and canonical model, independently of the customer group or channel.

Explicit purchase prices are final costs: the stored statistics multiplier is
automatically `1`, regardless of the account quota multiplier. For example,
a 10-second video sold at $0.10/second and purchased at $0.04/second, with sales
multiplier 1, records $1 revenue, $0.40 cost, and $0.60 gross profit.
Existing dashboard/account statistics and sales cost snapshots use the same
stored cost fields. Customer billing and account quota accounting retain their
existing formulas.

New tasks freeze the selected account's cost mode, price, and statistics
multiplier atomically with account assignment, before the upstream create
request. A create retry with another account captures that account's price.
Status-driven and background recovery settlement both use the frozen cost.
Video costs follow the adapter's actual billable duration: fflink uses output
seconds; bblabu 2.5 includes reference-video input seconds when applicable.
Fixed per-request costs apply once to the completed task.

When no account model price matches, the sales-base-price × account-multiplier
estimate is frozen. A matching model without an applicable price rejects task
submission to that account; the scheduler tries another account within the
existing switch limit and releases skipped concurrency slots. If no suitable
account remains, creation returns `seedance_account_cost_not_configured` and
releases the unsubmitted task and balance hold. This local price gap does not
count as a provider failure or silently use a different resolution or zero. Configure
costs for all enabled model/resolution combinations before accepting traffic.
Failed/canceled tasks keep the existing release behavior. Historical usage is
not recalculated, and tasks created before the migration have a NULL snapshot
and retain the previous settlement behavior.

Account create/update/bulk/import validation shares the existing admin account
routes. Omitting `extra.model_cost_pricing` preserves the latest stored prices;
an explicit empty array clears them. Invalid data returns HTTP 400. Non-Seedance
accounts support Token, per-request, image, and video costs in the ordinary
gateway usage path. They require a base/default price to cover usage outside
configured tiers. Token amounts use the existing per-token API units; the UI
shows USD per million tokens. Unspecified token components are zero, and no
official model price or sales discount is imported into the purchase price.
The independent `/v1/images/batches` settlement path is not covered by this
account configuration. Profit-based admission still uses its existing policy.
If ordinary gateway purchase-cost calculation fails after a successful request,
it logs a warning and retains the previous cost estimate, customer billing, and
usage log. Correct the account configuration before treating that estimate as
the actual purchase cost.

The migration only adds a nullable JSONB column to the existing task table.
No public video endpoint or switch changes. After deployment, reopen saved
channel and account prices, verify USD/second values, and check successful tasks
from two supplier accounts with different costs. Verify account cost, gross
profit, and that repeated status/content polls do not charge twice. Rolling
back the app can leave the added column in place; preserved legacy group prices
are available to older code.

## Create Request

```json
{
  "model": "seedance-2.5",
  "prompt": "A slow cinematic push-in over a product on a desk",
  "resolution": "720p",
  "duration": 10,
  "aspect_ratio": "16:9",
  "audio": false,
  "referenceImages": [],
  "referenceVideos": [],
  "referenceAudios": []
}
```

`model`, `prompt`, `resolution`, `duration`, and `aspect_ratio` are validated
and normalized by Sub2API. Reference URLs must be public HTTPS URLs. Provider
specific payload conversion and capability checks happen after the request has
entered the gateway.

## Unified Success Response

Create returns `202 Accepted`; status and cancellation return `200 OK`.
All three JSON endpoints use exactly the following fields:

```json
{
  "id": "task-123",
  "object": "video",
  "status": "queued",
  "model": "seedance-2.5",
  "resolution": "720p",
  "duration": 10,
  "content_url": null,
  "error": null
}
```

Public status values are:

| Public value | Meaning |
| --- | --- |
| `queued` | Accepted and waiting for generation. |
| `in_progress` | Generation is running or being finalized. |
| `completed` | Content can be fetched from `content_url`. |
| `failed` | Generation reached a terminal failure. |
| `canceled` | The task was canceled. |

The create endpoint is always asynchronous: an upstream `success` or
`succeeded` acknowledgement is exposed as `queued`, and the caller should
poll the task endpoint for the actual terminal state.

`content_url` is non-null only for `completed` tasks and points to the local
gateway path `/v1/videos/{id}/content`. `error` is non-null only for a failed
task and has the shape `{ "code": "video_generation_failed", "message":
"Video generation failed" }`. Transport, authentication, validation, and
  upstream availability errors continue to use the gateway's normal `error`
  envelope and HTTP status codes. A successful cancellation acknowledgement for
  a non-terminal task returns `status: "canceled"`, even when an upstream uses a
  generic `success`/`succeeded` or empty response body. If the provider explicitly
  returns `failed`, `error`, or a boolean `success:false`, the gateway returns
  `status: "failed"` with the stable error `{ "code": "video_cancellation_failed",
  "message": "Video cancellation failed" }` and releases the hold as a failed
  task. If the provider
  explicitly reports `completed`, the gateway does not release the hold; it
  returns `409` with `video_cancellation_conflict` and resumes normal settlement.
  The BBLabu adapter does not expose task cancellation; for a non-terminal BBLabu
  task, `DELETE` returns `501` with the stable `operation_not_supported` error
  instead of forwarding provider details. If another request is already
  finalizing the task, cancellation returns `409` with
  `video_cancellation_in_progress`; retry after the status poll settles.

The gateway writes this response itself. It never forwards upstream
`task_id`, `job_id`, provider names, provider status strings, or provider JSON
success bodies to callers.

Cancellation is idempotent for a task that is already `completed`, `failed`, or
`canceled`: the gateway returns that durable terminal state instead of
rewriting it as `canceled`. A cancellation request for a still-running task
returns `canceled` after the upstream cancellation operation is acknowledged.

## Content Download

`GET /v1/videos/{id}/content` returns the video bytes, not the JSON task
envelope. Existing `Range`, `If-Range`, and media response behavior is kept so
clients can stream or resume downloads. The same API key that created the task
is required for status, cancellation, and content access.

Content is available only after the task is locally settled as `completed`.
Canceled, failed, released, or still-finalizing tasks return `409` with
`video_content_unavailable`; the gateway never serves bytes from a provider
after a local terminal release.

Task lookup and cancellation still perform API-key, user, group, IP, and task
ownership checks. They do not consume a second generation charge, so polling
and cancellation remain available after the key's generation balance is
exhausted.

## Provider Boundary

The `videoprovider` package translates each registered upstream protocol into
the internal task vocabulary. Adding or changing an upstream adapter must not
change this public response schema. Provider-specific account selection and
credentials remain an administrator-side concern.
