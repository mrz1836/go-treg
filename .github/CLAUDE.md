# CLAUDE.md

## 🤖 Welcome, Claude

This repository uses **`AGENTS.md`** as the single source of truth for:

* Coding conventions (naming, formatting, commenting, testing)
* Contribution workflows (branch prefixes, commit message style, PR templates)
* Release, CI, and dependency‑management policies
* Security reporting and governance links

> **TL;DR:** **Read `AGENTS.md` first.**
> All technical or procedural questions are answered there.

### Quick Checklist for Claude

1. **Study `AGENTS.md`**
   Make sure every automated change or suggestion respects those rules.
2. **Follow branch‑prefix and commit‑message standards**
   They drive Mergify auto‑labeling and CI gates.
3. **Never tag releases**
4. **Pass CI**
   Run `go fmt`, `goimports`, `go vet`, `staticcheck`, and `golangci‑lint` locally before opening a PR.

If you encounter conflicting guidance elsewhere, `AGENTS.md` wins.
Questions or ambiguities? Open a discussion or ping a maintainer instead of guessing.

Happy hacking!

---

## About This Project

`go-treg` is an independent, unofficial Go client for the **Treg API gateway** (https://treg.to):
a typed core for Treg's own concepts (targets, controls, metadata, refusals, money), an open
execution seam over `/call/{rest}` that calls any well-formed catalog id with no SDK release
(`Call`, `CallBytes`, `CallJSON[T]`), and typed reads of the public catalog and the team's calls,
account, orgs and tools. It is a library, not an application: there is no binary, CLI or
configuration file. It is not affiliated with Treg or Superdesign and contains none of their code.

- Module: `github.com/mrz1836/go-treg` (package `treg`, one flat package)
- Go version: 1.26 (CI runs 1.26.x; checks also pass on 1.27)
- Runtime dependencies: none (`testify` is used only by tests and `internal/testutil`)
- Operation coverage: `docs/coverage.md`, machine-checked list `docs/operations.tsv`

**Invariants that changes must preserve:**
- **No runtime dependencies.** Library code imports only the standard library, and never `log`,
  `os`, `unsafe`, `reflect`, `compress/gzip` or `net/http/httputil`. `depguard` and `forbidigo`
  enforce this.
- **Exactly one HTTP attempt per call.** No retries, redirects, polling, fallbacks or invented
  idempotency keys. `GetBody` stays nil; bodyless GET/HEAD/DELETE/OPTIONS carry `noReplayBody`;
  the idempotency key is stored under the non-canonical map key `Idempotency-key`. The only
  `http.NoBody` is the documented empty POST/PUT/PATCH residual.
- **The token reaches only the configured origin.** Only as `X-Treg-Token`, never on the public
  catalog routes, never from a URL learned from a response. Redirects are refused and their
  `Location` moved aside. The token, the client settings, a Target's strings and a stream live
  only inside function values, so no fmt verb or holding can print them.
- **No values in errors.** `ValidationError` names a field and a fixed reason; `CallError` text
  holds the op, kind, status, gateway mark, a fixed reason and a refusal code only when
  `Refusal.Known()`. Causes are fixed sentinels, never `*url.Error` or JSON errors.
- **Discovery never executes; the SDK never picks an endpoint.** Typed reads are GETs.
- **No logging, globals (other than sentinel errors) or `init`, no sleeps, no environment
  reads, no proxy environment** in SDK code. The only code that runs apart from the caller is the
  `context.AfterFunc` that closes a body when its context ends, and a stream's header timer.
- **Unit tests stay offline.** The live smoke tests (`live_internal_test.go`, `live` build tag)
  run only with `TREG_LIVE=1`, are run only by the owner, by hand, and make a `/call/` request
  only with an explicit id and cost cap. Never run them, and never set those variables. The guard
  test is safe: `go test -tags live -count=1 -run TestLiveGuard .`
- **Synthetic data only.** Fixtures under `testdata/synthetic/` are hand-written to observed
  shapes with `example.com` data; never copy catalog content, provider examples or real responses.
