package treg_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// Raw islands of the catalog fixtures. Each island sits on one line of its
// fixture, so the decoded bytes must equal it exactly.
const (
	cloneInput       = `{"sample_url":  {"type": "url", "required": true}, "name": {"type": "string", "max": 64}}`
	cloneTestRequest = `{"body": {"sample_url": "https://example.com/sample.wav", "name": "Synthetic"}}`
	cloneCost        = `{"type": "per_call", "value": 0.006667, "currency": "USD", "per": 1, "unit": "request", ` +
		`"usd": 0.006667, "usd_min": 0.005, "source": "provider_page", "source_url": "https://acmevoice.example.com/pricing", ` +
		`"checked": "2026-09-15", "confidence": "medium", "note": "Synthetic list price.", "varies": false, ` +
		`"table": [{"tier": "standard", "usd": 0.006667}], "settle": {"on": "success"}}`
	cloneAsync = `{"id_from": "id", "poll": {"endpoint": "acmevoice.jobs.get", "param": {"in": "pathParams", "name": "id"}}, ` +
		`"status": {"path": "status", "success": ["succeeded"], "failure": ["failed", "canceled"]}, ` +
		`"result": {"path": "output"}, "interval": 2}`
	cloneObserved = `{"p50_ms": 840, "samples": 12,  "window": "7d"}`
	cloneReviews  = `[{"stars": 4, "text": "Synthetic review."}]`

	// minimalSearch is the smallest search answer the model accepts.
	minimalSearch = `{"query":"","count":0,"total":0,"results":[],"hints":[]}`
)

// readCall runs one typed read with the clients a test built: the public
// reads use cat, Client.EndpointAccess uses c.
type readCall func(ctx context.Context, cat *treg.CatalogClient, c *treg.Client, opts ...treg.CallOption) (
	treg.Metadata, treg.Capture, error)

// catalogRead returns the readCall of r, a read of the CatalogClient.
func catalogRead[T any](r typedRead[*treg.CatalogClient, T]) readCall {
	return func(ctx context.Context, cat *treg.CatalogClient, _ *treg.Client, opts ...treg.CallOption) (
		treg.Metadata, treg.Capture, error,
	) {
		return outcome(r(cat, ctx, opts...))
	}
}

// clientRead returns the readCall of r, a read of the Client.
func clientRead[T any](r typedRead[*treg.Client, T]) readCall {
	return func(ctx context.Context, _ *treg.CatalogClient, c *treg.Client, opts ...treg.CallOption) (
		treg.Metadata, treg.Capture, error,
	) {
		return outcome(r(c, ctx, opts...))
	}
}

// outcome reduces a typed read's result to its metadata and capture.
func outcome[T any](res *treg.Result[T], err error) (treg.Metadata, treg.Capture, error) {
	if err != nil {
		return treg.Metadata{}, treg.Capture{}, err
	}
	return res.Meta, res.Captured, nil
}

// readCase is one typed catalog read and what its request looks like.
type readCase struct {
	name    string
	op      treg.Operation
	path    string
	query   string
	fixture string
	authed  bool
	call    readCall
}

// readCases returns every typed catalog read, each with a full request.
func readCases() []readCase {
	return []readCase{
		{
			name: "ServerInfo", op: treg.OpServerInfo, path: "/meta", fixture: "catalog/meta.json",
			call: catalogRead(bind((*treg.CatalogClient).ServerInfo, treg.ServerInfoRequest{})),
		},
		{
			name: "Platforms", op: treg.OpCatalogPlatforms, path: "/catalog/platforms", fixture: "catalog/platforms.json",
			call: catalogRead(bind((*treg.CatalogClient).Platforms, treg.PlatformsRequest{})),
		},
		{
			name: "Platform", op: treg.OpCatalogPlatform, path: "/catalog/platforms/voice-gen", query: "include_hidden=1",
			fixture: "catalog/platform.json",
			call:    catalogRead(bind((*treg.CatalogClient).Platform, treg.PlatformRequest{Slug: "voice-gen", IncludeHidden: true})),
		},
		{
			name: "Provider", op: treg.OpCatalogProvider, path: "/catalog/providers/acmevoice", fixture: "catalog/provider.json",
			call: catalogRead(bind((*treg.CatalogClient).Provider, treg.ProviderRequest{Service: "acmevoice"})),
		},
		{
			name: "Search", op: treg.OpCatalogSearch, path: "/catalog/search",
			query: "added_within_days=30&limit=5&q=clone+a+voice&sort=best", fixture: "catalog/search.json",
			call: catalogRead(bind((*treg.CatalogClient).Search,
				treg.SearchRequest{Query: "clone a voice", Limit: 5, AddedWithinDays: 30, Sort: treg.SearchSortBest})),
		},
		{
			name: "Endpoint", op: treg.OpCatalogEndpoint, path: "/catalog/endpoints/" + cloneID, fixture: "catalog/endpoint.json",
			call: catalogRead(bind((*treg.CatalogClient).Endpoint, treg.EndpointRequest{ID: cloneID})),
		},
		{
			name: "Example", op: treg.OpCatalogExample, path: "/catalog/examples/" + cloneID, fixture: "catalog/example.json",
			call: catalogRead(bind((*treg.CatalogClient).Example, treg.ExampleRequest{ID: cloneID})),
		},
		{
			name: "EndpointAccess", op: treg.OpEndpointAccess, path: "/catalog/endpoints/" + cloneID + "/access",
			query: "authorization_method=oauth_pkce", fixture: "catalog/access.json", authed: true,
			call: clientRead(bind((*treg.Client).EndpointAccess,
				treg.EndpointAccessRequest{ID: cloneID, AuthorizationMethod: "oauth_pkce"})),
		},
	}
}

// scriptClients returns a CatalogClient and a Client that share script.
func scriptClients(t *testing.T, script *testutil.Script, opts ...treg.Option) (*treg.CatalogClient, *treg.Client) {
	t.Helper()
	return newCatalog(t, script, opts...), newClient(t, script, opts...)
}

// runRead runs tc once against a Script answering with step and checks that
// exactly one request went out.
func runRead(t *testing.T, tc readCase, step testutil.Step, opts []treg.Option, callOpts ...treg.CallOption) (
	treg.Metadata, treg.Capture, error,
) {
	t.Helper()
	script := testutil.NewScript(step)
	cat, c := scriptClients(t, script, opts...)
	meta, captured, err := tc.call(t.Context(), cat, c, callOpts...)
	assert.Equal(t, 1, script.Calls(), "exactly one request")
	return meta, captured, err
}

// raws returns its arguments as raw JSON values.
func raws(values ...string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(values))
	for _, v := range values {
		out = append(out, json.RawMessage(v))
	}
	return out
}

// assertCloneRow checks the full synthetic row acmevoice.voice.clone.
func assertCloneRow(t *testing.T, fixture string, ep treg.Endpoint) {
	t.Helper()
	for _, island := range []string{cloneInput, cloneTestRequest, cloneCost, cloneAsync, cloneObserved, cloneReviews} {
		require.Contains(t, fixture, island, "the fixture holds the island verbatim")
	}
	assert.Equal(t, treg.EndpointID(cloneID), ep.ID)
	assert.Equal(t, "acmevoice", ep.Provider)
	assert.Equal(t, "Acme Voice", ep.ProviderDisplay)
	assert.Equal(t, "Clone a voice", ep.Name)
	assert.Equal(t, "Creates a synthetic voice from a short sample.", ep.Summary)
	assert.Equal(t, http.MethodPost, ep.Method)
	assert.Equal(t, "/v1/voices/clone", ep.Path)
	assert.Equal(t, "own_account", ep.Scope)
	assert.Equal(t, "core", ep.Tier)
	assert.Equal(t, treg.EndpointKindAction, ep.Kind)
	assert.True(t, ep.Kind.Known())
	assert.Equal(t, "voice", ep.Domain)
	assert.Equal(t, "treg call acmevoice.voice.clone", ep.CallTemplate)
	assertSameBytes(t, cloneInput, ep.Input)
	assertSameBytes(t, cloneTestRequest, ep.TestRequest)
	assert.True(t, ep.StrictQuery)
	assert.True(t, ep.StrictBody)
	assert.True(t, ep.BodyAllowlist)
	assertCloneCost(t, ep.Cost)
	assertCloneAsync(t, ep.Async)
	assert.Equal(t, treg.Some(true), ep.PlatformEligible, "platform_eligible")
	assert.Equal(t, treg.Some("shared_key"), ep.PlatformAuth, "platform_auth")
	assert.Equal(t, treg.Null[string](), ep.PlatformBlocked, "platform_blocked")
	assert.Equal(t, treg.Some(treg.EndpointOutcome{Status: 404, Means: "The voice sample was not found."}), ep.Miss, "miss")
	assert.Equal(t, treg.Some(treg.EndpointOutcome{Status: 410, Means: "The voice was deleted."}), ep.NotFound, "not_found")
	assert.Equal(t, treg.Null[string](), ep.Status, "status")
	assert.Equal(t, treg.Null[string](), ep.StatusNote, "status_note")
	assert.Equal(t, treg.Null[treg.EndpointID](), ep.SupersededBy, "superseded_by")
	assert.Equal(t, treg.Some("2026-09-20"), ep.Verified, "verified")
	assert.Equal(t, treg.Some("2026-01-05"), ep.Added, "added")
	assert.Equal(t, "https://acmevoice.example.com/docs/clone", ep.DocsURL)
	assert.True(t, ep.HasExample)
	assertSameBytes(t, cloneObserved, ep.Observed)
	assertSameBytes(t, cloneReviews, ep.Reviews)
	assert.Nil(t, ep.RoutedChildren)
	assert.Nil(t, ep.ChildrenHidden)
	assert.Nil(t, ep.AuthorizationMethod)
	assert.Equal(t, treg.Optional[string]{}, ep.Capability, "capability")
	assert.Equal(t, treg.Optional[string]{}, ep.Platform, "platform")
	assert.Equal(t, treg.Optional[treg.Decimal]{}, ep.Score, "score")
}

