<div align="center">

# 🚪&nbsp;&nbsp;go-treg

**An independent, unofficial Go client for the Treg API gateway: one typed core, any catalog endpoint.**

<br/>

<a href="https://github.com/mrz1836/go-treg/releases"><img src="https://img.shields.io/github/release-pre/mrz1836/go-treg.svg?include_prereleases&style=flat-square&logo=github&color=black" alt="Release"></a>
<a href="https://golang.org/"><img src="https://img.shields.io/github/go-mod/go-version/mrz1836/go-treg?style=flat-square&logo=go&color=00ADD8" alt="Go Version"></a>
<a href="https://github.com/mrz1836/go-treg/blob/master/LICENSE"><img src="https://img.shields.io/github/license/mrz1836/go-treg.svg?style=flat-square&color=blue" alt="License"></a>

<br/>

<table align="center" border="0">
  <tr>
    <td align="right">
       <code>CI / CD</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://github.com/mrz1836/go-treg/actions"><img src="https://img.shields.io/github/actions/workflow/status/mrz1836/go-treg/fortress.yml?branch=master&label=build&logo=github&style=flat-square" alt="Build"></a>
       <a href="https://github.com/mrz1836/go-treg/actions"><img src="https://img.shields.io/github/last-commit/mrz1836/go-treg.svg?style=flat-square&logo=git&logoColor=white&label=last%20update" alt="Last Commit"></a>
    </td>
    <td align="right">
       &nbsp;&nbsp;&nbsp;&nbsp; <code>Quality</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://codecov.io/gh/mrz1836/go-treg"><img src="https://codecov.io/gh/mrz1836/go-treg/branch/master/graph/badge.svg?style=flat-square" alt="Coverage"></a>
    </td>
  </tr>

  <tr>
    <td align="right">
       <code>Security</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://scorecard.dev/viewer/?uri=github.com/mrz1836/go-treg"><img src="https://api.scorecard.dev/projects/github.com/mrz1836/go-treg/badge?style=flat-square" alt="Scorecard"></a>
       <a href=".github/SECURITY.md"><img src="https://img.shields.io/badge/policy-active-success?style=flat-square&logo=security&logoColor=white" alt="Security"></a>
    </td>
    <td align="right">
       &nbsp;&nbsp;&nbsp;&nbsp; <code>Community</code> &nbsp;&nbsp;
    </td>
    <td align="left">
       <a href="https://github.com/mrz1836/go-treg/graphs/contributors"><img src="https://img.shields.io/github/contributors/mrz1836/go-treg.svg?style=flat-square&color=orange" alt="Contributors"></a>
       <a href="https://mrz1818.com/"><img src="https://img.shields.io/badge/donate-bitcoin-ff9900?style=flat-square&logo=bitcoin" alt="Bitcoin"></a>
    </td>
  </tr>
</table>

</div>

<br/>
<br/>

<div align="center">

## <code>Project Navigation</code>

</div>

<table align="center">
  <tr>
    <td align="center" width="20%">
       🚀&nbsp;<a href="#-installation"><code>Installation</code></a>
    </td>
    <td align="center" width="20%">
       🏁&nbsp;<a href="#-quick-start"><code>Quick&nbsp;Start</code></a>
    </td>
    <td align="center" width="20%">
       💡&nbsp;<a href="#-usage"><code>Usage</code></a>
    </td>
    <td align="center" width="20%">
       ⚙️&nbsp;<a href="#%EF%B8%8F-behavior"><code>Behavior</code></a>
    </td>
    <td align="center" width="20%">
       📚&nbsp;<a href="#-documentation"><code>Documentation</code></a>
    </td>
  </tr>
  <tr>
    <td align="center">
       🧪&nbsp;<a href="#-examples--tests"><code>Examples&nbsp;&&nbsp;Tests</code></a>
    </td>
    <td align="center">
      🔧&nbsp;<a href="#-development"><code>Development</code></a>
    </td>
    <td align="center">
      🛠️&nbsp;<a href="#%EF%B8%8F-code-standards"><code>Code&nbsp;Standards</code></a>
    </td>
    <td align="center">
      🤖&nbsp;<a href="#-ai-usage--assistant-guidelines"><code>AI&nbsp;Usage</code></a>
    </td>
    <td align="center">
       🤝&nbsp;<a href="#-contributing"><code>Contributing</code></a>
    </td>
  </tr>
