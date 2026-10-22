# Video Provider Drivers

`videoprovider` is the protocol boundary between Sub2API's stable video API and
upstream-specific asynchronous video APIs. The `seedance` platform name remains
only as a backward-compatible quota and scheduling namespace.

## Driver ownership

Each driver owns all upstream protocol details:

- HTTP method, URL path and authentication headers
- create payload encoding and provider-specific validation
- task ID and task status response parsing
- model-list response parsing
- billing behavior that differs by protocol

Core handlers and services must call `Driver.BuildRequest`, `Driver.ParseTask`
and `Driver.ParseModels`. They must not switch on a provider ID or reconstruct
provider URLs, authentication headers or payload fields.

## Adding a protocol

1. Add one driver file in this package and implement `Driver`.
2. Register it once in `defaultRegistry` in `registry.go`, including stable aliases.
3. Add the protocol metadata once in `frontend/src/constants/videoProviders.ts`.
4. Add a contract test covering create, status, content, cancellation (when
   supported), model discovery and status normalization.
5. Add model pricing/capability configuration only for models Sub2API can bill.

An upstream that already implements a registered protocol needs no new driver:
create an account with that protocol ID, API key and custom base URL.

Protocol IDs are persisted on asynchronous tasks. Never rename or repurpose a
registered ID; add a new versioned ID when a protocol changes incompatibly.

## Video Tasks JSON v1

`video_tasks_json_v1` is an opt-in JSON task protocol. It requires an explicit
HTTPS API base URL; a bare origin becomes `/v1`, while an explicit API prefix
is preserved. It uses POST `/videos`, GET `/videos/{id}`, GET
`/videos/{id}/content`, and GET `/models`. DELETE `/videos/{id}` is available
only when an account's frozen model capability explicitly enables it. None of
the BBLabu presets enables cancellation because its public documentation does
not establish a cancellation contract.

`BBLabuModelCapabilities` returns 33 independent presets verified against the
public catalog `2026-10-16.1` and media documentation on 2026-10-17. These do
not change legacy `CanonicalModel`, `LookupModel`, or `DefaultModelIDs`.
`ParseModels` intentionally retains new upstream model IDs; discovery alone
does not authorize generation without a matching enabled capability.

Call `ParseVideoModelCapabilities` for account JSON, then
`ParseCreateRequestWithCapabilities` for user input. The latter freezes the
selected `VideoModelCapability` in `CreateRequest.Capability`. Pass the same
capability through `RequestParams.Capability` for subsequent operations.
Account overrides take precedence over presets, including disabled overrides.
Profiles `bblabu_official` and `bblabu_route` choose documented field names;
the builder creates a fresh allowlisted payload and never forwards `Raw`.

Capabilities distinguish controlled, fixed, and unknown output durations.
Unknown duration does not acquire an invented numeric default. Billing units
are `per_second` or `per_video`; metering policies are `output_seconds`,
`output_and_reference_seconds`, and `per_video`. Account pricing must verify
the capability's metering policy and trust flag before accepting a request.
The new driver's protocol-level `BillingPolicy` is not a model billing rule.
Its response parser preserves fractional seconds in `DurationSecondsExact`
and `ReferenceInputSecondsExact`; the integer fields remain for old callers.
Output metering also accepts top-level `seconds` as a JSON number or numeric
string as a fallback after existing `duration` fields. All metering must remain
finite, nonnegative, and at most 3600 seconds.

The sd14 route accepts `reference_video_durations` as gateway validation
metadata to enforce its documented 25-second input-plus-output limit; that
metadata is never forwarded or trusted for monetary usage. MM3 reference
videos remain disabled until their duration wire format is verified. Other
documented combinations remain available. Model presets contain no prices.

## Zhiqi v1 and mixed protocols

`zhiqi_v1` uses the same task endpoint paths and Bearer authentication, with
`https://zhiqiapi.com/v1` as its default base URL. It has no built-in model
names: discover `/models`, then explicitly configure each model capability
using the `zhiqi_v1` field profile and configure purchase and sales pricing.
The protocol does not document task cancellation and rejects that capability.

Both capability drivers implement the optional `ModelCapabilityDriver`
interface. Services use `SupportsModelCapabilities` and
`ValidateCapabilityProvider` instead of identifying capability support by
provider ID. A capability's `model` remains the customer-facing identity;
optional `upstream_model` changes only the provider's create payload. Both
identities and the field profile must stay frozen with the task.

The common API keeps its existing input semantics. Zhiqi maps reference arrays
to `reference_image_urls`, `reference_video_urls`, and `reference_audio_urls`,
and start/end frames to `first_frame_image_url`/`last_frame_image_url`. A first
frame or the first reference image is never silently reinterpreted as Zhiqi's
separate primary `input_reference`. Provider-only fields are not forwarded
from `Raw`. The Zhiqi create payload explicitly sends `n: 1`, matching the
gateway's single-output contract.

Zhiqi's optional `usage.duration_seconds` supplies exact output metering only
for this driver. Missing usage stays unknown; request duration, progress,
elapsed task time, and provider cost are never substituted for actual usage.
Zhiqi does not expose actual reference-video seconds, so its capability rejects
`output_and_reference` billing and `output_and_reference_seconds` metering.
`SupportsReferenceVideoMetering` lets admission reject incompatible sales or
purchase quantity policies before submitting a task; accepting a reference
video does not establish a monetary metering contract.
Its `expired` terminal status maps to failure. Existing BBLabu parsers retain
their original metering fields and status behavior.

The optional `ContentURLParser` handles Zhiqi's documented legacy JSON content
fallback. It extracts a public HTTPS URL without issuing network requests.
The gateway must then apply its normal URL/DNS/redirect checks, fetch the media
without the supplier API key, and require a video response.