// assertCloneCost checks the cost block of acmevoice.voice.clone.
func assertCloneCost(t *testing.T, oc treg.Optional[treg.Cost]) {
	t.Helper()
	cost, ok := oc.Get()
	require.True(t, ok, "cost holds a value")
	assert.Equal(t, treg.CostPerCall, cost.Type)
	assert.True(t, cost.Type.Known())
	assert.Equal(t, treg.Some(treg.Decimal("0.006667")), cost.Value, "cost.value")
	assert.Equal(t, treg.Some("USD"), cost.Currency, "cost.currency")
	assert.Equal(t, treg.Some(treg.Decimal("1")), cost.Per, "cost.per")
	assert.Equal(t, treg.Some("request"), cost.Unit, "cost.unit")
	assert.Equal(t, treg.Some(treg.Decimal("0.006667")), cost.USD, "cost.usd")
	assert.Equal(t, treg.Some(treg.Decimal("0.005")), cost.USDMin, "cost.usd_min")
	assert.Equal(t, treg.Some("provider_page"), cost.Source, "cost.source")
	assert.Equal(t, treg.Some("https://acmevoice.example.com/pricing"), cost.SourceURL, "cost.source_url")
	assert.Equal(t, treg.Some("2026-09-15"), cost.Checked, "cost.checked")
	assert.Equal(t, treg.Some("medium"), cost.Confidence, "cost.confidence")
	assert.Equal(t, treg.Some("Synthetic list price."), cost.Note, "cost.note")
	assert.Equal(t, treg.Some(false), cost.Varies, "cost.varies")
	assertSameBytes(t, cloneCost, cost.Raw, "Raw keeps the whole block, extra keys included")
}

// assertCloneAsync checks the async descriptor of acmevoice.voice.clone.
func assertCloneAsync(t *testing.T, oa treg.Optional[treg.AsyncDescriptor]) {
	t.Helper()
	d, ok := oa.Get()
	require.True(t, ok, "async holds a value")
	assert.Equal(t, "id", d.IDFrom)
	assert.Empty(t, d.TaskID)
	assert.Equal(t, treg.EndpointID("acmevoice.jobs.get"), d.Poll.Endpoint)
	require.NotNil(t, d.Poll.Param)
	assert.Equal(t, treg.AsyncParam{In: "pathParams", Name: "id"}, *d.Poll.Param)
	assert.Empty(t, d.Poll.URLFrom)
	assert.Equal(t, "status", d.Status.Path)
	assert.Equal(t, raws(`"succeeded"`), d.Status.Success)
	assert.Equal(t, raws(`"failed"`, `"canceled"`), d.Status.Failure)
	assert.Empty(t, d.Status.BilledFailure)
	assert.Equal(t, "output", d.Result.Path)
	assert.Nil(t, d.Result.FetchParam)
	assert.Equal(t, treg.Decimal("2"), d.Interval)
	assert.Equal(t, treg.Optional[int64]{}, d.MaxAge, "async.max_age")
	assert.False(t, d.TerminalOnSubmission)
	assertSameBytes(t, cloneAsync, d.Raw)
}

// assertLegacyRow checks the retired synthetic row examplespeech.tts.legacy,
// whose nullable members are all null.
func assertLegacyRow(t *testing.T, ep treg.Endpoint) {
	t.Helper()
	assert.Equal(t, treg.EndpointID("examplespeech.tts.legacy"), ep.ID)
	assert.Equal(t, http.MethodGet, ep.Method)
	assert.Empty(t, ep.Scope)
	assert.Equal(t, "extended", ep.Tier)
	assert.Equal(t, treg.EndpointKindData, ep.Kind)
	assertSameBytes(t, "null", ep.Input)
	assertSameBytes(t, "null", ep.TestRequest)
	assert.False(t, ep.StrictQuery)
	assert.False(t, ep.StrictBody)
	assert.False(t, ep.BodyAllowlist)
	assert.Equal(t, treg.Null[treg.Cost](), ep.Cost, "cost")
	assert.Equal(t, treg.Null[treg.AsyncDescriptor](), ep.Async, "async")
	assert.Equal(t, treg.Null[bool](), ep.PlatformEligible, "platform_eligible")
	assert.Equal(t, treg.Optional[string]{}, ep.PlatformAuth, "platform_auth")
	assert.Equal(t, treg.Some("provider_terms"), ep.PlatformBlocked, "platform_blocked")
	assert.Equal(t, treg.Null[treg.EndpointOutcome](), ep.Miss, "miss")
	assert.Equal(t, treg.Optional[treg.EndpointOutcome]{}, ep.NotFound, "not_found")
	assert.Equal(t, treg.Some("retired"), ep.Status, "status")
	assert.Equal(t, treg.Some("Replaced by a newer synthetic row."), ep.StatusNote, "status_note")
	assert.Equal(t, treg.Some[treg.EndpointID]("examplespeech.tts.create"), ep.SupersededBy, "superseded_by")
	assert.Equal(t, treg.Null[string](), ep.Verified, "verified")
	assert.Equal(t, treg.Null[string](), ep.Added, "added")
	assert.Empty(t, ep.DocsURL)
	assert.False(t, ep.HasExample)
	assert.Nil(t, ep.Observed)
	assert.Nil(t, ep.Reviews)
}

func TestCatalogReadsWire(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The public reads go out from a standalone CatalogClient and from
			// the Catalog of a Client that has a token and an org.
			for _, from := range []struct {
				name       string
				standalone bool
			}{
				{"standalone catalog", true},
				{"catalog of a client", false},
			} {
				t.Run(from.name, func(t *testing.T) {
					t.Parallel()
					srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: synthetic(t, tc.fixture)}))
					c := newClient(t, srv, treg.WithOrg(testOrg))
					cat := c.Catalog()
					if from.standalone {
						cat = newCatalog(t, srv)
					}
					meta, _, err := tc.call(t.Context(), cat, c)
					require.NoError(t, err)
					assert.Equal(t, tc.op, meta.Op)
					assert.Equal(t, http.StatusOK, meta.StatusCode)
					assertReadWire(t, tc, testutil.OnlyRequest(t, srv))
				})
			}
		})
	}
}

// assertReadWire checks the one GET of a typed read.
func assertReadWire(t *testing.T, tc readCase, r testutil.Request) {
	t.Helper()
	assert.Equal(t, http.MethodGet, r.Method)
	assert.Equal(t, tc.path, r.Path)
	assert.Equal(t, tc.query, r.RawQuery)
	assert.Equal(t, "application/json", r.Header.Get("Accept"))
	assert.Empty(t, r.Body)
	assert.Empty(t, r.Header.Get("Authorization"))
	assert.Empty(t, r.Header.Get("Cookie"))
	if tc.authed {
		assert.Equal(t, testToken, r.Header.Get("X-Treg-Token"))
		assert.Equal(t, testOrg, r.Header.Get("X-Treg-Org"))
		return
	}
	assert.NotContains(t, r.Header, "X-Treg-Token")
	assert.NotContains(t, r.Header, "X-Treg-Org")
	for name, values := range r.Header {
		for _, v := range values {
			assert.NotContains(t, v, testToken, "header %s", name)
			assert.NotContains(t, v, testOrg, "header %s", name)
		}
	}
	assert.NotContains(t, r.URL, testToken)
}

func TestCatalogReadsQueryVariants(t *testing.T) {
	t.Parallel()
	platformBody := `{"platform":{"slug":"voice-gen","label":"","category":""},"capabilities":[],` +
		`"domains":[],"extended":[],"hidden_count":0,"providers":{}}`
	for _, v := range []struct {
		name, path, query, body string
		call                    readCall
	}{
		{
			"platform_without_hidden", "/catalog/platforms/voice-gen", "", platformBody,
			catalogRead(bind((*treg.CatalogClient).Platform, treg.PlatformRequest{Slug: "voice-gen"})),
		},
		{
			"search_empty_query", "/catalog/search", "q=", minimalSearch,
			catalogRead(bind((*treg.CatalogClient).Search, treg.SearchRequest{})),
		},
		{
			"search_escaped_query", "/catalog/search", "q=a+b%26c%3Dd%2Fe%25", minimalSearch,
			catalogRead(bind((*treg.CatalogClient).Search, treg.SearchRequest{Query: "a b&c=d/e%"})),
		},
		{
			"search_newest", "/catalog/search", "limit=100&q=tts&sort=newest", minimalSearch,
			catalogRead(bind((*treg.CatalogClient).Search, treg.SearchRequest{Query: "tts", Limit: 100, Sort: treg.SearchSortNewest})),
		},
		{
			"access_without_method", "/catalog/endpoints/" + cloneID + "/access", "", `{"tier":"none"}`,
			clientRead(bind((*treg.Client).EndpointAccess, treg.EndpointAccessRequest{ID: cloneID})),
		},
		{
			"endpoint_unreserved_id", "/catalog/endpoints/acme-voice.tts_v2~beta", "",
			`{"endpoint":{},"provider":{},"siblings":[],"call_template":"","example_response":null,"hints":[]}`,
			catalogRead(bind((*treg.CatalogClient).Endpoint, treg.EndpointRequest{ID: "acme-voice.tts_v2~beta"})),
		},
	} {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: v.body})
			cat, c := scriptClients(t, script)
			_, _, err := v.call(t.Context(), cat, c)
			require.NoError(t, err)
			r := testutil.OnlyRequest(t, script)
			assert.Equal(t, v.path, r.Path)
			assert.Equal(t, v.query, r.RawQuery)
			assert.Equal(t, http.MethodGet, r.Method)
		})
	}
}

