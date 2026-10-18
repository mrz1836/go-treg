package urlmetrics_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/examples/extension/urlmetrics"
)

// The tests use only what an external package has: httptest, the public
// treg API and the adapter. Every value is synthetic.
const (
	testToken = "test-token-0123456789abcdef"
	callRef   = "0123456789abcdef0123456789abcdef"
)

// seen is one request as the fake Treg saw it.
type seen struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// fakeTreg answers every request with one fixed status, header and body, and
// records what it received.
type fakeTreg struct {
	status int
	header http.Header
	body   string

	mu   sync.Mutex
	reqs []seen
}

// ServeHTTP records r and writes the fixed answer.
func (f *fakeTreg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, seen{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	f.mu.Unlock()
	maps.Copy(w.Header(), f.header)
	w.WriteHeader(f.status)
	_, _ = io.WriteString(w, f.body)
}

// requests returns a copy of the requests received so far.
func (f *fakeTreg) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// newClient serves f over TLS and returns a Client that talks to it: the
// pattern any package outside go-treg uses.
func newClient(t *testing.T, f *fakeTreg) *treg.Client {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	c, err := treg.NewClient(testToken, treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client()))
	require.NoError(t, err)
	return c
}

// jsonAnswer returns the headers of a JSON answer carrying a call ref and,
// when cost is not empty, a charge.
func jsonAnswer(cost string) http.Header {
	h := http.Header{"Content-Type": {"application/json"}, "X-Treg-Call-Id": {callRef}}
	if cost != "" {
		h.Set("X-Treg-Cost-Micro", cost)
	}
	return h
}

func TestLookupSendsRequestAndDecodesMetrics(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{
		status: http.StatusOK,
		header: jsonAnswer("13334"),
		body: `{"results":[` +
			`{"page":"example.com/","subdomain":"example.com","root_domain":"example.com","title":"Example Domain",` +
			`"last_crawled":"2026-07-30","http_code":200,"page_authority":57,"domain_authority":93,"spam_score":1,` +
			`"link_propensity":0.0123,"root_domains_to_root_domain":12345,"pages_to_page":987},` +
			`{"page":"www.example.com/page","subdomain":"www.example.com","root_domain":"example.com","title":null,` +
			`"last_crawled":null,"http_code":null,"page_authority":null,"domain_authority":93,"spam_score":null,` +
			`"link_propensity":null,"root_domains_to_root_domain":0}]}`,
	}
	c := newClient(t, f)
	ctl := treg.Controls{
		MaxCost:        new(treg.MicroUSD(20_000)),
		IdempotencyKey: "urlmetrics-job-0001",
		Tags:           []treg.Tag{{Key: "customer", Value: "cust_example"}},
	}

	res, err := urlmetrics.Lookup(t.Context(), c,
		urlmetrics.Request{Targets: []string{"example.com", "https://www.example.com/page"}}, ctl)
	require.NoError(t, err)

	reqs := f.requests()
	require.Len(t, reqs, 1)
	got := reqs[0]
	assert.Equal(t, http.MethodPost, got.Method)
	assert.Equal(t, "/call/moz.web.url.metrics", got.Path)
	assert.JSONEq(t, `{"targets":["example.com","https://www.example.com/page"]}`, string(got.Body))
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, testToken, got.Header.Get("X-Treg-Token"))
	assert.Equal(t, "0.02", got.Header.Get("X-Treg-Route-Max-Cost"))
	assert.Equal(t, "urlmetrics-job-0001", got.Header.Get("Idempotency-Key"))
	assert.Equal(t, "customer=cust_example", got.Header.Get("X-Treg-Meta"))

	require.Len(t, res.Value.Results, 2)
	full, sparse := res.Value.Results[0], res.Value.Results[1]
	assert.Equal(t, "example.com/", full.Page)
	assert.Equal(t, "example.com", full.RootDomain)
	assert.Equal(t, "Example Domain", full.Title)
	assert.Equal(t, "2026-07-30", full.LastCrawled)
	assert.Equal(t, new(200), full.HTTPCode)
	assert.Equal(t, new(57), full.PageAuthority)
	assert.Equal(t, new(93), full.DomainAuthority)
	assert.Equal(t, new(1), full.SpamScore)
	require.NotNil(t, full.LinkPropensity)
	assert.InDelta(t, 0.0123, *full.LinkPropensity, 1e-12)
	assert.Equal(t, new(int64(12345)), full.RootDomainsToRootDomain)

	assert.Equal(t, "www.example.com/page", sparse.Page)
	assert.Empty(t, sparse.Title, "a null string reads as empty")
	assert.Nil(t, sparse.HTTPCode)
	assert.Nil(t, sparse.PageAuthority)
	assert.Nil(t, sparse.SpamScore, "a null number stays nil, apart from zero")
	assert.Nil(t, sparse.LinkPropensity)
	assert.Equal(t, new(int64(0)), sparse.RootDomainsToRootDomain, "zero is a value, not null")

	assert.Equal(t, treg.OpCall, res.Meta.Op)
	assert.Equal(t, http.StatusOK, res.Meta.StatusCode)
	assert.Equal(t, callRef, res.Meta.CallRef)
	assert.Equal(t, new(treg.MicroUSD(13_334)), res.Meta.CostMicro)
}

