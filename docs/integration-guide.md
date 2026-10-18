# Writing an integration package

go-treg calls any Treg target, whether a catalog endpoint, a registered tool or an upstream URL, through
one request type, `CallRequest`, and three entry points: `CallJSON`, `Client.CallBytes` and `Client.Call`. It
has no code for any particular endpoint, and it needs none. If you want typed access to one endpoint, write
a small package of your own on top of the public API. This note explains how.

Two working examples live in this module, each with tests:

- [`examples/extension/urlmetrics`](../examples/extension/urlmetrics/urlmetrics.go) wraps
  `moz.web.url.metrics`: a JSON request and a JSON answer, decoded with `CallJSON`.
- [`examples/extension/fishtts`](../examples/extension/fishtts/fishtts.go) wraps `fishaudio.tts.s2-1-pro`:
  a JSON request with a required provider header and a binary audio answer, streamed with `Call`.

Neither one forks the SDK, registers anything, uses reflection, fetches a schema at runtime, holds the
Treg token or adds a dependency. Yours shouldn't either.

## Call directly, or write a package?

For a one-off call, skip the package. Declare the types next to the call and use `CallJSON`:

```go
type in struct {
	Targets []string `json:"targets"`
}
type out struct {
	Results []struct {
		Page            string `json:"page"`
		DomainAuthority *int   `json:"domain_authority"`
	} `json:"results"`
}
res, err := treg.CallJSON[out](ctx, client, treg.CallRequest{
	Target: treg.CatalogEndpoint("moz.web.url.metrics"),
	Method: http.MethodPost,
	Body:   treg.JSONBody(in{Targets: []string{"example.com"}}),
})
```

Write a package when any of these apply:

- several places call the endpoint and should share one set of types;
- the endpoint has rules worth checking before you spend money, such as "1 to 50 targets" or a text-size
  limit;
- the endpoint is unusual: it needs a provider header, sends binary data or streams;
- you want one place to record which descriptor you wrote against, and to keep its tests.

A package is one exported function per operation, plus its request and response types. Its signature
takes `context.Context`, a `*treg.Client`, your request and `treg.Controls`:

```go
func Lookup(ctx context.Context, c *treg.Client, in Request, ctl treg.Controls) (*treg.Result[Response], error)
```

The function receives the `*treg.Client`, never the token. Authentication, timeouts, size limits and
error sanitizing come with the client.

## From descriptor to Go types

Read the endpoint's catalog descriptor once, while you write the package. The package does not fetch it
at runtime.

```go
cat, err := treg.NewCatalogClient() // or client.Catalog(); catalog reads never send the token
if err != nil {
	panic(err)
}
desc, err := cat.Endpoint(ctx, treg.EndpointRequest{ID: "moz.web.url.metrics"})
if err != nil {
	panic(err)
}
ep := desc.Value.Endpoint
verified, _ := ep.Verified.Get() // "2026-07-31" when urlmetrics was written
```

These fields of `treg.Endpoint` matter most:

| Field | What it tells you |
|---|---|
| `Method`, `Path` | The upstream method to send, and the provider path, including any `{id}` placeholders. |
| `Input` | The request: query parameters, path parameters, body, body type and headers. Kept raw. |
| `TestRequest` | A request known to work. Use it as your first test fixture's shape. |
| `Cost` | The list price, an `Optional[Cost]`: its `Type` (`CostQuotaRows`, `CostPerCall`, ...) and the full block in `Raw`. |
| `Async` | Set when a call returns a task to follow instead of an answer. See [Async descriptors](#async-descriptors). |
| `Verified` | The date Treg last verified the row. Record it in your package. |
| `StrictQuery` | Only declared query parameters, each once, and no body. |
| `Status`, `StatusNote`, `SupersededBy` | Set on a retired row. See [Drift](#drift). |

`EndpointDescriptor.ExampleResponse` (or `CatalogClient.Example`) holds an example answer. It is
provider data: use it to shape your response type, and write your own synthetic fixtures for tests.

`Input` uses Treg's own descriptor format, not JSON Schema. It has sections such as `pathParams`,
`queryParams`, `body`, `bodyType` (`json`, `form` or `multipart`), `headers`, `queryArrayEncoding` and
`note`. Each field is described as `{type, required, note, example, enum, ...}`, with types such as
`string`, `integer`, `array[string]` or `string_or_array`. Translate it by hand. For
`moz.web.url.metrics` the body section reads, in part:

```json
{"bodyType": "json", "body": {"targets": {"type": "array[string]", "required": true,
  "note": "1-50 URLs or domains; a bare domain is treated as that page"}}}
```

which becomes:

```go
var ErrTargets = errors.New("urlmetrics: 1 to 50 targets")

type Request struct {
	Targets []string `json:"targets"`
}
```

Rules for the types:

- **Model only what you use.** Leave out optional fields you don't send. urlmetrics leaves out
  `distributions` and the history lists, each of which adds quota rows.
- **Optional request fields** use `omitempty`. If false or zero must still be sent, use a pointer, like
  `Normalize *bool` in fishtts.
- **Response fields that may be null** are pointers, so that null stays apart from zero. Use
  `treg.Optional[T]` when you must also tell an absent key from an explicit null.
- **Unknown response fields are ignored** by `encoding/json`, and `CallJSON` applies no strict check to
  your types, so a field Treg or the provider adds later breaks nothing.
- **Check the descriptor's documented limits locally**, before anything is sent, and return your own
  sentinel error. When you add a limit of your own, such as `fishtts.MaxTextBytes` (a guard against a
  large bill on a per-byte price), document it as yours, not the provider's.

## Building the request

- **Target.** Use `treg.CatalogEndpoint(id)`. The id is never checked against a catalog snapshot, so any
  well-formed id works the day Treg adds it. It is a `treg.EndpointID`, the type every model uses for an
  endpoint id (`Endpoint.ID`, `SupersededBy`, `Poll.Endpoint`, ...), so an id you read passes straight on;
  a constant needs no conversion.
- **Method.** Use the descriptor's `Method`, as an `http.Method*` constant.
- **Path parameters** ride in the query. For a path such as `/model/{id}`, send
  `Query: url.Values{"id": {voiceID}}`; Treg fills in the placeholder.
- **Query parameters** go in `Query`, encoded with `url.Values.Encode`, so a repeated key is sent as
  `k=a&k=b`. If the descriptor sets `queryArrayEncoding: "json"`, send the array as one value holding a
  JSON array literal instead: marshal the slice with `encoding/json` and send
  `url.Values{"ad_reached_countries": {string(b)}}`, which is `["US","CA"]` on the wire.
- **Body.** Use `JSONBody` for `bodyType: "json"`, `FormBody` for `form`, `BytesBody` with a
  `mime/multipart` body for `multipart`, and `ReaderBody` for a large upload of known size. The body
  constructor sets `Content-Type`.
- **Provider headers** go in `CallRequest.Header`, such as `http.Header{"Model": {"s2.1-pro"}}` for Fish
  TTS. Framing, routing, cookie, authorization, idempotency and `X-Treg-*` names, and the SDK-owned
  `Content-Type`, `Content-Encoding`, `Accept-Encoding` and `User-Agent`, are refused with a
  `*treg.ValidationError` (reason `reserved_header`) before anything is sent. A few descriptors declare
  an upstream `Idempotency-Key`: you cannot send that through `Header`, because `Controls.IdempotencyKey`
  is Treg's own control.
- **Provider keys** go in `ProviderAuthorization`, which Treg relays as the upstream `Authorization`
  header. Never put the Treg token there: the SDK refuses a request whose target, query, headers,
  controls or in-memory body carry it (reason `credential_in_request`; tokens shorter than 16 bytes and
  `ReaderBody` contents are not scanned).
- **Controls** are the caller's. Take a `treg.Controls` parameter and pass it through unchanged. Never set
  a default `MaxCost`, never generate or reuse an `IdempotencyKey`, and never add `Tags`: the caller
  owns the budget, the recovery key and the attribution. Remember that an idempotency key does not make
  a retry free for inline generative media such as speech: Treg regenerates and bills it again.

## Choosing the method

| The answer is | Use | Then |
|---|---|---|
| JSON you want typed | `CallJSON[T]` | A non-2xx is already an error. |
| Binary, large, or server-sent events | `Client.Call` | `defer resp.Close()`, check `resp.StatusError(ctx)`, then read. |
| Small, any content, and you want the status and raw bytes | `Client.CallBytes` | Check `raw.StatusError()`, then use `raw.Body()`. |

`CallJSON` and `CallBytes` read the whole body within `WithMaxResponseBytes` and `WithTimeout`. `Call`
returns a stream whose reads stop at `WithMaxStreamBytes` and whose life ends at `WithStreamTimeout`. All
three make exactly one attempt and never follow a redirect. `CallJSON` accepts `IgnoreContentType()` for a
provider that sends JSON under another media type, `UseNumber()`, and `CaptureResponse(true)` to keep the
exact bytes for diagnosis.

The streaming shape, as in fishtts:

```go
resp, err := c.Call(ctx, treg.CallRequest{
	Target:   treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"),
	Method:   http.MethodPost,
	Header:   http.Header{"Model": {"s2.1-pro"}},
	Body:     treg.JSONBody(in),
	Controls: ctl,
})
if err != nil {
	return Result{}, err // a *treg.ValidationError (nothing sent) or a *treg.CallError
}
defer func() { _ = resp.Close() }() // ends the request and its timers
res := Result{Meta: resp.Meta()}
if err = resp.StatusError(ctx); err != nil {
	return res, err // a *treg.CallError for a 4xx or 5xx, with the Refusal parsed
}
// Check resp.Header().Get("Content-Type") before writing anything, then copy.
```

Check the media type before you write to the caller's destination: a provider can answer 200 with a JSON
error. fishtts refuses anything that is neither `audio/*` nor `application/octet-stream` with `ErrNotAudio`.

## Errors

Return the SDK's errors as they are, or wrap them with `%w`, so that `errors.Is` and `errors.As` keep
working for your callers.

| Error | Meaning | What to do |
|---|---|---|
| `*treg.ValidationError` (`ErrInvalidRequest`, `ErrInvalidConfig`) | A local check failed. `Field` and `Reason` name it, never the value. | Nothing was sent. Fix the input. |
| `*treg.CallError` with `Kind` `http_status` | A 4xx or 5xx. `StatusCode`, `Refusal` and `Meta` say more. | Branch on `Refusal.Code`, such as `insufficient_balance` with `BalanceMicro` and `EstimatedCostMicro`. |
| ... and `errors.Is(err, treg.ErrGatewayRefusal)` | Treg marked the answer as its own refusal (`X-Treg-Error: 1`, so `GatewayMarked` is true) or reported saturation. | Read `Refusal.Code`. The marker is evidence, not proof, and a 5xx can match `ErrOutcomeUnknown` too. |
| ... and `errors.Is(err, treg.ErrOutcomeUnknown)` | The call may have executed and been billed: a 5xx that is not a proven pre-relay refusal, or a failure after the request was sent. | Reconcile before repeating it (below). |
| A `*treg.CallError` after a 2xx (such as `ErrMalformedResponse`, `ErrResponseTooLarge` or a failed stream read) | The call executed. `Meta` keeps the call ref and cost. | Do not resubmit it. |

To reconcile an unknown outcome, look the call up by its ref:

```go
if ce, ok := errors.AsType[*treg.CallError](err); ok && errors.Is(err, treg.ErrOutcomeUnknown) {
	if ce.Meta.CallRef != "" {
		detail, gerr := client.Calls().Get(ctx, treg.GetCallRequest{Ref: ce.Meta.CallRef})
		if gerr == nil {
			charged, settled := detail.Value.ChargedMicro.Get()
			fmt.Println(detail.Value.Call, charged, settled) // what happened, and what the ledger settled
		}
	}
	// Without a ref (no response arrived), look through client.Calls().List.
}
```

Repeat a call only after reconciling. If it carried an `IdempotencyKey`, repeating the exact same request
with the same key lets Treg answer from the stored result instead of calling the provider again. Keys last
24 hours, and inline generative media is regenerated and billed again. The SDK never retries, and your
package shouldn't either.

Your package's own errors fall into two groups, and its documentation should say which is which:

- **Before sending**, such as `urlmetrics.ErrTargets` and `fishtts.ErrText`: nothing was sent.
- **After a 2xx**, such as `fishtts.ErrNotAudio` and `fishtts.ErrWrite` (the caller's writer failed
  mid-stream): the call executed and may have been billed. Return the response's `Meta` with the error, so
  the caller keeps the evidence.

## Money

- Amounts are `treg.MicroUSD`, integer millionths of a dollar. Format them with `DecimalUSD` or `String`;
  the SDK never converts them to a float. Catalog prices such as `Cost.USD` are `treg.Decimal` display
  text, a list price rather than a quote. `Client.EndpointAccess` gives an estimate, which is a preview,
  not a reservation.
- `Metadata.CostMicro` is `X-Treg-Cost-Micro` as reported. `nil` means unknown, never zero: the call ran
  on the team's own key, or Treg refused it before reserving anything. On a replay
  (`Metadata.Replayed`), `OriginalCostMicro` says what the original call was charged.
- When `Metadata.Async` is set, `CostMicro` is the reservation, not the charge; the terminal task
  settles the real charge.
- `Controls.MaxCost` is the reserve ceiling Treg enforces on metered and routed calls. A call over it is
  refused with a 402 and `Refusal.Code` `route_max_cost`.
- Reconcile money through `Client.Calls().Get` (`CallDetail.ChargedMicro` and `Ledger`), not from
  response headers.

## Routed ids

A routed id, such as `treg.web.search`, answers with an envelope. Decode it with your own output type:

```go
res, err := treg.CallJSON[treg.Routed[MyOutput]](ctx, client, req)
```

`res.Value.Treg.Outcome` is `RouteOutcomeHit`, `RouteOutcomeWeak`, `RouteOutcomeMiss` or
`RouteOutcomePending`, an open set (`Known` reports a listed value). A miss may still be charged. A pending
outcome arrives as HTTP 202 and counts as a success: keep `Treg.CallRef` and `Treg.Async`, never
resubmit, and check on it later with your own explicit call. `Treg.ChargedMicro` is null while the call
is pending, which means unknown, not zero. The SDK never substitutes a routed id, picks a provider or
falls back, and your package shouldn't either: choosing between a direct id, its siblings and a routed
id is the caller's decision.

## Async descriptors

An async endpoint answers with a task, and `Metadata.Async` (the `X-Treg-Async` header) says how to
follow it. It is data the SDK never acts on. If your package follows it, do so with explicit calls:

- `Poll.Endpoint` plus `Poll.Param` names a catalog endpoint and the parameter that takes the task id.
  Target it with `treg.CatalogEndpoint(d.Poll.Endpoint)`, which validates it like any id. Path parameters
  ride in the query: `Query: url.Values{d.Poll.Param.Name: {taskID}}`.
- Otherwise `Poll.URLFrom` names a URL in the submission response. Check that its host is in
  `Poll.URLHosts` before you call it with `treg.UpstreamURL`.
- `Status.Path` and the `Success`, `Failure`, `BilledFailure` and `Progress` lists classify the poll
  response. A value in none of the lists is not a success.
- Poll no faster than `Interval` and stop after `MaxAge`. A poll loop in a library should take an explicit
  budget from the caller and stop at the first failure.

## Testing your package

Test against a local TLS server with the public options, exactly as an outside user would:

```go
srv := httptest.NewTLSServer(handler)
t.Cleanup(srv.Close)
c, err := treg.NewClient("test-token-0123456789abcdef",
	treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client()))
```

Have the handler record each request (method, `r.URL.Path`, headers, body) under a mutex, and answer
with synthetic data: `example.com` hosts and made-up ids. Then check:

- the exact wire request: the method, the path `/call/<id>`, the body, the provider headers,
  `X-Treg-Token`, and the caller's controls (`X-Treg-Route-Max-Cost`, `Idempotency-Key`,
  `X-Treg-Meta`);
- the decoded answer, including a null field and the metadata (`X-Treg-Call-Id` as `Meta.CallRef`,
  `X-Treg-Cost-Micro` as `Meta.CostMicro`);
- zero requests for every invalid input;
- a gateway refusal (status 402, `X-Treg-Error: 1` and a `detail` body), a 5xx (`ErrOutcomeUnknown`),
  and, for streams, a failing writer and a context canceled mid-stream.

`internal/testutil` belongs to go-treg's own tests; an outside package cannot import it and does not need
it. Both example packages' `_test.go` files show the whole pattern, including an `Example` function that
runs offline.

## Drift

Treg's catalog changes underneath your package, so plan for it:

- **Record the descriptor's `verified` date** in the package (`urlmetrics.Verified`). Compare it with
  `Endpoint.Verified` when you revisit the package, and reread `Input` and `Cost` when it moves.
- **Additive changes are safe.** New response fields are ignored, and new optional request fields
  don't matter until you choose to send them.
- **Removed or renamed ids break only your wrapper.** The SDK has no list of ids, so nothing in it
  changes; your call starts failing with a `*treg.CallError` instead.
- **Tombstones.** A retired id answers 410, and its descriptor's `Status`, `StatusNote` and
  `SupersededBy` say why and what replaced it. An idempotency 410 is different: it carries an
  `idempotency_*` `Refusal.Code`. The SDK never follows `SupersededBy`; moving to the replacement is a
  change you make, review and test in your package.