// failureCase is a 2xx-or-not answer that a typed read must refuse.
type failureCase struct {
	name    string
	step    func(fixture string) testutil.Step
	kind    treg.FailureKind
	reason  string
	status  int
	refusal string
	marked  bool
}

// failureCases returns the answers every typed read refuses.
func failureCases() []failureCase {
	html := http.Header{"Content-Type": {"text/html; charset=utf-8"}}
	return []failureCase{
		{
			name: "not_found_string_detail", kind: treg.FailureHTTPStatus, status: http.StatusNotFound,
			step: func(string) testutil.Step {
				return testutil.Step{Status: http.StatusNotFound, Body: `{"detail":"unknown platform 'nope'"}`}
			},
		},
		{
			name: "not_found_prose_error", kind: treg.FailureHTTPStatus, status: http.StatusNotFound,
			step: func(string) testutil.Step {
				return testutil.Step{
					Status: http.StatusNotFound,
					Body:   `{"detail":{"error":"unknown endpoint 'nope.x'","hint":"search the catalog","did_you_mean":[]}}`,
				}
			},
		},
		{
			name: "not_found_gateway_marked", kind: treg.FailureHTTPStatus, status: http.StatusNotFound,
			marked: true, step: func(string) testutil.Step {
				return testutil.Step{Status: http.StatusNotFound, Header: gatewayHeader(), Body: `{"detail":"unknown platform 'nope'"}`}
			},
		},
		{
			name: "provider_paused", kind: treg.FailureHTTPStatus, status: http.StatusServiceUnavailable,
			refusal: "provider_paused", step: func(string) testutil.Step {
				return testutil.Step{
					Status: http.StatusServiceUnavailable,
					Body:   `{"detail":{"error":"provider_paused","message":"Paused for synthetic maintenance."}}`,
				}
			},
		},
		{
			name: "server_error", kind: treg.FailureHTTPStatus, status: http.StatusInternalServerError,
			step: func(string) testutil.Step {
				return testutil.Step{Status: http.StatusInternalServerError, Body: `{"detail":"Internal Server Error"}`}
			},
		},
		{
			name: "bad_gateway_html", kind: treg.FailureHTTPStatus, status: http.StatusBadGateway,
			step: func(string) testutil.Step {
				return testutil.Step{Status: http.StatusBadGateway, Header: html, Body: "<html>bad gateway</html>"}
			},
		},
		{
			name: "redirect", kind: treg.FailureUnexpectedResponse, reason: "redirect", status: http.StatusFound,
			step: func(string) testutil.Step {
				return testutil.Step{Status: http.StatusFound, Header: http.Header{"Location": {"https://example.com/elsewhere"}}}
			},
		},
		{
			name: "permanent_redirect", kind: treg.FailureUnexpectedResponse, reason: "redirect",
			status: http.StatusPermanentRedirect, step: func(fixture string) testutil.Step {
				return testutil.Step{
					Status: http.StatusPermanentRedirect, Body: fixture,
					Header: http.Header{"Content-Type": {"application/json"}, "Location": {"https://example.com/elsewhere"}},
				}
			},
		},
		{
			name: "html_content_type", kind: treg.FailureUnexpectedResponse, reason: "content_type", status: http.StatusOK,
			step: func(fixture string) testutil.Step { return testutil.Step{Header: html, Body: fixture} },
		},
		{
			name: "repeated_content_type", kind: treg.FailureUnexpectedResponse, reason: "content_type", status: http.StatusOK,
			step: func(fixture string) testutil.Step {
				return testutil.Step{Header: http.Header{"Content-Type": {"application/json", "application/json"}}, Body: fixture}
			},
		},
		{
			name: "content_encoding", kind: treg.FailureUnexpectedResponse, reason: "content_encoding", status: http.StatusOK,
			step: func(fixture string) testutil.Step {
				return testutil.Step{Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"br"}}, Body: fixture}
			},
		},
		{
			name: "empty_body", kind: treg.FailureUnexpectedResponse, reason: "empty_body", status: http.StatusOK,
			step: func(string) testutil.Step { return testutil.Step{} },
		},
		{
			name: "whitespace_body", kind: treg.FailureUnexpectedResponse, reason: "empty_body", status: http.StatusOK,
			step: func(string) testutil.Step { return testutil.Step{Body: " \r\n\t "} },
		},
		{
			name: "no_content", kind: treg.FailureUnexpectedResponse, reason: "empty_body", status: http.StatusNoContent,
			step: func(string) testutil.Step { return testutil.Step{Status: http.StatusNoContent} },
		},
		{
			name: "trailing_data", kind: treg.FailureMalformedResponse, reason: "trailing_data", status: http.StatusOK,
			step: func(fixture string) testutil.Step { return testutil.Step{Body: fixture + "{}"} },
		},
		{
			name: "trailing_garbage", kind: treg.FailureMalformedResponse, reason: "trailing_data", status: http.StatusOK,
			step: func(fixture string) testutil.Step { return testutil.Step{Body: fixture + " x"} },
		},
		{
			name: "truncated_json", kind: treg.FailureMalformedResponse, reason: "truncated_json", status: http.StatusOK,
			step: func(fixture string) testutil.Step { return testutil.Step{Body: fixture[:len(fixture)/2]} },
		},
		{
			name: "not_json", kind: treg.FailureMalformedResponse, reason: "invalid_json", status: http.StatusOK,
			step: func(string) testutil.Step { return testutil.Step{Body: "<html>ok</html>"} },
		},
	}
}

func TestCatalogReadsFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := synthetic(t, tc.fixture)
			for _, fc := range failureCases() {
				t.Run(fc.name, func(t *testing.T) {
					t.Parallel()
					meta, captured, err := runRead(t, tc, fc.step(fixture), nil)
					assert.Equal(t, treg.Metadata{}, meta)
					assert.Equal(t, treg.CaptureDisabled, captured.State())
					ce := requireCallError(t, err, callErrorWant{
						Op: tc.op, Kind: fc.kind, Reason: fc.reason, Status: fc.status, Marked: new(fc.marked),
					})
					assertRefusalCode(t, fc.refusal, &ce)
					assert.NotContains(t, err.Error(), "nope", "errors never quote the body")
					assert.NotContains(t, err.Error(), "example.com", "errors never quote a URL")
				})
			}
		})
	}
}

// assertRefusalCode checks the refusal code of a failure: code, which then
// shows in the error text, or none, as prose is never parsed into a code.
func assertRefusalCode(t *testing.T, code string, ce *treg.CallError) {
	t.Helper()
	if code == "" {
		if ce.Refusal != nil {
			assert.Empty(t, ce.Refusal.Code, "prose is never parsed into a code")
		}
		return
	}
	require.NotNil(t, ce.Refusal)
	assert.Equal(t, code, ce.Refusal.Code)
	assert.True(t, ce.Refusal.Known())
	assert.Contains(t, ce.Error(), code)
}

func TestCatalogReadsRateLimitRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := gatewayHeader()
			header.Set("Retry-After", "3")
			_, _, err := runRead(t, tc, testutil.Step{
				Status: http.StatusTooManyRequests, Header: header,
				Body: `{"detail":{"error":"public_demo_rate_limited","retry_after_s":2.5}}`,
			}, nil)
			ce := requireCallError(t, err, callErrorWant{
				Op: tc.op, Kind: treg.FailureHTTPStatus, Status: http.StatusTooManyRequests, Marked: new(true),
			})
			require.NotNil(t, ce.Refusal)
			assert.Equal(t, "public_demo_rate_limited", ce.Refusal.Code)
			require.NotNil(t, ce.Refusal.RetryAfterSeconds)
			assert.Equal(t, int64(3), *ce.Refusal.RetryAfterSeconds)
			assert.True(t, ce.Meta.HasRetryAfter)
		})
	}
}

func TestCatalogReadsCapture(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkCaptureCases(t, synthetic(t, tc.fixture), tc.op,
				func(t *testing.T, step testutil.Step, opts []treg.Option, callOpts ...treg.CallOption) (treg.Capture, error) {
					t.Helper()
					_, captured, err := runRead(t, tc, step, opts, callOpts...)
					return captured, err
				})
		})
	}
}

func TestCatalogReadsCallOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		for _, opt := range []struct {
			field string
			opt   treg.CallOption
		}{
			{"UseNumber", treg.UseNumber()},
			{"IgnoreContentType", treg.IgnoreContentType()},
		} {
			t.Run(tc.name+" "+opt.field, func(t *testing.T) {
				t.Parallel()
				script := testutil.NewScript()
				cat, c := scriptClients(t, script)
				_, _, err := tc.call(t.Context(), cat, c, treg.CaptureResponse(true), opt.opt)
				requireValidation(t, err, opt.field, "not_applicable")
				assert.Zero(t, script.Calls(), "nothing was sent")
			})
		}
	}
}

