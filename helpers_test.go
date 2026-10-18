package treg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// This file holds what the root package's external tests share: synthetic
// values, client constructors, fixtures, response headers, error and byte
// assertions, typed reads bound to their requests, and the plumbing of the
// authenticated read tests.

// testToken is the synthetic token every test uses. It is longer than the
// credential scan's 16-byte floor, so the scan applies.
const testToken = "trg_test_aaaabbbbaaaabbbbSYNTH"

// testBaseURL is the origin of clients on in-memory transports.
const testBaseURL = "https://treg.test"

// Synthetic values of the catalog fixtures.
const (
	// cloneID is the id of the fixtures' fullest row.
	cloneID = "acmevoice.voice.clone"

	// testOrg is the team slug of the catalog tests' clients.
	testOrg = "team-synthetic"
)

// Synthetic values shared by the authenticated read tests.
const (
	// authReadEmailMarker is the personal email planted in fixtures; it must
	// never print.
	authReadEmailMarker = "secret.person@example.com"

	// authReadCallRef and authReadChildRef are the fixtures' call refs.
	authReadCallRef  = "5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f"
	authReadChildRef = "8e1d7c6b5a4f3e2d1c0b9a8f7e6d5c4b:r0"

	// authReadOrg is the slug sent with WithOrg.
	authReadOrg = "acme-team"

	// authReadAdminOnly is Treg's 403 body for an admin-only read.
	authReadAdminOnly = `{"detail":"admin role in this org is required"}`
)

// newClient returns a Client with testToken and opts whose requests go to to:
// a TLS server such as a *testutil.Server or a *testutil.DropServer, reached
// at its URL over its own client, or any http.RoundTripper, such as a
// *testutil.Script, answering in memory at testBaseURL.
func newClient(tb testing.TB, to any, opts ...treg.Option) *treg.Client {
	tb.Helper()
	c, err := treg.NewClient(testToken, append(clientOptions(tb, to), opts...)...)
	require.NoError(tb, err)
	return c
}

// newCatalog returns a standalone CatalogClient with opts whose requests go
// to to, as newClient's do.
func newCatalog(tb testing.TB, to any, opts ...treg.Option) *treg.CatalogClient {
	tb.Helper()
	c, err := treg.NewCatalogClient(append(clientOptions(tb, to), opts...)...)
	require.NoError(tb, err)
	return c
}

// clientOptions returns the options that send a test client's requests to
// to; see newClient. It fails tb for anything else.
func clientOptions(tb testing.TB, to any) []treg.Option {
	tb.Helper()
	switch to := to.(type) {
	case interface {
		URL() string
		Client() *http.Client
	}:
		return []treg.Option{treg.WithBaseURL(to.URL()), treg.WithHTTPClient(to.Client())}
	case http.RoundTripper:
		return []treg.Option{treg.WithBaseURL(testBaseURL), treg.WithHTTPClient(&http.Client{Transport: to})}
	}
	require.FailNow(tb, "a test client needs a server or an http.RoundTripper", "got %T", to)
	return nil
}

// synthetic returns the contents of testdata/synthetic/<name>.
func synthetic(tb testing.TB, name string) string {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "synthetic", filepath.FromSlash(name)))
	require.NoError(tb, err)
	return string(data)
}

// mustParse parses a URL.
func mustParse(tb testing.TB, raw string) *url.URL {
	tb.Helper()
	u, err := url.Parse(raw)
	require.NoError(tb, err)
	return u
}

// gatewayHeader returns the header of a JSON answer that Treg marked as its
// own with X-Treg-Error: 1. Each call returns a new header, which a test may
// extend.
func gatewayHeader() http.Header {
	return http.Header{"Content-Type": {"application/json"}, "X-Treg-Error": {"1"}}
}

