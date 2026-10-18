package main

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
)

// The tests are offline: every request goes to a local TLS server, the
// token is a parameter, and no environment variable is read or set. Every
// value is synthetic.
const (
	testToken  = "test-token-0123456789abcdef"
	searchBody = `{"query":"url metrics","count":2,"total":9,"hints":[],"results":[` +
		`{"id":"moz.web.url.metrics","kind":"data","method":"POST","cost":{"type":"per_call","usd":0.006667}},` +
		`{"id":"vendor.web.url.lookup","kind":"data","method":"GET"}]}`
	endpointBody = `{"endpoint":{"id":"moz.web.url.metrics","method":"POST","path":"/v1/url-metrics",` +
		`"verified":"2026-09-20"},"siblings":[],"hints":[]}`
	usageBody    = `{"org":"acme-team","used_today":17,"cap":500}`
	pathSearch   = "/catalog/search"
	pathEndpoint = "/catalog/endpoints/moz.web.url.metrics"
	pathUsage    = "/usage/me"
)

// seen is one request as the fake Treg saw it.
type seen struct {
	Path     string
	Query    string
	HasToken bool
}

// fakeTreg answers each path with its handler and records every request.
type fakeTreg struct {
	routes map[string]http.HandlerFunc

	mu   sync.Mutex
	reqs []seen
}

// ServeHTTP records r and hands it to its route, or answers 404.
func (f *fakeTreg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, seen{Path: r.URL.Path, Query: r.URL.RawQuery, HasToken: r.Header.Get("X-Treg-Token") != ""})
	f.mu.Unlock()
	if h, ok := f.routes[r.URL.Path]; ok {
		h(w, r)
		return
	}
	http.NotFound(w, r)
}

// requests returns a copy of the requests received so far.
func (f *fakeTreg) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// paths returns the paths of the requests received so far.
func (f *fakeTreg) paths() []string {
	reqs := f.requests()
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Path)
	}
	return out
}

// jsonAnswer returns a handler that answers status with body.
func jsonAnswer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// healthyRoutes answers the three reads the quickstart makes.
func healthyRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		pathSearch:   jsonAnswer(http.StatusOK, searchBody),
		pathEndpoint: jsonAnswer(http.StatusOK, endpointBody),
		pathUsage:    jsonAnswer(http.StatusOK, usageBody),
	}
}

// serve starts f on a local TLS server and returns the options that point
// both of the quickstart's clients at it.
func serve(t *testing.T, f *fakeTreg) []treg.Option {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return []treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}
}

// noEnv is a getenv that records the names it is asked for and finds none.
type noEnv struct {
	mu    sync.Mutex
	names []string
}

// get records name and returns "".
func (e *noEnv) get(name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names = append(e.names, name)
	return ""
}

// asked returns the names asked for so far.
func (e *noEnv) asked() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.names)
}

// What run writes to stdout, step by step, against healthyRoutes.
const (
	outResults = "results: 2 of 9\n" +
		"  moz.web.url.metrics data per_call 0.006667\n" +
		"  vendor.web.url.lookup data  \n"
	outFirst   = "first: POST /v1/url-metrics verified 2026-09-20\n"
	outUsage   = "usage today: 17 cap: 500\n"
	outSkipped = "usage: skipped (set TREG_TOKEN to read your own usage)\n"
	outDryRun  = "dry run: pass -live to search the public catalog (and read your usage if TREG_TOKEN is set)\n"
)

func TestRunReadsCatalogAndUsage(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{routes: healthyRoutes()}
	opts := serve(t, f)
	var out strings.Builder

	require.NoError(t, run(t.Context(), &out, "url metrics", testToken, opts...))
	assert.Equal(t, outResults+outFirst+outUsage, out.String())

	reqs := f.requests()
	require.Len(t, reqs, 3, "one request per read: nothing retries")
	assert.Equal(t, seen{Path: pathSearch, Query: "limit=5&q=url+metrics"}, reqs[0], "the catalog never gets the token")
	assert.Equal(t, seen{Path: pathEndpoint}, reqs[1], "the first result's descriptor, without the token")
	assert.Equal(t, seen{Path: pathUsage, HasToken: true}, reqs[2])
	assert.NotContains(t, out.String(), testToken)
}

func TestRunWithoutTokenSkipsUsage(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{routes: healthyRoutes()}
	opts := serve(t, f)
	var out strings.Builder

	require.NoError(t, run(t.Context(), &out, "url metrics", "", opts...))
	assert.Equal(t, outResults+outFirst+outSkipped, out.String())
	assert.Equal(t, []string{pathSearch, pathEndpoint}, f.paths(), "no usage request without a token")
}

func TestRunNoResultsStopsAfterSearch(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{routes: map[string]http.HandlerFunc{
		pathSearch: jsonAnswer(http.StatusOK, `{"query":"nothing","count":0,"total":0,"hints":[],"results":[]}`),
	}}
	opts := serve(t, f)
	var out strings.Builder

	require.NoError(t, run(t.Context(), &out, "nothing", testToken, opts...))
	assert.Equal(t, "results: 0 of 0\n", out.String())
	assert.Equal(t, []string{pathSearch}, f.paths())
}