func TestCatalogReadsNilContext(t *testing.T) {
	t.Parallel()
	var nilCtx context.Context
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			cat, c := scriptClients(t, script)
			_, _, err := tc.call(nilCtx, cat, c)
			requireValidation(t, err, "ctx", "nil")
			_, _, err = tc.call(nilCtx, cat, c, treg.UseNumber())
			requireValidation(t, err, "ctx", "nil") // the context is checked before the options
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestCatalogReadsDoneContext(t *testing.T) {
	t.Parallel()
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: synthetic(t, tc.fixture)})
			cat, c := scriptClients(t, script)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, _, err := tc.call(ctx, cat, c, treg.CaptureResponse(true))
			ce := requireCallError(t, err, callErrorWant{Op: tc.op, Kind: treg.FailureCanceled})
			assert.Equal(t, treg.CaptureUnavailable, ce.Captured().State())
			assert.Zero(t, script.Calls(), "a done context sends nothing")
		})
	}
}

func TestCatalogReadsUninitialized(t *testing.T) {
	t.Parallel()
	var nilClient *treg.Client
	assert.Nil(t, nilClient.Catalog())
	assert.Nil(t, (&treg.Client{}).Catalog())
	clients := []struct {
		name string
		cat  *treg.CatalogClient
		c    *treg.Client
	}{
		{"nil", nil, nil},
		{"zero", &treg.CatalogClient{}, &treg.Client{}},
		{"catalog_of_nil_client", nilClient.Catalog(), nilClient},
		{"catalog_of_zero_client", (&treg.Client{}).Catalog(), &treg.Client{}},
	}
	for _, tc := range readCases() {
		for _, cl := range clients {
			t.Run(tc.name+" "+cl.name, func(t *testing.T) {
				t.Parallel()
				_, _, err := tc.call(t.Context(), cl.cat, cl.c)
				requireConfigValidation(t, err, "client", "not_initialized")
			})
		}
	}
}

func TestCatalogReadsResponseLimit(t *testing.T) {
	t.Parallel()
	limit := []treg.Option{treg.WithMaxResponseBytes(16)}
	for _, tc := range readCases() {
		tooLarge := callErrorWant{Op: tc.op, Kind: treg.FailureResponseTooLarge, Status: http.StatusOK}
		t.Run(tc.name+" declared length", func(t *testing.T) {
			t.Parallel()
			_, _, err := runRead(t, tc, testutil.Step{Body: synthetic(t, tc.fixture)}, limit)
			requireCallError(t, err, tooLarge)
		})
		t.Run(tc.name+" undeclared length", func(t *testing.T) {
			t.Parallel()
			// Without a declared length the body is read up to the limit.
			body := testutil.NewBody(synthetic(t, tc.fixture))
			rt := testutil.TransportWith(http.StatusOK, nil, -1, body)
			_, _, err := tc.call(t.Context(), newCatalog(t, rt, limit...), newClient(t, rt, limit...))
			requireCallError(t, err, tooLarge)
			assert.LessOrEqual(t, body.BytesRead(), int64(17))
			assert.Positive(t, body.Closes(), "the body was closed")
		})
	}
}

// The parts of paddedSearch around its padding.
const (
	paddedHead = `{"query":"`
	paddedTail = `","count":0,"total":0,"results":[],"hints":[]}`
)

// paddedSearch returns a valid search answer of exactly size bytes.
func paddedSearch(size int) string {
	return paddedHead + strings.Repeat("a", size-len(paddedHead)-len(paddedTail)) + paddedTail
}

func TestCatalogTypedReadCap(t *testing.T) {
	t.Parallel()
	const typedCap = 8 << 20
	limit := treg.WithMaxResponseBytes(16 << 20)

	t.Run("exactly_8_MiB", func(t *testing.T) {
		t.Parallel()
		body := paddedSearch(typedCap)
		require.Len(t, body, typedCap)
		rt := testutil.TransportWith(http.StatusOK, nil, -1, io.NopCloser(strings.NewReader(body)))
		res, err := newCatalog(t, rt, limit).Search(t.Context(), treg.SearchRequest{})
		require.NoError(t, err)
		assert.Len(t, res.Value.Query, typedCap-len(paddedHead)-len(paddedTail))
	})
	for _, tc := range []struct {
		name     string
		declared bool
	}{
		{"undeclared_over_8_MiB", false},
		{"declared_over_8_MiB", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := paddedSearch(typedCap + 1)
			length := int64(-1)
			if tc.declared {
				length = int64(len(body))
			}
			rt := testutil.TransportWith(http.StatusOK, nil, length, io.NopCloser(strings.NewReader(body)))
			_, err := newCatalog(t, rt, limit).Search(t.Context(), treg.SearchRequest{})
			requireCallError(t, err, callErrorWant{
				Op: treg.OpCatalogSearch, Kind: treg.FailureResponseTooLarge, Status: http.StatusOK,
			})
		})
	}
	t.Run("endless_body", func(t *testing.T) {
		t.Parallel()
		body := &testutil.EndlessBody{}
		rt := testutil.TransportWith(http.StatusOK, nil, -1, body)
		_, err := newClient(t, rt, limit).EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: cloneID})
		require.ErrorIs(t, err, treg.ErrResponseTooLarge)
		assert.Greater(t, body.BytesRead(), int64(typedCap), "the cap allows 8 MiB")
		assert.Less(t, body.BytesRead(), int64(typedCap+(64<<10)), "reading stops just past 8 MiB, not at 16 MiB")
		assert.Positive(t, body.Closes())
	})
}

func TestCatalogSlugValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value, reason string
	}{
		{"empty", "", "required"},
		{"too_long", strings.Repeat("a", 129), "too_long"},
		{"slash", "a/b", "invalid_character"},
		{"space", "a b", "invalid_character"},
		{"escaped_slash", "%2F", "invalid_character"},
		{"question_mark", "a?b", "invalid_character"},
		{"non_ascii", "café", "invalid_character"},
		{"tab", "a\tb", "control_character"},
		{"dot", ".", "dot_segment"},
		{"dot_dot", "..", "dot_segment"},
	}
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			cat := newCatalog(t, script)
			_, err := cat.Platform(t.Context(), treg.PlatformRequest{Slug: sc.value, IncludeHidden: true})
			requireValidation(t, err, "Slug", sc.reason)
			_, err = cat.Provider(t.Context(), treg.ProviderRequest{Service: sc.value})
			requireValidation(t, err, "Service", sc.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestCatalogSlugAccepted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, slug string }{
		{"max length", strings.Repeat("a", 128)},
		{"dash", "voice-gen"},
		{"unreserved punctuation", "a.b_c~d"},
		{"three dots", "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(
				testutil.Step{Body: `{"platform":{"slug":"","label":"","category":""},"capabilities":[],"domains":[],` +
					`"extended":[],"hidden_count":0,"providers":{}}`},
				testutil.Step{Body: `{"provider":{"service":"","display_name":""},"platforms":[]}`},
			)
			cat := newCatalog(t, script)
			_, err := cat.Platform(t.Context(), treg.PlatformRequest{Slug: tc.slug})
			require.NoError(t, err)
			_, err = cat.Provider(t.Context(), treg.ProviderRequest{Service: tc.slug})
			require.NoError(t, err)
			assert.Equal(t, []string{"/catalog/platforms/" + tc.slug, "/catalog/providers/" + tc.slug}, script.Paths())
		})
	}
}

func TestCatalogSearchValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		req    treg.SearchRequest
		field  string
		reason string
	}{
		{"limit_over", treg.SearchRequest{Limit: 101}, "Limit", "out_of_range"},
		{"limit_negative", treg.SearchRequest{Limit: -1}, "Limit", "out_of_range"},
		{"added_over", treg.SearchRequest{AddedWithinDays: 366}, "AddedWithinDays", "out_of_range"},
		{"added_negative", treg.SearchRequest{AddedWithinDays: -1}, "AddedWithinDays", "out_of_range"},
		{"sort_bogus", treg.SearchRequest{Sort: "bogus"}, "Sort", "invalid_format"},
		{"sort_case", treg.SearchRequest{Sort: "Best"}, "Sort", "invalid_format"},
		{"query_too_long", treg.SearchRequest{Query: strings.Repeat("q", 501)}, "Query", "too_long"},
		{"query_nul", treg.SearchRequest{Query: "voice\x00clone"}, "Query", "control_character"},
		{"query_newline", treg.SearchRequest{Query: "voice\nclone"}, "Query", "control_character"},
		{"query_escape", treg.SearchRequest{Query: "voice\x1bclone"}, "Query", "control_character"},
		{"query_tab", treg.SearchRequest{Query: "voice\tclone"}, "Query", "control_character"},
		{"query_del", treg.SearchRequest{Query: "voice\x7fclone"}, "Query", "control_character"},
		{"query_c1", treg.SearchRequest{Query: "voice\u0085clone"}, "Query", "control_character"},
		{"query_invalid_utf8", treg.SearchRequest{Query: "voice\xffclone"}, "Query", "invalid_character"},
		{"query_utf8_too_long", treg.SearchRequest{Query: strings.Repeat("é", 251)}, "Query", "too_long"},
	}
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			_, err := newCatalog(t, script).Search(t.Context(), sc.req)
			requireValidation(t, err, sc.field, sc.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestCatalogSearchAccepted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		req   treg.SearchRequest
		query string
	}{
		{"zero_values", treg.SearchRequest{}, "q="},
		{"limit_one", treg.SearchRequest{Limit: 1}, "limit=1&q="},
		{"limit_max", treg.SearchRequest{Limit: 100}, "limit=100&q="},
		{"added_one", treg.SearchRequest{AddedWithinDays: 1}, "added_within_days=1&q="},
		{"added_max", treg.SearchRequest{AddedWithinDays: 365}, "added_within_days=365&q="},
		{"query_max", treg.SearchRequest{Query: strings.Repeat("q", 500)}, "q=" + strings.Repeat("q", 500)},
		{"query_utf8", treg.SearchRequest{Query: "voix française"}, "q=voix+fran%C3%A7aise"},
		{"query_utf8_max", treg.SearchRequest{Query: strings.Repeat("é", 250)}, "q=" + strings.Repeat("%C3%A9", 250)},
		{"query_symbols", treg.SearchRequest{Query: " text-to-speech: 100% "}, "q=+text-to-speech%3A+100%25+"},
	}
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: minimalSearch})
			_, err := newCatalog(t, script).Search(t.Context(), sc.req)
			require.NoError(t, err)
			assert.Equal(t, sc.query, testutil.OnlyRequest(t, script).RawQuery)
		})
	}
}

