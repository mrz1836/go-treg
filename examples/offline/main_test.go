package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
)

// Paths of the walkthrough's four requests.
const (
	pathSearch  = "/catalog/search"
	pathMetrics = "/call/moz.web.url.metrics"
	pathAudio   = "/call/fishaudio.tts.s2-1-pro"
	pathPaid    = "/call/vendor.paid.thing"
)

// Output of the walkthrough's first three steps.
const (
	outFound   = "found: moz.web.url.metrics\n"
	outMetrics = "domain authority: 91 cost: $0.006667\n"
	outAudio   = "audio bytes: 20\n"
)

// fakeWith serves fakeTreg, except that path answers with h.
func fakeWith(path string, h http.HandlerFunc) http.Handler {
	fake := fakeTreg()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			h(w, r)
			return
		}
		fake.ServeHTTP(w, r)
	})
}

// serve starts h on a local TLS server and returns the options that point a
// Client at it.
func serve(t *testing.T, h http.Handler) []treg.Option {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return []treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}
}

// answer returns a handler that answers status with a body of contentType.
func answer(status int, contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	require.NoError(t, run(t.Context(), &out))
	assert.Equal(t, outFound+outMetrics+outAudio+
		"refused: insufficient_balance gateway: true\n", out.String())
}

func TestWalkthroughStopsOnUnexpectedOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		answer  http.HandlerFunc
		extra   []treg.Option
		wantErr error
		wantOut string
	}{
		{
			name:    "invalid client option",
			extra:   []treg.Option{treg.WithTimeout(-1)},
			wantErr: treg.ErrInvalidConfig,
		},
		{
			name:    "search fails",
			path:    pathSearch,
			answer:  answer(http.StatusInternalServerError, "application/json", `{}`),
			wantErr: treg.ErrHTTPStatus,
		},
		{
			name: "search finds nothing",
			path: pathSearch,
			answer: answer(http.StatusOK, "application/json",
				`{"query":"url metrics","count":0,"total":0,"hints":[],"results":[]}`),
			wantErr: errNoResults,
		},
		{
			name: "call refused",
			path: pathMetrics,
			answer: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Treg-Error", "1")
				answer(http.StatusPaymentRequired, "application/json", `{"detail":{"error":"route_max_cost"}}`)(w, r)
			},
			wantErr: treg.ErrGatewayRefusal,
			wantOut: outFound,
		},
		{
			name:    "call returns no rows",
			path:    pathMetrics,
			answer:  answer(http.StatusOK, "application/json", `{"results":[]}`),
			wantErr: errNoMetrics,
			wantOut: outFound,
		},
		{
			name:    "call returns a null domain authority",
			path:    pathMetrics,
			answer:  answer(http.StatusOK, "application/json", `{"results":[{"page":"example.com/","domain_authority":null}]}`),
			wantErr: errNoMetrics,
			wantOut: outFound,
		},
		{
			name: "stream connection dropped",
			path: pathAudio,
			answer: func(http.ResponseWriter, *http.Request) {
				panic(http.ErrAbortHandler) // no response at all
			},
			wantErr: treg.ErrTransport,
			wantOut: outFound + outMetrics,
		},
		{
			name:    "stream answers an error status",
			path:    pathAudio,
			answer:  answer(http.StatusServiceUnavailable, "application/json", `{"message":"busy"}`),
			wantErr: treg.ErrHTTPStatus,
			wantOut: outFound + outMetrics,
		},
		{
			name:    "stream exceeds its bound",
			extra:   []treg.Option{treg.WithMaxStreamBytes(4)},
			wantErr: treg.ErrResponseTooLarge,
			wantOut: outFound + outMetrics,
		},
		{
			name:    "paid call succeeds",
			path:    pathPaid,
			answer:  answer(http.StatusOK, "application/json", `{"ok":true}`),
			wantErr: errNotRefused,
			wantOut: outFound + outMetrics + outAudio,
		},
		{
			name:    "paid call fails without a refusal",
			path:    pathPaid,
			answer:  answer(http.StatusBadGateway, "text/plain", "upstream failed"),
			wantErr: treg.ErrOutcomeUnknown,
			wantOut: outFound + outMetrics + outAudio,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := fakeTreg()
			if tt.path != "" {
				handler = fakeWith(tt.path, tt.answer)
			}
			opts := append(serve(t, handler), tt.extra...)
			var out strings.Builder

			err := walkthrough(t.Context(), &out, opts...)
			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantOut, out.String(), "each step prints only after it succeeds")
			assert.NotContains(t, out.String(), "refused:", "only an actual refusal is reported as one")
		})
	}
}