func TestRunReportsEachFailingStep(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		override  map[string]http.HandlerFunc
		token     string
		extra     []treg.Option
		wantErr   error
		wantText  []string
		wantOut   string
		wantPaths []string
	}{
		{
			name:      "catalog client options refused",
			extra:     []treg.Option{treg.WithOrg("acme-team")},
			wantErr:   treg.ErrInvalidConfig,
			wantText:  []string{"catalog client: "},
			wantPaths: []string{},
		},
		{
			name:      "search answers 503",
			override:  map[string]http.HandlerFunc{pathSearch: jsonAnswer(http.StatusServiceUnavailable, `{}`)},
			wantErr:   treg.ErrHTTPStatus,
			wantText:  []string{"search: ", "(HTTP 503)"},
			wantPaths: []string{pathSearch},
		},
		{
			name:      "endpoint answers 404",
			override:  map[string]http.HandlerFunc{pathEndpoint: jsonAnswer(http.StatusNotFound, `{}`)},
			wantErr:   treg.ErrHTTPStatus,
			wantText:  []string{"endpoint: ", "(HTTP 404)"},
			wantOut:   outResults,
			wantPaths: []string{pathSearch, pathEndpoint},
		},
		{
			name:      "token refused locally",
			token:     "not a token",
			wantErr:   treg.ErrInvalidConfig,
			wantText:  []string{"client: "},
			wantOut:   outResults + outFirst,
			wantPaths: []string{pathSearch, pathEndpoint},
		},
		{
			name: "usage refused by Treg",
			override: map[string]http.HandlerFunc{pathUsage: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Treg-Error", "1")
				jsonAnswer(http.StatusUnauthorized, `{"detail":{"error":"authorization_required"}}`)(w, r)
			}},
			wantErr:   treg.ErrGatewayRefusal,
			wantText:  []string{"usage: ", "(HTTP 401, gateway)", "authorization_required"},
			wantOut:   outResults + outFirst,
			wantPaths: []string{pathSearch, pathEndpoint, pathUsage},
		},
		{
			name: "usage transport failure",
			override: map[string]http.HandlerFunc{pathUsage: func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler) // drops the connection: no HTTP status at all
			}},
			wantErr:   treg.ErrTransport,
			wantText:  []string{"usage: "},
			wantOut:   outResults + outFirst,
			wantPaths: []string{pathSearch, pathEndpoint, pathUsage},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			routes := healthyRoutes()
			maps.Copy(routes, tt.override)
			f := &fakeTreg{routes: routes}
			opts := append(serve(t, f), tt.extra...)
			token := testToken
			if tt.token != "" {
				token = tt.token
			}
			var out strings.Builder

			err := run(t.Context(), &out, "url metrics", token, opts...)
			require.ErrorIs(t, err, tt.wantErr)
			for _, want := range tt.wantText {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), "status 0", "no status is printed when Treg sent none")
			assert.NotContains(t, err.Error(), testToken)
			assert.Equal(t, tt.wantOut, out.String(), "run reports what it read and returns the error unprinted")
			assert.Equal(t, tt.wantPaths, f.paths(), "each read is one request, and nothing after a failure")
			if ce, ok := errors.AsType[*treg.CallError](err); ok && ce.StatusCode == 0 {
				assert.NotContains(t, err.Error(), "HTTP", "a failure without a response names no status")
			}
		})
	}
}

func TestRealMainDryRunMakesNoRequest(t *testing.T) {
	t.Parallel()
	env := &noEnv{}
	var stdout, stderr strings.Builder

	assert.Equal(t, exitOK, realMain(nil, env.get, &stdout, &stderr))
	assert.Equal(t, outDryRun, stdout.String())
	assert.Empty(t, stderr.String())
	assert.Empty(t, env.asked(), "the token is read only with -live")
}

func TestRealMainArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr []string
	}{
		{
			name:       "unknown flag",
			args:       []string{"-nope"},
			wantCode:   exitUsage,
			wantStderr: []string{"flag provided but not defined: -nope\n", "Usage of quickstart:", "-live", "-query"},
		},
		{
			name:       "stray argument",
			args:       []string{"url metrics"},
			wantCode:   exitUsage,
			wantStderr: []string{"quickstart: unexpected arguments; see -h\n"},
		},
		{
			name:       "help",
			args:       []string{"-h"},
			wantCode:   exitOK,
			wantStderr: []string{"Usage of quickstart:", "-live", "-query"},
		},
		{
			name:       "query without -live",
			args:       []string{"-query", "voice"},
			wantCode:   exitOK,
			wantStdout: outDryRun,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := &noEnv{}
			var stdout, stderr strings.Builder

			assert.Equal(t, tt.wantCode, realMain(tt.args, env.get, &stdout, &stderr))
			assert.Equal(t, tt.wantStdout, stdout.String(), "stdout holds only the program's report")
			if len(tt.wantStderr) == 0 {
				assert.Empty(t, stderr.String())
			}
			for _, want := range tt.wantStderr {
				assert.Contains(t, stderr.String(), want)
			}
			assert.Empty(t, env.asked())
		})
	}
}

func TestRealMainLiveReportsErrorAndExitCode(t *testing.T) {
	t.Parallel()
	env := &noEnv{}
	var stdout, stderr strings.Builder

	// A query over Treg's 500-byte bound is refused before anything is
	// sent, so -live stays offline here.
	code := realMain([]string{"-live", "-query", strings.Repeat("a", 501)}, env.get, &stdout, &stderr)
	assert.Equal(t, exitError, code)
	assert.Equal(t, []string{"TREG_TOKEN"}, env.asked(), "the example, not the SDK, reads the token")
	assert.Empty(t, stdout.String(), "the error goes to stderr only")
	assert.True(t, strings.HasPrefix(stderr.String(), "quickstart: search: "), stderr.String())
	assert.True(t, strings.HasSuffix(stderr.String(), "too_long\n"), stderr.String())
	assert.Equal(t, 1, strings.Count(stderr.String(), "\n"), "one line")
	assert.NotContains(t, stderr.String(), "status 0")
}