// catalogIDCases returns ids that every id-taking read refuses.
func catalogIDCases() []struct{ name, id, reason string } {
	return []struct{ name, id, reason string }{
		{"empty", "", "required"},
		{"no_dot", "acmevoice", "invalid_format"},
		{"http_prefix", "http.example.tts", "invalid_format"},
		{"http_prefix_upper", "HTTP.example.tts", "invalid_format"},
		{"https_prefix", "https.example.tts", "invalid_format"},
		{"dot_dot", "acmevoice..tts", "invalid_format"},
		{"leading_dot", ".acmevoice.tts", "invalid_format"},
		{"trailing_dot", "acmevoice.tts.", "invalid_format"},
		{"too_long", "a." + strings.Repeat("b", 255), "too_long"},
		{"escaped_slash", "acmevoice%2Ftts.x", "invalid_character"},
		{"slash", "acmevoice/tts.x", "invalid_character"},
		{"space", "acmevoice.tts x", "invalid_character"},
		{"url", "https://example.com/x", "invalid_character"},
		{"crlf", "acmevoice.tts\r\n", "control_character"},
	}
}

func TestCatalogIDValidation(t *testing.T) {
	t.Parallel()
	for _, ic := range catalogIDCases() {
		t.Run(ic.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			cat, c := scriptClients(t, script)
			id := treg.EndpointID(ic.id)
			_, err := cat.Endpoint(t.Context(), treg.EndpointRequest{ID: id})
			requireValidation(t, err, "ID", ic.reason)
			_, err = cat.Example(t.Context(), treg.ExampleRequest{ID: id})
			requireValidation(t, err, "ID", ic.reason)
			_, err = c.EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: id})
			requireValidation(t, err, "ID", ic.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestCatalogIDMaxLength(t *testing.T) {
	t.Parallel()
	id := "a." + strings.Repeat("b", 254)
	require.Len(t, id, 256)
	script := testutil.NewScript(testutil.Step{Body: `{"tier":"none"}`}, testutil.Step{Body: `"x"`})
	cat, c := scriptClients(t, script)
	_, err := c.EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: treg.EndpointID(id)})
	require.NoError(t, err)
	_, err = cat.Example(t.Context(), treg.ExampleRequest{ID: treg.EndpointID(id)})
	require.NoError(t, err)
	assert.Equal(t, []string{"/catalog/endpoints/" + id + "/access", "/catalog/examples/" + id}, script.Paths())
}

func TestEndpointAccessAuthorizationMethodValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, method, reason string }{
		{"slash", "a/b", "invalid_character"},
		{"space", "a b", "invalid_character"},
		{"escaped slash", "a%2Fb", "invalid_character"},
		{"non-ASCII", "café", "invalid_character"},
		{"DEL", "a\x7fb", "control_character"},
		{"too long", strings.Repeat("m", 65), "too_long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			_, err := newClient(t, script).EndpointAccess(t.Context(),
				treg.EndpointAccessRequest{ID: cloneID, AuthorizationMethod: tc.method})
			requireValidation(t, err, "AuthorizationMethod", tc.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
	t.Run("max length accepted", func(t *testing.T) {
		t.Parallel()
		method := strings.Repeat("m", 64)
		script := testutil.NewScript(testutil.Step{Body: `{"tier":"none"}`})
		_, err := newClient(t, script).EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: cloneID, AuthorizationMethod: method})
		require.NoError(t, err)
		assert.Equal(t, "authorization_method="+method, testutil.OnlyRequest(t, script).RawQuery)
	})
}

func TestCatalogCredentialScan(t *testing.T) {
	t.Parallel()
	access := bind((*treg.Client).EndpointAccess, treg.EndpointAccessRequest{ID: cloneID, AuthorizationMethod: testToken})
	for _, tc := range []struct {
		name          string
		call          readCall
		opts          []treg.CallOption
		field, reason string
	}{
		{
			"Search query", catalogRead(bind((*treg.CatalogClient).Search, treg.SearchRequest{Query: "find " + testToken})),
			nil, "Request", "credential_in_request",
		},
		{
			"Platform slug", catalogRead(bind((*treg.CatalogClient).Platform, treg.PlatformRequest{Slug: testToken})),
			nil, "Request", "credential_in_request",
		},
		{
			"Endpoint id", catalogRead(bind((*treg.CatalogClient).Endpoint, treg.EndpointRequest{ID: "acmevoice." + testToken})),
			nil, "Request", "credential_in_request",
		},
		// The options are checked before the scan.
		{
			"EndpointAccess method with an inapplicable option", clientRead(access),
			[]treg.CallOption{treg.UseNumber()},
			"UseNumber", "not_applicable",
		},
		{"EndpointAccess method", clientRead(access), nil, "Request", "credential_in_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			_, _, err := tc.call(t.Context(), c.Catalog(), c, tc.opts...)
			requireValidation(t, err, tc.field, tc.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
			assert.NotContains(t, err.Error(), testToken)
		})
	}
}

func TestCatalogServerInfoDecodes(t *testing.T) {
	t.Parallel()
	t.Run("fixture", func(t *testing.T) {
		t.Parallel()
		script := testutil.NewScript(testutil.Step{Body: synthetic(t, "catalog/meta.json")})
		res, err := newCatalog(t, script).ServerInfo(t.Context(), treg.ServerInfoRequest{})
		require.NoError(t, err)
		info := res.Value
		assert.Equal(t, "https://treg.example.com", info.PublicURL)
		assert.True(t, info.GitHub)
		assert.False(t, info.Google)
		assert.Equal(t, "2026.10.0-synthetic", info.AppVersion)
		assert.Equal(t, "0.42.0", info.TregVersion)
		assert.True(t, info.Hub)
		assert.Equal(t, map[string]treg.PausedProvider{
			"examplespeech": {DisplayName: "Example Speech", Message: "Paused for synthetic maintenance."},
		}, info.PausedProviders)
		assert.Equal(t, treg.Some(treg.ReferralSettings{ReferrerMicro: 5_000_000, ReferredMicro: 2_500_000}), info.Referral, "referral")
		ref, ok := info.Referral.Get()
		require.True(t, ok)
		assert.Equal(t, "5", ref.ReferrerMicro.DecimalUSD())
		assert.Equal(t, "$2.5", ref.ReferredMicro.String())
		assert.Equal(t, treg.OpServerInfo, res.Meta.Op)
		assert.Equal(t, http.StatusOK, res.Meta.StatusCode)
		assert.Equal(t, treg.CaptureDisabled, res.Captured.State())
	})
	head := `{"public_url":"https://treg.example.com","github":false,"google":true,"app_version":"a",` +
		`"treg_version":"b","hub":false,"paused_providers":{}`
	for _, variant := range []struct {
		name     string
		body     string
		referral treg.Optional[treg.ReferralSettings]
	}{
		{"referral_absent", head + `}`, treg.Optional[treg.ReferralSettings]{}},
		{"referral_null", head + `,"referral":null}`, treg.Null[treg.ReferralSettings]()},
	} {
		t.Run(variant.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: variant.body})
			res, err := newCatalog(t, script).ServerInfo(t.Context(), treg.ServerInfoRequest{})
			require.NoError(t, err)
			assert.Empty(t, res.Value.PausedProviders)
			assert.True(t, res.Value.Google)
			assert.Equal(t, variant.referral, res.Value.Referral, "referral")
		})
	}
}

func TestCatalogPlatformsDecodes(t *testing.T) {
	t.Parallel()
	fixture := synthetic(t, "catalog/platforms.json")
	script := testutil.NewScript(testutil.Step{Body: fixture})
	res, err := newCatalog(t, script).Platforms(t.Context(), treg.PlatformsRequest{})
	require.NoError(t, err)
	list := res.Value
	require.Len(t, list.Platforms, 3)

	voice := list.Platforms[0]
	assert.Equal(t, "voice-gen", voice.Slug)
	assert.Equal(t, "Voice generation", voice.Label)
	assert.Equal(t, "audio", voice.Category)
	assert.Equal(t, treg.Some[int64](1), voice.Featured, "featured")
	assert.Equal(t, "Synthetic voices and speech for tests.", voice.Summary)
	price, ok := voice.PriceFrom.Get()
	require.True(t, ok)
	priceRaw := `{"type": "per_call", "value": 0.006667, "currency": "USD", "per": 1, "unit": "request", ` +
		`"usd": 0.006667, "source": "synthetic", "table": [{"tier": "standard", "usd": 0.006667}]}`
	require.Contains(t, fixture, priceRaw)
	assertSameBytes(t, priceRaw, price.Raw, "the rate table stays in Raw")
	assert.Equal(t, treg.CostPerCall, price.Type)
	assert.Equal(t, treg.Some(treg.Decimal("0.006667")), price.USD, "price_from.usd")
	assert.Equal(t, treg.Optional[treg.Decimal]{}, price.USDMin, "price_from.usd_min")
	assert.Equal(t, int64(3), voice.Capabilities)
	assert.Equal(t, int64(7), voice.Endpoints)
	assert.Equal(t, int64(5), voice.Verified)
	assert.Equal(t, []string{"acmevoice", "examplespeech"}, voice.Providers)

	seo := list.Platforms[1]
	assert.Equal(t, "web-seo", seo.Slug)
	assert.Equal(t, treg.Null[int64](), seo.Featured, "featured")
	assert.Equal(t, treg.Null[treg.Cost](), seo.PriceFrom, "price_from")
	assert.Equal(t, int64(0), seo.Verified)

	misc := list.Platforms[2]
	assert.Equal(t, treg.Optional[int64]{}, misc.Featured, "featured")
	assert.Equal(t, treg.Optional[treg.Cost]{}, misc.PriceFrom, "price_from")
	assert.NotNil(t, misc.Providers)
	assert.Empty(t, misc.Providers)

	assert.Equal(t, map[string]string{
		"acmevoice": "Acme Voice", "examplespeech": "Example Speech", "sampleseo": "Sample SEO",
	}, list.Providers)
	assert.Equal(t, "synthetic-catalog-2026-10-01", list.GeneratedFrom)
	assert.Equal(t, treg.OpCatalogPlatforms, res.Meta.Op)
}

