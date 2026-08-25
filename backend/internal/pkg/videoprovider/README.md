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