// requireValidation requires err to be a *treg.ValidationError that matches
// treg.ErrInvalidRequest, the sentinel of a request refused before anything
// was sent, and that names field and reason.
func requireValidation(tb testing.TB, err error, field, reason string) {
	tb.Helper()
	requireValidationOf(tb, err, treg.ErrInvalidRequest, field, reason)
}

// requireConfigValidation requires err to be a *treg.ValidationError that
// matches treg.ErrInvalidConfig, the sentinel of a client, option, service or
// stream that cannot be used, and that names field and reason.
func requireConfigValidation(tb testing.TB, err error, field, reason string) {
	tb.Helper()
	requireValidationOf(tb, err, treg.ErrInvalidConfig, field, reason)
}

// requireValidationOf is the body of requireValidation and
// requireConfigValidation: err must match sentinel.
func requireValidationOf(tb testing.TB, err, sentinel error, field, reason string) {
	tb.Helper()
	require.ErrorIs(tb, err, sentinel)
	ve := testutil.RequireErrorAs[*treg.ValidationError](tb, err)
	assert.Equal(tb, field, ve.Field, "the field")
	assert.Equal(tb, reason, ve.Reason, "the reason")
}

// callErrorWant is what requireCallError requires of a *treg.CallError. Each
// field is checked only when it is set, so that a call states just what it
// checks; OutcomeUnknown is the exception, checked always.
type callErrorWant struct {
	// Op is the operation.
	Op treg.Operation

	// Kind is the failure kind; err must match its sentinel too (see
	// failureSentinel). Setting Kind checks Reason as well, which refines it: a
	// want that names a kind names its reason, or none.
	Kind treg.FailureKind

	// Reason is the reason code, checked when it or Kind is set.
	Reason string

	// Status is the HTTP status received. Meta, which describes the response,
	// must hold it too, and the error's Op.
	Status int

	// Marked, when not nil, is whether the response carried X-Treg-Error: 1
	// (GatewayMarked and Meta.GatewayMarked), and so whether an HTTP status
	// failure matches treg.ErrGatewayRefusal. Leave it nil for a saturation
	// refusal, which matches treg.ErrGatewayRefusal unmarked.
	Marked *bool

	// Is is one more sentinel that err must match.
	Is error

	// OutcomeUnknown is checked always, with errors.Is(err,
	// treg.ErrOutcomeUnknown): it is false for typed reads, refusals and 2xx
	// answers, and true for a /call/ that may have executed.
	OutcomeUnknown bool
}

// requireCallError requires err to be a *treg.CallError matching want, and
// returns a copy of it for further checks: a value, which errcheck does not
// take for an error, so that a call that needs nothing more can drop it.
func requireCallError(tb testing.TB, err error, want callErrorWant) treg.CallError {
	tb.Helper()
	ce := testutil.RequireErrorAs[*treg.CallError](tb, err)
	if want.Op != "" {
		assert.Equal(tb, want.Op, ce.Op, "Op")
	}
	if want.Kind != "" {
		assert.Equal(tb, want.Kind, ce.Kind, "Kind")
		if sentinel := failureSentinel(want.Kind); sentinel != nil {
			require.ErrorIs(tb, err, sentinel)
		}
	}
	if want.Kind != "" || want.Reason != "" {
		assert.Equal(tb, want.Reason, ce.Reason, "Reason")
	}
	if want.Status != 0 {
		assert.Equal(tb, want.Status, ce.StatusCode, "StatusCode")
		assert.Equal(tb, want.Status, ce.Meta.StatusCode, "Meta.StatusCode")
		assert.Equal(tb, ce.Op, ce.Meta.Op, "Meta.Op")
	}
	if want.Marked != nil {
		assert.Equal(tb, *want.Marked, ce.GatewayMarked, "GatewayMarked")
		assert.Equal(tb, *want.Marked, ce.Meta.GatewayMarked, "Meta.GatewayMarked")
		if *want.Marked && ce.Kind == treg.FailureHTTPStatus {
			require.ErrorIs(tb, err, treg.ErrGatewayRefusal)
		} else if !*want.Marked {
			require.NotErrorIs(tb, err, treg.ErrGatewayRefusal)
		}
	}
	if want.Is != nil {
		require.ErrorIs(tb, err, want.Is)
	}
	assert.Equal(tb, want.OutcomeUnknown, ce.OutcomeUnknown, "OutcomeUnknown")
	if want.OutcomeUnknown {
		require.ErrorIs(tb, err, treg.ErrOutcomeUnknown)
	} else {
		require.NotErrorIs(tb, err, treg.ErrOutcomeUnknown)
	}
	return *ce
}