func TestCatalogPlatformDecodes(t *testing.T) {
	t.Parallel()
	fixture := synthetic(t, "catalog/platform.json")
	script := testutil.NewScript(testutil.Step{Body: fixture})
	res, err := newCatalog(t, script).Platform(t.Context(), treg.PlatformRequest{Slug: "voice-gen"})
	require.NoError(t, err)
	p := res.Value
	assert.Equal(t, treg.PlatformRef{Slug: "voice-gen", Label: "Voice generation", Category: "audio"}, p.Platform)

	require.Len(t, p.Capabilities, 1)
	assert.Equal(t, "voice.clone", p.Capabilities[0].ID)
	assert.Equal(t, "Clone a voice from a short sample.", p.Capabilities[0].Description)
	require.Len(t, p.Capabilities[0].Endpoints, 1)
	assertCloneRow(t, fixture, p.Capabilities[0].Endpoints[0])

	assert.Equal(t, raws(`{"id": "examplespeech.voice.list", "note": "Synthetic extended row."}`), p.Extended)
	assert.Equal(t, int64(2), p.HiddenCount)

	require.Len(t, p.Domains, 1)
	assert.Equal(t, "voice", p.Domains[0].Domain)
	require.Len(t, p.Domains[0].Rows, 1)
	row := p.Domains[0].Rows[0]
	assert.Equal(t, "data", row.Kind)
	assert.Equal(t, "speech.legacy", row.Capability)
	assert.Equal(t, "Retired synthetic speech rows.", row.Description)
	assert.Equal(t, "voice", row.Domain)
	require.Len(t, row.Endpoints, 1)
	assertLegacyRow(t, row.Endpoints[0])

	require.Len(t, p.Providers, 2)
	acme := p.Providers["acmevoice"]
	assert.Equal(t, "acmevoice", acme.Service)
	assert.Equal(t, "Acme Voice", acme.DisplayName)
	assert.Equal(t, treg.Some("60 requests per minute"), acme.Limits, "limits")
	assert.Equal(t, treg.Some("https://acmevoice.example.com/pricing"), acme.PricingURL, "pricing_url")
	assert.Equal(t, treg.Some("https://acmevoice.example.com/docs"), acme.Docs, "docs")
	assert.Equal(t, treg.Some("api_key"), acme.AuthKind, "auth_kind")
	assert.Equal(t, treg.Some(true), acme.Metered, "metered")
	speech := p.Providers["examplespeech"]
	assert.Equal(t, treg.Null[string](), speech.Limits, "limits")
	assert.Equal(t, treg.Null[string](), speech.PricingURL, "pricing_url")
	assert.Equal(t, treg.Some("oauth"), speech.AuthKind, "auth_kind")
	assert.Equal(t, treg.Some(false), speech.Metered, "metered")
	assert.Equal(t, treg.OpCatalogPlatform, res.Meta.Op)
}

func TestCatalogProviderDecodes(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: synthetic(t, "catalog/provider.json")})
	res, err := newCatalog(t, script).Provider(t.Context(), treg.ProviderRequest{Service: "acmevoice"})
	require.NoError(t, err)
	d := res.Value
	assert.Equal(t, "acmevoice", d.Provider.Service)
	assert.Equal(t, treg.Some("oauth"), d.Provider.AuthKind, "auth_kind")
	require.Len(t, d.Platforms, 2)
	assert.Equal(t, "voice-gen", d.Platforms[0].Slug)
	assert.Equal(t, "Voice generation", d.Platforms[0].Label)
	assert.Equal(t, "web-seo", d.Platforms[1].Slug)
	assert.NotNil(t, d.Platforms[1].Tools)
	assert.Empty(t, d.Platforms[1].Tools)
	require.Len(t, d.Platforms[0].Tools, 1)

	ep := d.Platforms[0].Tools[0]
	assert.Equal(t, treg.EndpointID("acmevoice.voice.any"), ep.ID)
	assert.Equal(t, treg.EndpointKindRouted, ep.Kind)
	assert.Equal(t, "any_account", ep.Scope)
	assertSameBytes(t, `{"text": {"type": "string"}}`, ep.Input)
	assertSameBytes(t, "null", ep.TestRequest)
	cost, ok := ep.Cost.Get()
	require.True(t, ok)
	assert.Equal(t, treg.CostPerSuccess, cost.Type)
	assert.Equal(t, treg.Some(treg.Decimal("0.01")), cost.USD, "cost.usd")
	assert.Equal(t, treg.Some(treg.Decimal("0.002")), cost.USDMin, "cost.usd_min")
	assert.Equal(t, treg.Some(true), cost.Varies, "cost.varies")
	assert.Equal(t, treg.Optional[string]{}, cost.Source, "cost.source")
	assert.Equal(t, treg.Null[treg.AsyncDescriptor](), ep.Async, "async")
	assert.Equal(t, treg.Some(false), ep.PlatformEligible, "platform_eligible")
	assert.Equal(t, []treg.EndpointID{"acmevoice.voice.clone", "examplespeech.tts.create"}, ep.RoutedChildren)
	assertSameBytes(t, "1", ep.ChildrenHidden)
	assertSameBytes(t, `"oauth_pkce"`, ep.AuthorizationMethod)
	assertSameBytes(t, `["oauth_pkce", "device_code"]`, ep.AuthorizationMethods)
	assertSameBytes(t, `{"oauth_pkce": "/connect/acmevoice"}`, ep.AuthorizationPaths)
	assertSameBytes(t, `["voices:write"]`, ep.RequiredScopes)
	assertSameBytes(t, `"https://acmevoice.example.com/"`, ep.RequiredResource)
	assertSameBytes(t, `"bearer"`, ep.TokenType)
	assert.Equal(t, treg.OpCatalogProvider, res.Meta.Op)
}

func TestCatalogSearchDecodes(t *testing.T) {
	t.Parallel()
	t.Run("fixture", func(t *testing.T) {
		t.Parallel()
		fixture := synthetic(t, "catalog/search.json")
		script := testutil.NewScript(testutil.Step{Body: fixture})
		res, err := newCatalog(t, script).Search(t.Context(), treg.SearchRequest{Query: "clone a voice"})
		require.NoError(t, err)
		s := res.Value
		assert.Equal(t, "clone a voice", s.Query)
		assert.Equal(t, int64(2), s.Count)
		assert.Equal(t, int64(9), s.Total)
		assert.Equal(t, []string{"Narrow the search with a platform."}, s.Hints)
		assert.Equal(t, treg.Some(treg.SearchSortBest), s.Sort, "sort")
		assert.Equal(t, treg.Some[int64](30), s.AddedWithinDays, "added_within_days")
		assert.Equal(t, treg.Some[int64](365), s.CappedAtDays, "capped_at_days")
		assertSameBytes(t, `{"provider": "acmevoice",  "why": "synthetic"}`, s.LedBy)
		assertSameBytes(t, `[{"id": "examplespeech.tts.create", "distance": 0.120}]`, s.Near)
		require.Len(t, s.Results, 2)
		assertSearchTopRow(t, s.Results[0])
		assertSearchFreeRow(t, fixture, s.Results[1])
		assert.Equal(t, treg.OpCatalogSearch, res.Meta.Op)
	})
	t.Run("minimal answer", func(t *testing.T) {
		t.Parallel()
		script := testutil.NewScript(testutil.Step{Body: minimalSearch})
		res, err := newCatalog(t, script).Search(t.Context(), treg.SearchRequest{})
		require.NoError(t, err)
		assert.Equal(t, treg.Optional[treg.SearchSort]{}, res.Value.Sort, "sort")
		assert.Equal(t, treg.Optional[int64]{}, res.Value.AddedWithinDays, "added_within_days")
		assert.Equal(t, treg.Optional[int64]{}, res.Value.CappedAtDays, "capped_at_days")
		assert.Nil(t, res.Value.LedBy)
		assert.Nil(t, res.Value.Near)
		assert.NotNil(t, res.Value.Results)
		assert.Empty(t, res.Value.Results)
	})
	t.Run("a sort echo outside the request's set still decodes", func(t *testing.T) {
		t.Parallel()
		body := strings.TrimSuffix(minimalSearch, "}") + `,"sort":"relevance"}`
		res, err := newCatalog(t, testutil.NewScript(testutil.Step{Body: body})).Search(t.Context(), treg.SearchRequest{})
		require.NoError(t, err)
		assert.Equal(t, treg.Some(treg.SearchSort("relevance")), res.Value.Sort)
	})
}

