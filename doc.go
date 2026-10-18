// Package treg is an independent, unofficial Go client for the Treg API
// gateway (https://treg.to). It is not affiliated with Treg or Superdesign.
//
// Treg puts thousands of catalog endpoints behind one proxy route,
// /call/{rest}, next to its own platform routes. This package gives you a
// typed core for Treg's own concepts, an open execution seam that calls any
// valid catalog id without an SDK release, and caller-owned request and
// result types, so your own packages can add typed integrations without a
// fork. The runtime depends on the standard library only.
//
//	client, err := treg.NewClient(token) // add treg.WithOrg(slug) for an unpinned identity token
//	if err != nil {
//		return err
//	}
//	res, err := treg.CallJSON[myOutput](ctx, client, treg.CallRequest{
//		Target:   treg.CatalogEndpoint("moz.web.url.metrics"),
//		Method:   http.MethodPost,
//		Body:     treg.JSONBody(myInput{Targets: []string{"example.com"}}),
//		Controls: treg.Controls{MaxCost: new(treg.MicroUSD(20_000))}, // $0.02 reserve ceiling
//	})
//
// # Discovery and execution
//
// Discovery never executes. CatalogClient (from NewCatalogClient or
// Client.Catalog) reads the public catalog and /meta without a token:
// Search, Find, Platforms, Platform, Provider, Endpoint, Example and
// ServerInfo. Execution is separate and explicit, through a Client:
// Client.Call (streaming), Client.CallBytes (buffered raw) and the function
// CallJSON (decoded into your own type). The SDK never picks, ranks,
// substitutes or runs an endpoint for you.
//
// Each call makes exactly one attempt. The SDK never retries, follows a
// redirect, polls, falls back to another provider or invents an
// idempotency key, and it builds every request so that net/http cannot
// resend it either, with one documented residual: an empty POST, PUT or
// PATCH (no body) is sent as http.NoBody, which HTTP/2 may resend after
// GOAWAY, REFUSED_STREAM or, on Go 1.26, a peer PROTOCOL_ERROR.
//
// # Targets
//
// A Target is the {rest} of /call/{rest}: CatalogEndpoint (a direct or
// routed catalog id, never looked up locally), RegisteredTool (an org tool
// and an upstream path, sent byte for byte) or UpstreamURL (the full URL of
// a registered tool). Treg tries an org tool or URL passthrough before a
// catalog id, so an org tool with the same name shadows a catalog id.
// CatalogEndpoint takes an EndpointID, the type every model uses for an
// endpoint id, so an id read from the catalog passes straight on.
//
// # Requests
//
// CallRequest carries the method, the query (Query, encoded with sorted
// keys, or RawQuery, sent byte for byte to Treg), upstream headers,
// ProviderAuthorization (the only way to send an upstream Authorization
// header), a Body and Controls. Bodies are JSONBody, BytesBody, FormBody
// (all encoded or copied before dispatch, within WithMaxRequestBytes) and
// ReaderBody (exactly size bytes, never closed by the SDK). Framing,
// routing, cookie, authorization, idempotency, X-Treg-* and SDK-owned header
// names are refused; every header value is checked more strictly than
// net/http checks it, so nothing fails after dispatch. A request that holds the token
// in any caller string or in-memory body is refused before anything is
// sent (tokens of 16 bytes or more; ReaderBody contents are not scanned).
//
// # Controls
//
// Controls are written only from the fields you set: IdempotencyKey,
// MaxCost (X-Treg-Route-Max-Cost, the reserve ceiling Treg enforces), Tags
// (X-Treg-Meta), MaxAge, NoCache, Route (RouteControls) and Extra for
// X-Treg-* controls added after this release. Invalid values are refused, never
// truncated, and Treg decides where a control applies.
//
// # Responses and status
//
//	Received   Call        CallBytes                   CallJSON and typed reads
//	2xx        *Response   *RawResponse                decoded; HEAD refused locally; empty body is an error
//	3xx        *Response   *RawResponse, body unread   error (unexpected_response)
//	4xx/5xx    *Response   *RawResponse                error (http_status) with the Refusal parsed
//
// A 1xx final response is an error (unexpected_response) for every entry
// point. Check a Response or RawResponse with StatusError. A provider failure
// inside a 2xx is provider data, not an error; X-Treg-Upstream-Failed sets
// Metadata.UpstreamFailed. A routed 202 (pending) is a success.
//
// # Errors
//
// A *ValidationError (ErrInvalidConfig or ErrInvalidRequest) means nothing
// was sent; it names a field and a fixed reason, never a value. Every other
// failure is a *CallError: Kind matches a sentinel (ErrHTTPStatus,
// ErrUnexpectedResponse, ErrMalformedResponse, ErrResponseTooLarge,
// ErrTransport, ErrTimeout) or context.Canceled/DeadlineExceeded; Reason
// refines it; Refusal holds a 4xx/5xx body's machine fields. ErrTimeout also
// matches context.DeadlineExceeded, so check it first. ErrGatewayRefusal
// matches a refusal Treg marked (X-Treg-Error: 1) or a saturation 503.
//
// ErrOutcomeUnknown matches a /call/ failure after which the request may
// have executed and been billed: a failure after the request headers were
// written (always, on an injected transport), or a 5xx that is not a proven
// pre-relay refusal. Reconcile with Calls().Get before repeating. A 2xx that
// failed to read or decode executed too: do not resubmit it.
//
// # Money and metadata
//
// Amounts are integer MicroUSD (1 USD = 1,000,000), never floats; display
// USD values decode as Decimal text. Metadata is what Treg's headers say
// about one request: the call ref, the cost as reported (nil means
// unknown, not zero; on an async submission it is the reservation), replay
// and cache facts, the async descriptor and Retry-After. It is evidence as
// received: an upstream reached on its own key can send X-Treg-* headers
// too, so reconcile money through Calls().Get.
//
// # Routed calls, async and idempotency
//
// Decode a routed id's envelope with CallJSON[Routed[O]]: Output is yours,
// Raw is the provider document byte for byte, and Treg is the _treg block.
// AsyncDescriptor (Metadata.Async) says how to follow an asynchronous
// submission; following it is your own explicit code. An idempotency key is
// yours: Treg keeps it 24 hours, answers 409 while the first call runs, 422
// for a different request and 410 when it lost the stored answer.
//
// # Limits, timeouts and transport
//
// Buffered calls are bounded by WithTimeout (default 240 s, including the
// body read) and WithMaxResponseBytes (16 MiB after decompression; typed
// reads at most 8 MiB). Streams are bounded by WithStreamTimeout (30 min,
// dispatch to Close), a header wait of min(Timeout, StreamTimeout) and
// WithMaxStreamBytes (1 GiB). The SDK's transport speaks HTTP/2 when
// offered, never reads proxy environment variables, keeps no cookies and
// refuses redirects. WithHTTPClient injects a client: it is copied, never
// modified, and its transport is trusted.
//
// # Privacy
//
// The token lives only inside function values. Errors, printed clients,
// requests, bodies, responses, captures and metadata never contain the
// token, URLs, queries, header values, bodies or provider text; refusal
// codes print only when Refusal.Known. Response.Header and
// RawResponse.Header return every header and are sensitive; so are
// Capture.Bytes and CallError.Body (WithErrorBodies). Models that hold
// personal data (CallRecord, StoredResult, Identity, Tool, UserUsage,
// ProviderResource) print their shape only through fmt and encoding/json.
// Exported data fields still print raw when you hold such a value in an
// unexported struct field, format a non-pointer with %p, or walk it by
// reflection; no Go type can prevent that.
//
// # Concurrency and extension
//
// A Client is immutable and safe for concurrent use; there is no global
// state, no init and no background goroutine beyond the ones that close a
// body when its context ends or cancel a stream's overdue header wait. A
// typed integration is an ordinary package of functions over CallJSON,
// CallBytes or Call: see examples/extension and docs/integration-guide.md.
package treg