</table>
<br/>

go-treg calls [Treg](https://treg.to), an API gateway that puts thousands of provider endpoints behind one
proxy route, `/call/{rest}`, next to its own catalog and account routes. Use it when a Go service needs to
discover endpoints, call any of them with types it owns, and stay in control of every billed request.

> **Independent and unofficial.** go-treg is not affiliated with, endorsed by or supported by Treg or
> Superdesign. It is a client of Treg's hosted API and contains none of Treg's code.

- **Open execution seam.** `CatalogEndpoint(id)` calls any well-formed catalog id, including ones Treg adds
  after this release, through the same path as every other call. No snapshot, registry or lookup.
- **Your types.** `CallJSON[T]` decodes into a type you own; `Call` streams binary answers; `CallBytes`
  keeps the raw bytes. A typed integration is an ordinary package of functions, with no fork.
- **Discovery never executes.** The catalog client reads, ranks nothing and runs nothing.
- **One attempt per call.** No retries, redirects, fallbacks, polling or invented idempotency keys, and Go's
  HTTP clients cannot resend a request either (one documented HTTP/2 residual for empty writes).
- **Money is integers.** `MicroUSD`, never floats; "unknown" stays distinct from zero.
- **Bounded and private.** Every call has a deadline and a size limit. Errors and printed values never hold
  the token, URLs, queries, header values, bodies or provider text.
- **Zero runtime dependencies.** The standard library only.

It is a library, not an application: no binary, CLI, configuration file or background work.

<br/>

## 📦 Installation

**go-treg** requires a [supported release of Go](https://golang.org/doc/devel/release.html#policy), 1.26 or
later.

```bash
go get github.com/mrz1836/go-treg
```

### Credentials

Every authenticated request carries your token as `X-Treg-Token`, only to the configured origin
(`https://treg.to` by default), never on the public catalog routes, never in a URL, body, error or log.
A per-org token or dashboard API key has its team baked in; an identity token from `treg login` that is not
pinned to a team also needs `WithOrg(slug)`. Your application decides where the token comes from: the SDK
reads no environment variables or files.

<br/>

## 🏁 Quick Start

Discover an endpoint, then call it with your own types:

```go
found, err := cat.Search(ctx, treg.SearchRequest{Query: "url metrics", Limit: 5})
```

```go
res, err := treg.CallJSON[urlMetricsOut](ctx, client, treg.CallRequest{
	Target: treg.CatalogEndpoint("moz.web.url.metrics"),
	Method: http.MethodPost,
	Body:   treg.JSONBody(urlMetricsIn{Targets: []string{"example.com"}}),
	Controls: treg.Controls{
		MaxCost:        new(treg.MicroUSD(20_000)), // $0.02 server-enforced reserve ceiling
		IdempotencyKey: "job-42-urlmetrics",        // reuse ONLY to recover this exact request
	},
})
```

`res.Value` is your type, `res.Meta` what Treg's headers said (call ref, cost as reported). Try it offline,
with no token and no network:

```bash
go run ./examples/offline
```

<br/>

## 💡 Usage

### Set up once

```go
client, err := treg.NewClient(token, opts...)
```

```go
cat, err := treg.NewCatalogClient(opts...)
```

Create one `Client` and share it: it is immutable and safe for concurrent use. `client.Catalog()` gives a
catalog client with the same settings that never sends the token. Options: `WithOrg`, `WithBaseURL` (a
self-hosted Treg, or a local test server), `WithHTTPClient`, `WithTimeout`, `WithStreamTimeout`,
`WithMaxRequestBytes`, `WithMaxResponseBytes`, `WithMaxStreamBytes`, `WithUserAgent`,
`WithResponseCapture` and `WithErrorBodies`. Every option is validated by the constructor; errors name the
option, never its value.

### Discover

```go
for _, ep := range found.Value.Results {
	cost, _ := ep.Cost.Get()
	fmt.Println(ep.ID, ep.Kind, cost.Type) // ranking is advice; the SDK never picks or runs one
}
desc, err := cat.Endpoint(ctx, treg.EndpointRequest{ID: "moz.web.url.metrics"})
```

`Endpoint` returns the descriptor: method, path, Treg's own input description (`Input`, kept raw: it is not
JSON Schema), the list price (`Cost`, display text), the async descriptor, the `Verified` date and siblings.
`Find` streams Treg's judged search as `FindStream.Events()`.

### Call any endpoint, even a new one

```go
raw, err := client.CallBytes(ctx, treg.CallRequest{
	Target: treg.CatalogEndpoint("future.vendor.resource.read"),
	Method: http.MethodGet,
	Query:  url.Values{"limit": {"5"}, "id": {"abc"}}, // {id}-style path params ride in the query (server-verified)
})
if err != nil {
	panic(err) // local/transport/size failures only
}
if err := raw.StatusError(); err != nil { // non-2xx stays inspectable; Refusal parsed when Treg refused
	panic(err)
}
```

Targets: `CatalogEndpoint(id)` (direct or routed id), `RegisteredTool(name, path)` (an org tool plus an
upstream path, sent byte for byte) and `UpstreamURL(url)` (a registered tool's full URL). Treg tries an org
tool before a catalog id, so an org tool with the same name shadows it.

### Stream binary answers

```go
resp, err := client.Call(ctx, treg.CallRequest{
	Target: treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"),
	Method: http.MethodPost,
	Header: http.Header{"Model": {"s2.1-pro"}},
	Body:   treg.JSONBody(map[string]string{"text": "Hello from example.com"}),
})
```

```go
defer func() { _ = resp.Close() }() // ends the request and both timers; no goroutine survives
if serr := resp.StatusError(ctx); serr != nil {
	panic(serr)
}
n, err := io.Copy(&dst, resp) // ≤ WithMaxStreamBytes; ctx/WithStreamTimeout stop it; n = bytes written
```

Provider headers go in `Header` (framing, routing, cookie, authorization, idempotency, `X-Treg-*` and the
SDK's own names are refused); a provider key goes in `ProviderAuthorization`, the only way to send an
upstream `Authorization`. Bodies are `JSONBody`, `BytesBody`, `FormBody` (encoded or copied before
dispatch) and `ReaderBody` (exactly `size` bytes, never closed by the SDK).

### Routed calls

```go
switch res.Value.Treg.Outcome {
case treg.RouteOutcomePending: // HTTP 202: keep Treg.CallRef + Treg.Async; never resubmit; check later explicitly
	fmt.Println("pending", res.Meta.StatusCode, res.Value.Treg.CallRef, res.Value.Treg.ChargedMicro.IsNull())
case treg.RouteOutcomeMiss: // 200 with output.email == null; may still be charged (Treg.ChargedMicro)
	fmt.Println("miss")
case treg.RouteOutcomeHit, treg.RouteOutcomeWeak:
	fmt.Println("served by", res.Meta.ServedBy)
}
```

Decode a routed id (`treg.*`) with `CallJSON[treg.Routed[O]]`: `Output` is your type, `Raw` the serving
provider's document byte for byte, `Treg` the `_treg` block (attempts, charge, outcome). A 202 is a success;
`ChargedMicro` is null while pending: unknown, not zero.

### Async submissions

```go
d := sub.Meta().Async // nil → not async; when set, Meta().CostMicro is a RESERVATION
```

`Metadata.Async` is the `X-Treg-Async` descriptor: where to poll, which statuses mean done, where the
result is. Following it is your own explicit code; the SDK never polls.

### Controls

`Controls` writes only what you set: `IdempotencyKey`, `MaxCost` (`X-Treg-Route-Max-Cost`, the reserve
ceiling Treg enforces), `Tags` (`X-Treg-Meta`), `MaxAge`, `NoCache`, `Route` (waterfall, strict filters,
verify, prefer, exclude) and `Extra` for `X-Treg-*` controls added later (such as `X-Treg-Body-Encoding`).
Invalid values are refused, never truncated.

### Errors

```go
fmt.Println(err)
if ce, ok := errors.AsType[*treg.CallError](err); ok && ce.Refusal != nil {
	fmt.Println(errors.Is(err, treg.ErrGatewayRefusal), ce.Refusal.Code, ce.Refusal.BalanceMicro.DecimalUSD())
}
if errors.Is(err, treg.ErrOutcomeUnknown) {
	fmt.Println("reconcile with Calls().Get before repeating")
}
```

On a 402 refusal, `fmt.Println(err)` prints `treg: call: http_status (HTTP 402, gateway): insufficient_balance`.
A `*ValidationError` means nothing was sent. A `*CallError` has a `Kind` (matched by a sentinel with
`errors.Is`), a fixed `Reason`, the `StatusCode`, `GatewayMarked`, `OutcomeUnknown`, the parsed `Refusal` and
the `Meta`. `ErrOutcomeUnknown` means the request may have executed and been billed: reconcile with
`Calls().Get` before repeating.

### Money

```go
m, err := treg.ParseDecimalUSD("0.02")
```

`MicroUSD` is an integer number of millionths of a dollar. `Metadata.CostMicro` is what Treg reported: `nil`
means unknown (an own-key call, or a refusal before any reserve), not zero; on an async submission it is the
reservation. List prices in the catalog are `Decimal` display text. A balance is not a budget:
`Controls.MaxCost` is the only hard cap Treg enforces per call.

### Typed reads

| Method | Route | Notes |
|---|---|---|
| `CatalogClient.ServerInfo` | `GET /meta` | public |
| `CatalogClient.Platforms`, `Platform`, `Provider` | `GET /catalog/platforms[/{slug}]`, `/catalog/providers/{service}` | public |
| `CatalogClient.Search`, `Find` | `GET /catalog/search`, `/catalog/find` (NDJSON) | public |
| `CatalogClient.Endpoint`, `Example` | `GET /catalog/endpoints/{id}`, `/catalog/examples/{id}` | public |
| `Client.EndpointAccess` | `GET /catalog/endpoints/{id}/access` | a preview, never a quote |
| `Calls().List`, `Get`, `Result` | `GET /calls`, `/calls/{call_ref}`, `/calls/{call_id}/result` | the ledger reconciles money |
| `Account().Me`, `Orgs`, `Usage` | `GET /auth/me`, `/orgs`, `/usage/me` | |
| `Orgs().Balance`, `Usage`, `Spend`, `UsageByTag`, `TagKeys`, `ProviderResources` | `GET /orgs/{org_id}/...` | admin+ except Balance and ProviderResources |
| `Tools().List`, `ByName` | `GET /tools`, `/tools/by-name/{name}` | |

Nullable or optional fields are `treg.Optional[T]` (absent, null and value stay apart); open fragments stay
`json.RawMessage`. Every typed read but the `Find` stream accepts `treg.CaptureResponse(true)` to keep the
exact bytes it decoded. Endpoint ids are `treg.EndpointID` in every model, so an id you read passes straight
to `CatalogEndpoint` or `EndpointRequest`. Choice fields are typed (`SearchSort`, `SpendGroup`, `SpendStack`,
`ResourceSource`), in requests and in the echoes `SearchResults.Sort`, `Spend.Group` and `Spend.Stack`. An
optional number or id set to 0 (`Limit`, `BeforeID`, `KeyID`, ...) is left out of the query, so Treg's
default applies.

### Write your own integration

A typed integration is a package of functions over `CallJSON`, `CallBytes` or `Call`. See
[examples/extension/urlmetrics](examples/extension/urlmetrics) (JSON),
[examples/extension/fishtts](examples/extension/fishtts) (binary stream with a required provider header)
and the [integration guide](docs/integration-guide.md).

### Testing your application

Point a client at a local TLS server, as [examples/offline](examples/offline/main.go) does:

```go
srv := httptest.NewTLSServer(fakeTreg())
defer srv.Close()
opts := []treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}
```

<br/>

## ⚙️ Behavior

### Exactly one attempt

Each call is one HTTP attempt. The SDK never retries, never follows a redirect (the `Location` is moved
aside so `http.Client` never parses it, and is kept only in the sensitive `Header()` clone), never polls,
never falls back and never invents an idempotency key. Requests are built so that net/http cannot resend
them: `GetBody` is always nil, bodyless GET, HEAD, DELETE and OPTIONS carry a non-rewindable empty body, and
the idempotency key is stored under a spelling net/http's replay check does not see. The documented residual:
an empty POST, PUT or PATCH is sent as `http.NoBody`, which HTTP/2 may resend after GOAWAY or REFUSED_STREAM
(which guarantee nothing was processed) and, on Go 1.26, after a peer PROTOCOL_ERROR (which does not).

### Status handling

| Received | `Call` | `CallBytes` | `CallJSON` and typed reads |
|---|---|---|---|
| 2xx | `*Response` | `*RawResponse` | decoded; HEAD refused locally; an empty body is an error |
| 3xx | `*Response` | `*RawResponse`, body unread | error (`unexpected_response`) |
| 4xx / 5xx | `*Response` | `*RawResponse` | error (`http_status`), `Refusal` parsed from the first 64 KiB |

A 1xx final response is an error (`unexpected_response`) for every entry point.

### The transport

The SDK builds its own transport: HTTP/2 when offered, TLS 1.2 or later, no proxy (environment variables are
never read), no cookies, no redirects. `WithHTTPClient` injects a client: it is copied and never modified, a
nil transport means the SDK's own, and a non-zero `Timeout` caps the SDK's deadlines. The injected transport
is trusted: it sees the token.

### Limits

| Bound | Default | Range |
|---|---|---|
| Buffered call (`WithTimeout`), incl. body read; also the stream header wait | 240 s | > 0 to 30 min |
| Stream life (`WithStreamTimeout`), dispatch to Close | 30 min | > 0 to 12 h |
| Request body (`WithMaxRequestBytes`) | 32 MiB | 1 B to 4 GiB |
| Buffered response (`WithMaxResponseBytes`), after decompression | 16 MiB | 1 B to 256 MiB |
| Typed reads | min(configured, 8 MiB) | fixed |
| Stream body (`WithMaxStreamBytes`) | 1 GiB | 1 B to 64 GiB |
| Refusal excerpt / kept error body | 64 KiB | fixed |
| Response headers | 64 KiB | fixed |
| Upstream request headers | 64 values, 8 KiB each, 16 KiB total | fixed |
| Find NDJSON | 1 MiB per line, 16 MiB total | fixed |

### Privacy

The token lives only inside function values. Errors, printed clients, requests, bodies, responses, captures
and metadata never contain it, nor URLs, queries, header values, bodies or provider text; refusal codes
print only when they are on the compiled allowlist (`Refusal.Known`), because an own-key upstream can send a
refusal-shaped body. `Header()`, `Capture.Bytes()` and `CallError.Body()` (with `WithErrorBodies`) are the
sensitive escape hatches. Models with personal data print their shape only; Go's fmt and reflection can
still reach exported fields of a value you keep in an unexported struct field. A request that holds the
token anywhere in caller strings or in-memory bodies is refused before it is sent (a token of 16 bytes or
more; `ReaderBody` contents are not scanned).

### What is not covered

Treg's write, credential, billing, onboarding and admin routes, and some of its reads, are out of this
release: see [coverage](docs/coverage.md) and the full [operation inventory](docs/operations.tsv). The SDK
does not poll async tasks, follow `superseded_by`, interpret provider documents or choose endpoints for you.

<br/>

## 📚 Documentation

View the generated [documentation](https://pkg.go.dev/github.com/mrz1836/go-treg?tab=doc),
[coverage](docs/coverage.md), the [integration guide](docs/integration-guide.md) and Treg's own
[documentation](https://treg.to/docs).

<br/>

<details>
<summary><strong><code>Development Setup (Getting Started)</code></strong></summary>
<br/>

Install [MAGE-X](https://github.com/mrz1836/mage-x) build tool for development:

```bash
# Install MAGE-X for development and building
go install github.com/mrz1836/mage-x/cmd/magex@latest
magex update:install
```

</details>

<details>
<summary><strong><code>Library Deployment</code></strong></summary>
<br/>

This project uses [goreleaser](https://github.com/goreleaser/goreleaser) for streamlined library releases to GitHub. To get started, install it via:

```bash
brew install goreleaser
```

The release process is defined in the [.goreleaser.yml](.goreleaser.yml) configuration file.

Then create and push a new Git tag using:

```bash
magex version:bump bump=patch push=true branch=master
```

</details>

<details>
<summary><strong><code>Build Commands</code></strong></summary>
<br/>

View all build commands

```bash
magex help
```

</details>

<details>
<summary><strong><code>GitHub Workflows</code></strong></summary>
<br/>

All workflows are driven by modular configuration in [`.github/env/`](.github/env/README.md) — no YAML editing required.

**[View all workflows and the control center →](.github/docs/workflows.md)**

</details>

<details>
<summary><strong><code>Updating Dependencies</code></strong></summary>
<br/>

To update all dependencies (Go modules, linters, and related tools), run:

```bash
magex deps:update
```

</details>

<br/>

## 🧪 Examples & Tests

All unit tests, fuzz tests and examples run via [GitHub Actions](https://github.com/mrz1836/go-treg/actions)
on Go 1.26.x, the supported baseline.

Runnable examples live in the package's `Example*` functions, in [examples/offline](examples/offline/main.go)
(a local fake: no token, no network, no cost) and in the two
[extension packages](examples/extension). [examples/quickstart](examples/quickstart/main.go) reads the live
public catalog, and with `TREG_TOKEN` your own usage; it is read-only and makes no request without `-live`.

> **Note:** apart from the opt-in live smoke tests below, tests never touch the network. They use in-memory
> transports and local TLS servers, with synthetic data only: `example.com` hosts and made-up tokens, ids
> and amounts.

### Live smoke tests

[`live_internal_test.go`](live_internal_test.go) is behind the `live` build tag, which normal builds and
test runs leave out (`magex test` and CI included; only the linter checks it). Only the owner runs it:

```bash
TREG_LIVE=1 go test -tags live -count=1 -run TestLive -v .
```

By default it reads only free, public routes; `TREG_TOKEN` adds your own usage. The one `/call/` test runs
only with `TREG_TOKEN`, `TREG_LIVE_CALL_ID` (a catalog id, GET) and `TREG_LIVE_MAX_COST` (a decimal USD
reserve ceiling) all set, and makes exactly one request.

<br/>

## 🔧 Development

```bash
magex format:fix # format the code (gofumpt, goimports)
magex lint       # run golangci-lint
magex test       # run all tests (fast)
magex test:race  # run all tests with the race detector (slower)
magex bench      # run the benchmarks
```

The library's dependencies are the standard library alone. This prints only the module itself:

```bash
go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' .
```

<br/>

## 🛠️ Code Standards

Read more about this Go project's [code standards](.github/CODE_STANDARDS.md).

<br/>

## 🤖 AI Usage & Assistant Guidelines

Read the [AI Usage & Assistant Guidelines](.github/tech-conventions/ai-compliance.md) for details on how AI is used in this project and how to interact with the AI assistants.

<br/>

## 👥 Maintainers

| [<img src="https://github.com/mrz1836.png" height="50" alt="MrZ" />](https://github.com/mrz1836) |
|:------------------------------------------------------------------------------------------------:|
|                                [MrZ](https://github.com/mrz1836)                                 |

<br/>

## 🤝 Contributing

View the [contributing guidelines](.github/CONTRIBUTING.md) and please follow the [code of conduct](.github/CODE_OF_CONDUCT.md).

### How can I help?

All kinds of contributions are welcome :raised_hands:!
The most basic way to show your support is to star :star2: the project, or to raise issues :speech_balloon:.
You can also support this project by [becoming a sponsor on GitHub](https://github.com/sponsors/mrz1836) :clap:
or by making a [**bitcoin donation**](https://mrz1818.com/?tab=tips&utm_source=github&utm_medium=sponsor-link&utm_campaign=go-treg&utm_term=go-treg&utm_content=go-treg) to ensure this journey continues indefinitely! :rocket:

[![Stars](https://img.shields.io/github/stars/mrz1836/go-treg?label=Please%20like%20us&style=social)](https://github.com/mrz1836/go-treg/stargazers)

<br/>

## 📝 License

go-treg is released under the [MIT License](LICENSE). Treg is a separate product with its own license; this
client copies none of its code.

[![License](https://img.shields.io/github/license/mrz1836/go-treg.svg?style=flat&v=1)](LICENSE)