// assertSearchTopRow checks the first synthetic search result, whose async
// descriptor uses every member.
func assertSearchTopRow(t *testing.T, ep treg.Endpoint) {
	t.Helper()
	assert.Equal(t, treg.EndpointID(cloneID), ep.ID)
	assert.Equal(t, treg.Some("voice.clone"), ep.Capability, "capability")
	assert.Equal(t, treg.Some("Clone a voice from a short sample."), ep.CapabilityDescription, "capability_description")
	assert.Equal(t, treg.Some("voice-gen"), ep.Platform, "platform")
	assert.Equal(t, treg.Some("Voice generation"), ep.PlatformLabel, "platform_label")
	assert.Equal(t, treg.Some(treg.Decimal("12.750")), ep.Score, "score keeps its exact text")
	cost, ok := ep.Cost.Get()
	require.True(t, ok)
	assertSameBytes(t, `{"type": "per_call", "value": 0.006667, "currency": "USD", "per": 1, "usd": 0.006667}`, cost.Raw)
	assert.Equal(t, treg.Optional[string]{}, cost.Unit, "cost.unit")

	d, ok := ep.Async.Get()
	require.True(t, ok)
	assert.Equal(t, "data.task_id", d.IDFrom)
	assert.Empty(t, d.Poll.Endpoint)
	assert.Nil(t, d.Poll.Param)
	assert.Equal(t, "data.poll_url", d.Poll.URLFrom)
	assert.Equal(t, []string{"jobs.example.com"}, d.Poll.URLHosts)
	assert.Equal(t, "data.state", d.Status.Path)
	assert.Equal(t, raws(`"done"`, `2`), d.Status.Success)
	assert.Equal(t, raws(`"error"`), d.Status.Failure)
	assert.Equal(t, raws(`"expired"`), d.Status.BilledFailure)
	assert.Equal(t, raws(`"queued"`, `"running"`), d.Status.Progress)
	assert.Empty(t, d.Result.Path)
	assert.Equal(t, "acmevoice.jobs.result", d.Result.Fetch)
	assert.Equal(t, "Result URLs expire after one hour.", d.Result.TTLNote)
	require.NotNil(t, d.Result.FetchParam)
	assert.Equal(t, treg.AsyncFetchParam{In: "queryParams", Name: "job", ValueFrom: "data.task_id"}, *d.Result.FetchParam)
	assert.Equal(t, treg.Decimal("1.5"), d.Interval)
	assert.Equal(t, treg.Some[int64](3600), d.MaxAge, "async.max_age")
	assert.True(t, d.TerminalOnSubmission)
}

// assertSearchFreeRow checks the second synthetic search result, whose
// cost members are all null.
func assertSearchFreeRow(t *testing.T, fixture string, ep treg.Endpoint) {
	t.Helper()
	assert.Equal(t, treg.EndpointID("examplespeech.tts.create"), ep.ID)
	assert.Equal(t, treg.Null[string](), ep.Capability, "capability")
	assert.Equal(t, treg.Null[string](), ep.CapabilityDescription, "capability_description")
	assert.Equal(t, treg.Some(treg.Decimal("3")), ep.Score, "score")
	assert.Equal(t, treg.Null[string](), ep.Verified, "verified")
	assert.Equal(t, treg.Some("2026-03-01"), ep.Added, "added")
	cost, ok := ep.Cost.Get()
	require.True(t, ok)
	freeRaw := `{"type": "free", "value": null, "currency": null, "per": null, "unit": null, "usd": null, ` +
		`"usd_min": null, "source": "synthetic", "source_url": null, "checked": null, "confidence": "low", ` +
		`"note": null, "varies": null}`
	require.Contains(t, fixture, freeRaw)
	assertSameBytes(t, freeRaw, cost.Raw)
	assert.Equal(t, treg.CostFree, cost.Type)
	assert.Equal(t, treg.Null[treg.Decimal](), cost.Value, "cost.value")
	assert.Equal(t, treg.Null[string](), cost.Currency, "cost.currency")
	assert.Equal(t, treg.Null[treg.Decimal](), cost.Per, "cost.per")
	assert.Equal(t, treg.Null[string](), cost.Unit, "cost.unit")
	assert.Equal(t, treg.Null[treg.Decimal](), cost.USD, "cost.usd")
	assert.Equal(t, treg.Null[treg.Decimal](), cost.USDMin, "cost.usd_min")
	assert.Equal(t, treg.Some("synthetic"), cost.Source, "cost.source")
	assert.Equal(t, treg.Null[string](), cost.SourceURL, "cost.source_url")
	assert.Equal(t, treg.Null[string](), cost.Checked, "cost.checked")
	assert.Equal(t, treg.Some("low"), cost.Confidence, "cost.confidence")
	assert.Equal(t, treg.Null[string](), cost.Note, "cost.note")
	assert.Equal(t, treg.Null[bool](), cost.Varies, "cost.varies")
	assert.Equal(t, treg.Null[treg.AsyncDescriptor](), ep.Async, "async")
	assert.Equal(t, treg.Null[bool](), ep.PlatformEligible, "platform_eligible")
}

func TestCatalogEndpointDecodes(t *testing.T) {
	t.Parallel()
	t.Run("fixture", func(t *testing.T) {
		t.Parallel()
		fixture := synthetic(t, "catalog/endpoint.json")
		script := testutil.NewScript(testutil.Step{Body: fixture})
		res, err := newCatalog(t, script).Endpoint(t.Context(), treg.EndpointRequest{ID: cloneID})
		require.NoError(t, err)
		d := res.Value
		assertCloneRow(t, fixture, d.Endpoint)
		assert.Equal(t, "acmevoice", d.Provider.Service)
		assert.Equal(t, treg.Some(true), d.Provider.Metered, "metered")
		require.Len(t, d.Siblings, 1)
		assert.Equal(t, treg.EndpointID("acmevoice.voice.delete"), d.Siblings[0].ID)
		assert.Equal(t, treg.EndpointKindAccount, d.Siblings[0].Kind)
		assert.Equal(t, "/v1/voices/{id}", d.Siblings[0].Path)
		assert.Equal(t, treg.Null[treg.Cost](), d.Siblings[0].Cost, "sibling cost")
		assert.Equal(t, treg.Some(true), d.ObservedPending, "observed_pending")
		assert.Equal(t, []treg.RelatedCapability{{
			EndpointID: "examplespeech.tts.create", Capability: "speech.synthesize", Does: "Reads text aloud with a stock voice.",
		}}, d.RelatedCapabilities)
		routing := `{"strategy": "cheapest",  "children": ["acmevoice.voice.clone"], "weights": {"acmevoice.voice.clone": 1.0}}`
		require.Contains(t, fixture, routing)
		assertSameBytes(t, routing, d.Routing)
		assert.Equal(t, `treg call acmevoice.voice.clone --json '{"sample_url": "https://example.com/sample.wav"}'`, d.CallTemplate)
		example := `{"voice_id": "vc_synthetic_001", "status": "ready",  ` +
			`"preview_url": "https://cdn.example.com/previews/vc_synthetic_001.mp3", "score": 0.50}`
		require.Contains(t, fixture, example)
		assertSameBytes(t, example, d.ExampleResponse)
		assert.Equal(t, []string{"Upload a clean 30 second sample."}, d.Hints)
		assert.Equal(t, treg.OpCatalogEndpoint, res.Meta.Op)
	})
	t.Run("minimal descriptor", func(t *testing.T) {
		t.Parallel()
		minimal := `{"endpoint":{"id":"a.b"},"provider":{"service":"a","display_name":"A"},"siblings":[],` +
			`"call_template":"treg call a.b","example_response":null,"hints":[]}`
		script := testutil.NewScript(testutil.Step{Body: minimal})
		res, err := newCatalog(t, script).Endpoint(t.Context(), treg.EndpointRequest{ID: "a.b"})
		require.NoError(t, err)
		assert.Equal(t, treg.Optional[bool]{}, res.Value.ObservedPending, "observed_pending")
		assert.Nil(t, res.Value.RelatedCapabilities)
		assert.Nil(t, res.Value.Routing)
		assertSameBytes(t, "null", res.Value.ExampleResponse)
		assert.Equal(t, treg.Optional[treg.Cost]{}, res.Value.Endpoint.Cost, "cost")
		assert.Nil(t, res.Value.Endpoint.Input)
	})
}

