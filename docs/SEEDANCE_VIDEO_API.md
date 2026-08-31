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