func TestLookupAcceptsFiftyTargets(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{status: http.StatusOK, header: jsonAnswer(""), body: `{"results":[]}`}
	c := newClient(t, f)
	targets := slices.Repeat([]string{"example.com"}, urlmetrics.MaxTargets)

	res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: targets}, treg.Controls{})
	require.NoError(t, err)
	assert.Empty(t, res.Value.Results)
	assert.Nil(t, res.Meta.CostMicro, "no X-Treg-Cost-Micro means unknown, never zero")

	reqs := f.requests()
	require.Len(t, reqs, 1)
	var body urlmetrics.Request
	require.NoError(t, json.Unmarshal(reqs[0].Body, &body))
	assert.Len(t, body.Targets, urlmetrics.MaxTargets)
	assert.Empty(t, reqs[0].Header.Values("X-Treg-Route-Max-Cost"), "no control is invented")
	assert.Empty(t, reqs[0].Header.Values("Idempotency-Key"), "no key is invented")
}

func TestLookupRejectsTargetsWithoutSending(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		targets []string
	}{
		{name: "nil targets", targets: nil},
		{name: "zero targets", targets: []string{}},
		{name: "51 targets", targets: slices.Repeat([]string{"example.com"}, urlmetrics.MaxTargets+1)},
		{name: "blank target", targets: []string{"example.com", " \t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTreg{status: http.StatusOK, header: jsonAnswer(""), body: `{"results":[]}`}
			c := newClient(t, f)

			res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: tt.targets}, treg.Controls{})
			require.ErrorIs(t, err, urlmetrics.ErrTargets)
			assert.Nil(t, res)
			assert.Empty(t, f.requests(), "nothing is sent")
		})
	}
}

func TestLookupPassesSDKValidationErrorThrough(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{status: http.StatusOK, header: jsonAnswer(""), body: `{"results":[]}`}
	c := newClient(t, f)

	res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: []string{"example.com"}},
		treg.Controls{MaxCost: new(treg.MicroUSD(-1))})
	require.ErrorIs(t, err, treg.ErrInvalidRequest)
	var ve *treg.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "Controls.MaxCost", ve.Field)
	assert.Equal(t, "negative", ve.Reason)
	assert.Nil(t, res)
	assert.Empty(t, f.requests(), "a ValidationError means nothing was sent")
}