// failureSentinel returns the sentinel that a CallError of kind matches, as
// documented on each FailureKind (the SDK's own mapping is unexported, so the
// tests state it independently).
func failureSentinel(kind treg.FailureKind) error {
	switch kind {
	case treg.FailureHTTPStatus:
		return treg.ErrHTTPStatus
	case treg.FailureUnexpectedResponse:
		return treg.ErrUnexpectedResponse
	case treg.FailureMalformedResponse:
		return treg.ErrMalformedResponse
	case treg.FailureResponseTooLarge:
		return treg.ErrResponseTooLarge
	case treg.FailureTransport:
		return treg.ErrTransport
	case treg.FailureTimeout:
		return treg.ErrTimeout
	case treg.FailureCanceled:
		return context.Canceled
	case treg.FailureDeadlineExceeded:
		return context.DeadlineExceeded
	}
	return nil
}

// assertSameBytes checks that got is want byte for byte. Raw islands, Raw
// members and captures promise the bytes as received (whitespace, key order,
// number spellings), which a semantic JSON comparison would not check.
func assertSameBytes(tb testing.TB, want string, got []byte, msgAndArgs ...any) {
	tb.Helper()
	assert.Equal(tb, want, string(got), msgAndArgs...)
}

// assertExactJSON checks that got is the JSON text want, both semantically
// and byte for byte: whitespace, key order and repeated keys as received.
func assertExactJSON(tb testing.TB, want string, got []byte) {
	tb.Helper()
	assert.JSONEq(tb, want, string(got))
	assert.Equal(tb, []byte(want), got, "the bytes as received")
}

// typedRead is a typed read of the service S, such as treg.CallsService or
// *treg.CatalogClient, with its request bound; bind builds one. Call it on a
// service value, or with on, on the service of a Client.
type typedRead[S, T any] func(svc S, ctx context.Context, opts ...treg.CallOption) (*treg.Result[T], error)

// bind returns the read that calls method, a method expression of S such as
// treg.CallsService.List or (*treg.CatalogClient).Search, with req, opts and
// then the options of each call.
func bind[S, Req, T any](
	method func(S, context.Context, Req, ...treg.CallOption) (*treg.Result[T], error), req Req, opts ...treg.CallOption,
) typedRead[S, T] {
	return func(svc S, ctx context.Context, more ...treg.CallOption) (*treg.Result[T], error) {
		return method(svc, ctx, req, append(slices.Clip(opts), more...)...)
	}
}

// on calls r on the service S of c (see serviceOf).
func (r typedRead[S, T]) on(ctx context.Context, c *treg.Client, opts ...treg.CallOption) (*treg.Result[T], error) {
	return r(serviceOf[S](c), ctx, opts...)
}

// erased returns r with its result dropped, so that reads of different
// result types can share a table.
func (r typedRead[S, T]) erased() anyRead[S] {
	return func(svc S, ctx context.Context, opts ...treg.CallOption) error {
		_, err := r(svc, ctx, opts...)
		return err
	}
}

// anyRead is a read of the service S with its request bound and its result
// dropped; typedRead.erased builds one.
type anyRead[S any] func(svc S, ctx context.Context, opts ...treg.CallOption) error

// on calls r on the service S of c (see serviceOf).
func (r anyRead[S]) on(ctx context.Context, c *treg.Client, opts ...treg.CallOption) error {
	return r(serviceOf[S](c), ctx, opts...)
}