func TestCatalogExampleBytes(t *testing.T) {
	t.Parallel()
	t.Run("fixture", func(t *testing.T) {
		t.Parallel()
		fixture := synthetic(t, "catalog/example.json")
		require.Contains(t, fixture, `"status": "ready-again"`, "the fixture repeats a key")
		script := testutil.NewScript(testutil.Step{Body: "\n  " + fixture + "\n\n"})
		res, err := newCatalog(t, script).Example(t.Context(), treg.ExampleRequest{ID: cloneID})
		require.NoError(t, err, "examples are provider data: no strict check")
		assertSameBytes(t, strings.TrimSpace(fixture), res.Value, "byte for byte, without surrounding whitespace")
		assert.Equal(t, treg.OpCatalogExample, res.Meta.Op)
	})
	for _, tc := range []struct{ name, body string }{
		{"string", `"just text"`},
		{"array of number spellings", `[1, 2.50, 1e3]`},
		{"null", `null`},
		{"small number", `0.000001`},
		{"keys differing in case", `{"Id": 1, "id": 2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: tc.body + "\n"})
			res, err := newCatalog(t, script).Example(t.Context(), treg.ExampleRequest{ID: cloneID})
			require.NoError(t, err)
			assertSameBytes(t, tc.body, res.Value)
		})
	}
}

func TestEndpointAccessDecodes(t *testing.T) {
	t.Parallel()
	t.Run("fixture", func(t *testing.T) {
		t.Parallel()
		script := testutil.NewScript(testutil.Step{Body: synthetic(t, "catalog/access.json")})
		res, err := newClient(t, script).EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: cloneID})
		require.NoError(t, err)
		a := res.Value
		assert.Equal(t, treg.AccessCredential, a.Tier)
		assert.True(t, a.Tier.Known())
		assert.Equal(t, treg.Some("Uses the team's connected Acme Voice account."), a.Detail, "detail")
		assert.Equal(t, treg.Some(true), a.Metered, "metered")
		assert.Equal(t, treg.Some(treg.MicroUSD(6667)), a.EstimatedCostMicro, "estimated_cost_micro")
		micro, ok := a.EstimatedCostMicro.Get()
		require.True(t, ok)
		assert.Equal(t, "0.006667", micro.DecimalUSD())
		assert.Equal(t, treg.Some(treg.Decimal("0.006667")), a.EstimatedCostUSD, "estimated_cost_usd")
		assert.Equal(t, treg.Some(treg.Decimal("0.005")), a.EstimatedCostUSDMin, "estimated_cost_usd_min")
		assert.Equal(t, treg.Some("oauth_pkce"), a.AuthorizationMethod, "authorization_method")
		assert.Equal(t, treg.Some("acmevoice.oauth"), a.ConnectCapability, "connect_capability")
		assert.Equal(t, treg.Some("treg connect acmevoice"), a.ConnectCommand, "connect_command")
		assert.Equal(t, treg.Some("Connect Acme Voice"), a.ActionLabel, "action_label")
		assert.Equal(t, treg.Some("Connect an Acme Voice account first."), a.MissingMessage, "missing_message")
		assertSameBytes(t, `{"steps": [{"do": "connect", "provider": "acmevoice"}],  "steps_total": 1}`, a.Plan)
		assertSameBytes(t, `[{"param": "speed", "why": "not allowed"}]`, a.Dropped)
		assert.Equal(t, treg.OpEndpointAccess, res.Meta.Op)
	})
	t.Run("unknown tier and nulls", func(t *testing.T) {
		t.Parallel()
		script := testutil.NewScript(testutil.Step{Body: `{"tier":"future_tier","detail":null,"metered":null,` +
			`"estimated_cost_micro":null,"estimated_cost_usd":null}`})
		res, err := newClient(t, script).EndpointAccess(t.Context(), treg.EndpointAccessRequest{ID: cloneID})
		require.NoError(t, err)
		assert.Equal(t, treg.AccessTier("future_tier"), res.Value.Tier)
		assert.False(t, res.Value.Tier.Known())
		assert.Equal(t, treg.Null[string](), res.Value.Detail, "detail")
		assert.Equal(t, treg.Null[bool](), res.Value.Metered, "metered")
		assert.Equal(t, treg.Null[treg.MicroUSD](), res.Value.EstimatedCostMicro, "estimated_cost_micro")
		assert.Equal(t, treg.Null[treg.Decimal](), res.Value.EstimatedCostUSD, "estimated_cost_usd")
		assert.Equal(t, treg.Optional[treg.Decimal]{}, res.Value.EstimatedCostUSDMin, "estimated_cost_usd_min")
		assert.Nil(t, res.Value.Plan)
		assert.Nil(t, res.Value.Dropped)
	})
}

func TestCatalogStrictDecoding(t *testing.T) {
	t.Parallel()
	search := catalogRead(bind((*treg.CatalogClient).Search, treg.SearchRequest{}))
	endpoint := catalogRead(bind((*treg.CatalogClient).Endpoint, treg.EndpointRequest{ID: cloneID}))
	access := clientRead(bind((*treg.Client).EndpointAccess, treg.EndpointAccessRequest{ID: cloneID}))
	info := catalogRead(bind((*treg.CatalogClient).ServerInfo, treg.ServerInfoRequest{}))
	row := func(extra string) string {
		return `{"query":"a","count":1,"total":1,"hints":[],"results":[{"id":"a.b","name":"x"` + extra + `}]}`
	}
	descriptor := func(example string) string {
		return `{"endpoint":{"id":"a.b"},"provider":{},"siblings":[],"call_template":"","example_response":` + example + `,"hints":[]}`
	}
	meta := `{"public_url":"","github":false,"google":false,"app_version":"","treg_version":"","hub":false,"paused_providers":{}`
	cases := []struct {
		name   string
		body   string
		call   readCall
		reason string
	}{
		{"top_level_duplicate", `{"query":"a","query":"b","count":0,"total":0,"results":[],"hints":[]}`, search, "duplicate_key"},
		{"case_folded_duplicate", `{"query":"a","QUERY":"b","count":0,"total":0,"results":[],"hints":[]}`, search, "duplicate_key"},
		{"escaped_duplicate", testutil.Esc(`{"query":"a","q~u0075ery":"b","count":0,"total":0,"results":[],"hints":[]}`), search, "duplicate_key"},
		{"nested_duplicate", row(`,"name":"y"`), search, "duplicate_key"},
		{"cost_duplicate", row(`,"cost":{"type":"free","type":"per_call"}`), search, "duplicate_key"},
		{"input_island_duplicate", row(`,"input":{"a":1,"a":2,"A":3}`), search, ""},
		{"test_request_island_duplicate", row(`,"test_request":{"q":1,"q":2}`), search, ""},
		{"observed_island_duplicate", row(`,"observed":{"n":1,"n":2}`), search, ""},
		{"cost_table_island_duplicate", row(`,"cost":{"type":"per_call","table":[{"t":1,"t":2}]}`), search, ""},
		{"quoted_count", `{"query":"a","count":"3","total":0,"results":[],"hints":[]}`, search, "decode"},
		{"quoted_score", row(`,"score":"0.5"`), search, "decode"},
		{"quoted_cost_usd", row(`,"cost":{"type":"per_call","usd":"0.01"}`), search, "decode"},
		{"fractional_count", `{"query":"a","count":1.5,"total":0,"results":[],"hints":[]}`, search, "decode"},
		{"example_island_duplicate", descriptor(`{"id":1,"id":2}`), endpoint, ""},
		{"routing_island_duplicate", `{"endpoint":{"id":"a.b"},"provider":{},"siblings":[],"call_template":"",` +
			`"routing":{"k":1,"k":2},"example_response":null,"hints":[]}`, endpoint, ""},
		{"descriptor_duplicate", `{"endpoint":{"id":"a.b","ID":"c.d"},"provider":{},"siblings":[],"call_template":"",` +
			`"example_response":null,"hints":[]}`, endpoint, "duplicate_key"},
		{"quoted_micro", `{"tier":"tool","estimated_cost_micro":"6667"}`, access, "decode"},
		{"fractional_micro", `{"tier":"tool","estimated_cost_micro":6667.5}`, access, "decode"},
		{"quoted_decimal", `{"tier":"tool","estimated_cost_usd":"0.006667"}`, access, "decode"},
		{"plan_island_duplicate", `{"tier":"tool","plan":{"s":1,"s":2},"dropped":[{"p":1,"p":2}]}`, access, ""},
		{"access_duplicate", `{"tier":"tool","tier":"none"}`, access, "duplicate_key"},
		{"repeated_bool", meta + `,"hub":true}`, info, "duplicate_key"},
		{"string_bool", strings.Replace(meta, `"hub":false`, `"hub":"false"`, 1) + `}`, info, "decode"},
		{"quoted_referral_micro", meta + `,"referral":{"referrer_micro":"1","referred_micro":2}}`, info, "decode"},
	}
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: sc.body})
			cat, c := scriptClients(t, script)
			_, _, err := sc.call(t.Context(), cat, c)
			assert.Equal(t, 1, script.Calls())
			if sc.reason == "" {
				require.NoError(t, err, "a raw island keeps its duplicate keys")
				return
			}
			requireCallError(t, err, callErrorWant{Kind: treg.FailureMalformedResponse, Reason: sc.reason, Status: http.StatusOK})
		})
	}
}

func TestCatalogKnownSets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		known bool
		want  bool
	}{
		{"endpoint kind data", treg.EndpointKind("data").Known(), true},
		{"endpoint kind action", treg.EndpointKind("action").Known(), true},
		{"endpoint kind account", treg.EndpointKind("account").Known(), true},
		{"endpoint kind utility", treg.EndpointKind("utility").Known(), true},
		{"endpoint kind routed", treg.EndpointKind("routed").Known(), true},
		{"endpoint kind widget", treg.EndpointKind("widget").Known(), false},
		{"cost type free", treg.CostType("free").Known(), true},
		{"cost type per_call", treg.CostType("per_call").Known(), true},
		{"cost type per_result", treg.CostType("per_result").Known(), true},
		{"cost type per_success", treg.CostType("per_success").Known(), true},
		{"cost type quota_rows", treg.CostType("quota_rows").Known(), true},
		{"cost type per_token", treg.CostType("per_token").Known(), false},
		{"access tier none", treg.AccessTier("none").Known(), true},
		{"access tier restricted", treg.AccessTier("restricted").Known(), true},
		{"access tier tool", treg.AccessTier("tool").Known(), true},
		{"access tier credential", treg.AccessTier("credential").Known(), true},
		{"access tier anonymous", treg.AccessTier("anonymous").Known(), true},
		{"access tier platform", treg.AccessTier("platform").Known(), true},
		{"access tier routed", treg.AccessTier("routed").Known(), true},
		{"access tier empty", treg.AccessTier("").Known(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.known)
		})
	}
}