func TestLookupInsufficientBalanceIsGatewayRefusal(t *testing.T) {
	t.Parallel()
	h := jsonAnswer("")
	h.Set("X-Treg-Error", "1")
	f := &fakeTreg{
		status: http.StatusPaymentRequired,
		header: h,
		body:   `{"detail":{"error":"insufficient_balance","balance_micro":0,"estimated_cost_micro":6667}}`,
	}
	c := newClient(t, f)

	res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: []string{"example.com"}}, treg.Controls{})
	require.EqualError(t, err, "treg: call: http_status (HTTP 402, gateway): insufficient_balance")
	require.ErrorIs(t, err, treg.ErrGatewayRefusal)
	require.ErrorIs(t, err, treg.ErrHTTPStatus)
	require.NotErrorIs(t, err, treg.ErrOutcomeUnknown, "a refusal did not execute")
	assert.Nil(t, res)

	var ce *treg.CallError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, http.StatusPaymentRequired, ce.StatusCode)
	assert.True(t, ce.GatewayMarked)
	require.NotNil(t, ce.Refusal)
	assert.Equal(t, "insufficient_balance", ce.Refusal.Code)
	assert.Equal(t, new(treg.MicroUSD(0)), ce.Refusal.BalanceMicro)
	assert.Equal(t, new(treg.MicroUSD(6667)), ce.Refusal.EstimatedCostMicro)
	assert.Equal(t, callRef, ce.Meta.CallRef)
	assert.Len(t, f.requests(), 1)
}

func TestLookupServerErrorIsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{
		status: http.StatusInternalServerError,
		header: http.Header{"Content-Type": {"text/plain"}, "X-Treg-Call-Id": {callRef}},
		body:   "upstream failed",
	}
	c := newClient(t, f)

	res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: []string{"example.com"}},
		treg.Controls{IdempotencyKey: "urlmetrics-job-0002"})
	require.ErrorIs(t, err, treg.ErrOutcomeUnknown)
	require.ErrorIs(t, err, treg.ErrHTTPStatus)
	require.NotErrorIs(t, err, treg.ErrGatewayRefusal)
	assert.Nil(t, res)

	var ce *treg.CallError
	require.ErrorAs(t, err, &ce)
	assert.True(t, ce.OutcomeUnknown)
	assert.Equal(t, callRef, ce.Meta.CallRef, "the ref to reconcile with Calls().Get")
	assert.Len(t, f.requests(), 1, "exactly one attempt: nothing retries")
}

func TestLookupUndecodableSuccessExecuted(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{status: http.StatusOK, header: jsonAnswer("6667"), body: `{"results":{"page":"example.com/"}}`}
	c := newClient(t, f)

	res, err := urlmetrics.Lookup(t.Context(), c, urlmetrics.Request{Targets: []string{"example.com"}}, treg.Controls{})
	require.ErrorIs(t, err, treg.ErrMalformedResponse)
	require.NotErrorIs(t, err, treg.ErrOutcomeUnknown, "a 2xx executed: the outcome is known")
	assert.Nil(t, res)

	var ce *treg.CallError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, callRef, ce.Meta.CallRef)
	assert.Equal(t, new(treg.MicroUSD(6667)), ce.Meta.CostMicro, "the call was charged: do not resubmit")
}

func ExampleLookup() {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Treg-Call-Id", "0123456789abcdef0123456789abcdef")
		w.Header().Set("X-Treg-Cost-Micro", "6667")
		_, _ = io.WriteString(w, `{"results":[{"page":"example.com/","domain_authority":93,"spam_score":null}]}`)
	}))
	defer srv.Close()
	client, err := treg.NewClient("test-token-0123456789abcdef", treg.WithBaseURL(srv.URL),
		treg.WithHTTPClient(srv.Client()))
	if err != nil {
		panic(err)
	}

	res, err := urlmetrics.Lookup(context.Background(), client,
		urlmetrics.Request{Targets: []string{"example.com"}},
		treg.Controls{MaxCost: new(treg.MicroUSD(20_000))}) // the caller's own ceiling: $0.02
	if err != nil {
		panic(err)
	}
	m := res.Value.Results[0]
	fmt.Println(m.Page, *m.DomainAuthority, m.SpamScore == nil)
	fmt.Println(res.Meta.CallRef, res.Meta.CostMicro.DecimalUSD())
	// Output:
	// example.com/ 93 true
	// 0123456789abcdef0123456789abcdef 0.006667
}