// namedRead is a read of the service S under the name of its method.
type namedRead[S any] struct {
	name string
	read anyRead[S]
}

// serviceOf returns the service of type S that c provides: c.Account(),
// c.Calls(), c.Orgs(), c.Tools(), c.Catalog() or c itself. Like those
// methods, it accepts a nil or zero c. Any other S panics.
func serviceOf[S any](c *treg.Client) S {
	var svc any
	switch any(*new(S)).(type) {
	case treg.AccountService:
		svc = c.Account()
	case treg.CallsService:
		svc = c.Calls()
	case treg.OrgsService:
		svc = c.Orgs()
	case treg.ToolsService:
		svc = c.Tools()
	case *treg.CatalogClient:
		svc = c.Catalog()
	case *treg.Client:
		svc = c
	}
	return svc.(S)
}

// authReadBody serves body with 200 over proto, from a TLS server, to the read
// rd of a Client with opts, and returns the decoded result and the one
// request the server recorded.
func authReadBody[S, T any](t *testing.T, proto testutil.Protocol, body string, rd typedRead[S, T], opts ...treg.Option) (
	*treg.Result[T], testutil.Request,
) {
	t.Helper()
	srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Body: body}))
	res, err := rd.on(t.Context(), newClient(t, srv, opts...))
	require.NoError(t, err)
	require.NotNil(t, res)
	return res, testutil.OnlyRequest(t, srv)
}

// authReadStep answers the read rd of a Client with opts with step, in
// memory, and returns its result and error. It requires exactly one request,
// and a result or an error, never both.
func authReadStep[S, T any](t *testing.T, step testutil.Step, rd typedRead[S, T], opts ...treg.Option) (*treg.Result[T], error) {
	t.Helper()
	script := testutil.NewScript(step)
	res, err := rd.on(t.Context(), newClient(t, script, opts...))
	assert.Equal(t, 1, script.Calls(), "exactly one request")
	if err == nil {
		require.NotNil(t, res, "a result without an error")
	} else {
		assert.Nil(t, res, "no result with an error")
	}
	return res, err
}

// authReadStepErr is authReadStep for a read whose result is dropped: it
// returns the read's error.
func authReadStepErr[S any](t *testing.T, step testutil.Step, rd anyRead[S], opts ...treg.Option) error {
	t.Helper()
	script := testutil.NewScript(step)
	err := rd.on(t.Context(), newClient(t, script, opts...))
	assert.Equal(t, 1, script.Calls(), "exactly one request")
	return err
}

// assertAuthReadWire checks the request of a typed read: one GET of wantURL
// (path and query as received), with the token, JSON accepted, no body, and
// X-Treg-Org only when org is set.
func assertAuthReadWire(t *testing.T, r testutil.Request, wantURL, org string) {
	t.Helper()
	assert.Equal(t, http.MethodGet, r.Method)
	assert.Equal(t, wantURL, r.URL)
	assert.Equal(t, []string{testToken}, r.Header.Values("X-Treg-Token"))
	assert.Equal(t, []string{"application/json"}, r.Header.Values("Accept"))
	assert.Empty(t, r.Body)
	assert.False(t, r.GetBody)
	if org == "" {
		assert.Empty(t, r.Header.Values("X-Treg-Org"))
	} else {
		assert.Equal(t, []string{org}, r.Header.Values("X-Treg-Org"))
	}
}

// forEachIdentity runs check in a parallel subtest for each identity a
// Client can call as: "own identity", with no option and org "", and "with
// org", with WithOrg(authReadOrg) and org authReadOrg.
func forEachIdentity(t *testing.T, check func(t *testing.T, org string, opts ...treg.Option)) {
	t.Helper()
	for _, id := range []struct {
		name, org string
		opts      []treg.Option
	}{
		{"own identity", "", nil},
		{"with org", authReadOrg, []treg.Option{treg.WithOrg(authReadOrg)}},
	} {
		t.Run(id.name, func(t *testing.T) {
			t.Parallel()
			check(t, id.org, id.opts...)
		})
	}
}

