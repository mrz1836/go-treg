# Coverage

This page summarizes which of Treg's operations go-treg covers. The full,
machine-checked list is [operations.tsv](operations.tsv): one row per
operation in Treg's OpenAPI document (`/admin/*` expanded), plus four
supplements that the document does not list. A test
(`TestOperationInventory`) keeps it in step with the code: every
`Operation` constant maps to its rows, and the counts below hold.

Source: `https://treg.to/openapi.json`, sha256
`468107e5cc12d4bda18b3eac926bb35c9a2d52057152fef12c84e5f7591878c8`, read on
2026-10-17: 159 paths, 190 operations. Its `info.version` is `0.0.1`, which
is not a stable API version.

## In this release

| Disposition | Count | Operations | SDK |
|---|---:|---|---|
| I-exec | 7 | `/call/{rest}` with DELETE, GET, HEAD, OPTIONS, PATCH, POST and PUT | `Call`, `CallBytes`, `CallJSON` |
| I-typed | 20 | `/meta`; catalog platforms, platform, provider, search and endpoint; `/auth/me`; `/orgs`; `/usage/me`; calls list, get and result; org usage, spend, by-tag, tag-keys, balance and provider-resources; tools list and by-name | `CatalogClient`, `Calls()`, `Account()`, `Orgs()`, `Tools()` |
| I-stream | 1 | `/catalog/find` (NDJSON) | `CatalogClient.Find` |
| I-typed (supplements) | 2 | `GET /catalog/examples/{id}` (public), `GET /catalog/endpoints/{id}/access` (member) | `CatalogClient.Example`, `Client.EndpointAccess` |

The execution seam reaches every catalog endpoint, including ones Treg adds
after this release: `CatalogEndpoint(id)` takes any well-formed `EndpointID`
and never checks a catalog snapshot. That proves the SDK is open, not that an
endpoint exists.

## Not in this release

| Disposition | Count | What | Why |
|---|---:|---|---|
| L-read | 24 | activity, runs, health, billing and its history, referrals, invites, feedback, hub reads, org settings, budgets, members, projects, pins and deny rules, bundles, API-key spend | Later; each needs its own approval |
| L-write | 37 | org, member, project and policy writes; tool, skill and bundle registration; hub publishing; feedback and reviews; referral code; the active `POST /health/run` | Later; state-changing |
| L-proto | 3 | `/media` upload, `/run`, `/hub/run` | Later; their own protocols |
| D-sec | 33 | API keys, agents, the public token, secrets, connections, OAuth, `GET /auth/cli-token` (a GET that discloses a token), token revocation | Deferred credential surface |
| O-ui | 32 | login, onboarding and demo flows | Browser flows, outside an SDK |
| O-cli | 3 | local-run grants and run reports, the CLI deny policy | Treg CLI internals |
| O-bill | 3 | top-up, auto-top-up, billing portal | Money movement stays in Treg's UI |
| O-admin | 27 | every `/admin/*` operation | Operator-only |
| O (supplements) | 2 | `/catalog/call/{rest}` (connector-only, 404 by default); `/mcp/`, `/.well-known/*`, `/providers.json`, `/tool-requests`, `/table/` | Outside the SDK or unverified |

Total: 190 OpenAPI operations, 28 in this release and 162 not, every one with
a disposition, plus the four supplements: two in this release and two outside
the SDK.

## Evidence

Typed read models follow Treg's pinned source (revision `7b118d2`) and, for
the public routes, live responses observed on 2026-10-17. The provider
resource rows were verified from the pinned source. Fields that the source
emits only sometimes, or with a type that varies, are `Optional` or raw
JSON, so additive changes need no release. See
[the integration guide](integration-guide.md) for writing your own typed
packages on top of the execution seam.