// checkAuthReadWire runs rd for each identity (see forEachIdentity) against a
// TLS server answering body with 200, and checks its one request with
// assertAuthReadWire.
func checkAuthReadWire[S any](t *testing.T, body string, rd anyRead[S], wantURL string) {
	t.Helper()
	forEachIdentity(t, func(t *testing.T, org string, opts ...treg.Option) {
		t.Helper()
		srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: body}))
		require.NoError(t, rd.on(t.Context(), newClient(t, srv, opts...)))
		assertAuthReadWire(t, testutil.OnlyRequest(t, srv), wantURL, org)
	})
}

// requireAuthReadsNotInitialized calls every read of reads, from ctx and
// from a nil context, on the zero S, on the S of a nil Client and on that of
// a zero Client, and requires ErrInvalidConfig with reason not_initialized.
func requireAuthReadsNotInitialized[S any](ctx context.Context, t *testing.T, reads []namedRead[S]) {
	t.Helper()
	var zero S
	for _, svc := range []struct {
		name string
		svc  S
	}{
		{"zero service", zero},
		{"nil client", serviceOf[S](nil)},
		{"zero client", serviceOf[S](&treg.Client{})},
	} {
		t.Run(svc.name, func(t *testing.T) {
			t.Parallel()
			var nilCtx context.Context
			for _, rd := range reads {
				t.Run(rd.name, func(t *testing.T) {
					t.Parallel()
					requireConfigValidation(t, rd.read(svc.svc, ctx), "client", "not_initialized")
					requireConfigValidation(t, rd.read(svc.svc, nilCtx), "client", "not_initialized")
				})
			}
		})
	}
}

// requireAuthReadsRefuseOptions requires every read of reads, called from
// ctx on a working Client, to refuse the CallJSON-only options, and to refuse
// a nil context before them, without sending anything.
func requireAuthReadsRefuseOptions[S any](ctx context.Context, t *testing.T, reads []namedRead[S]) {
	t.Helper()
	var nilCtx context.Context
	for _, rd := range reads {
		t.Run(rd.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			requireValidation(t, rd.read.on(ctx, c, treg.UseNumber()), "UseNumber", "not_applicable")
			requireValidation(t, rd.read.on(ctx, c, nil, treg.CaptureResponse(true), treg.IgnoreContentType()),
				"IgnoreContentType", "not_applicable")
			requireValidation(t, rd.read.on(nilCtx, c), "ctx", "nil")
			requireValidation(t, rd.read.on(nilCtx, c, treg.CaptureResponse(true)), "ctx", "nil")
			requireValidation(t, rd.read.on(nilCtx, c, treg.UseNumber()), "ctx", "nil")
			assert.Zero(t, script.Calls(), "nothing is sent")
		})
	}
}

// assertAuthReadNeverPrints formats a model in every holding of
// testutil.Shapes that fmt lets a model protect with every verb of
// testutil.FormatVerbs but %p, and encodes each holding with json.Marshal,
// failing t when any output holds a secret. A model held in an unexported
// field, and %p on a non-pointer, bypass Format in fmt itself, so they are
// not checked.
func assertAuthReadNeverPrints[T any](t *testing.T, p *T, secrets ...string) {
	t.Helper()
	for _, h := range testutil.Shapes(p) {
		if h.Unexported {
			continue
		}
		for _, verb := range testutil.FormatVerbs() {
			if verb != "%p" {
				testutil.AssertRedacted(t, fmt.Sprintf(verb, h.Value), secrets, "%s %s", h.Name, verb)
			}
		}
		b, err := json.Marshal(h.Value)
		require.NoError(t, err, h.Name)
		testutil.AssertRedacted(t, string(b), secrets, "%s json", h.Name)
	}
}
