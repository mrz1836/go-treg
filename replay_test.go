package treg_test

import (
	"bytes"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// replayCase is one request shape the replay tests send.
type replayCase struct {
	name string
	req  treg.CallRequest
	// body returns a fresh body for each send, since a reader body is
	// consumed; nil for a request without one.
	body func() treg.Body
	// emptyWrite marks an empty POST, PUT or PATCH: the documented residual.
	emptyWrite bool
}

// request returns the case's request with a fresh body.
func (rc replayCase) request() treg.CallRequest {
	req := rc.req
	if rc.body != nil {
		req.Body = rc.body()
	}
	return req
}

func replayCases() []replayCase {
	tgt := treg.CatalogEndpoint("x.y.z")
	jsonBody := func() treg.Body { return treg.JSONBody(map[string]int{"n": 1}) }
	return []replayCase{
		{name: "GET", req: treg.CallRequest{Target: tgt, Method: http.MethodGet}},
		{name: "GET with idempotency key", req: treg.CallRequest{
			Target: tgt, Method: http.MethodGet,
			Controls: treg.Controls{IdempotencyKey: "k1"},
		}},
		{name: "DELETE", req: treg.CallRequest{Target: tgt, Method: http.MethodDelete}},
		{name: "POST json", req: treg.CallRequest{Target: tgt, Method: http.MethodPost}, body: jsonBody},
		{name: "POST json with idempotency key", req: treg.CallRequest{
			Target: tgt, Method: http.MethodPost,
			Controls: treg.Controls{IdempotencyKey: "k2"},
		}, body: jsonBody},
		{name: "PUT reader", req: treg.CallRequest{Target: tgt, Method: http.MethodPut}, body: func() treg.Body {
			return treg.ReaderBody("text/plain", strings.NewReader("abc"), 3)
		}},
		{name: "POST empty with idempotency key", req: treg.CallRequest{
			Target: tgt, Method: http.MethodPost,
			Controls: treg.Controls{IdempotencyKey: "k3"},
		}, emptyWrite: true},
	}
}

// TestReplayNeverOverHTTP1 uses the HTTP/1 resend that the DropServer's own
// control test (TestDropServerDetectsResends in internal/testutil) proves
// the harness detects. The servers are real sockets, which a synctest bubble
// cannot hold, so the bound on the time taken is a wall-clock one.
func TestReplayNeverOverHTTP1(t *testing.T) {
	t.Parallel()
	for _, tt := range replayCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := testutil.NewDropServer(t, testutil.Step{Body: `{}`}, func(n int) bool { return n == 2 })
			counter := &testutil.CountingTransport{Next: srv.Client().Transport}
			c := newClient(t, srv, treg.WithHTTPClient(&http.Client{Transport: counter}), treg.WithTimeout(10*time.Second))
			_, err := c.CallBytes(t.Context(), tt.request())
			require.NoError(t, err, "the first call leaves an idle keep-alive connection")

			start := time.Now()
			_, err = c.CallBytes(t.Context(), tt.request())
			// The server read the request, so the outcome is unknown. The
			// reason, which depends on how the platform reports the dropped
			// connection, is not checked.
			ce := requireCallError(t, err, callErrorWant{Is: treg.ErrTransport, OutcomeUnknown: true})
			assert.Equal(t, treg.FailureTransport, ce.Kind)
			assert.Equal(t, 2, srv.Hits(), "the dropped request reaches the server once")
			assert.Equal(t, 1, srv.Conns(), "no second connection is dialed to resend")
			assert.Equal(t, 2, counter.Attempts(), "one SDK dispatch per call")
			assert.Less(t, time.Since(start), 5*time.Second)
		})
	}
}

// h2ResendsEmptyWrite reports whether the standard HTTP/2 client resends an
// empty write (http.NoBody), the documented residual, after fault: it does
// after GOAWAY or REFUSED_STREAM, which guarantee the first attempt was not
// processed, and Go 1.26 also after a peer PROTOCOL_ERROR, which does not.
func h2ResendsEmptyWrite(fault testutil.H2Fault) bool {
	return fault != testutil.H2ProtocolError || strings.HasPrefix(runtime.Version(), "go1.26")
}

// TestReplayNeverOverHTTP2 faults each request's stream. Its servers are real
// sockets, which a synctest bubble cannot hold.
func TestReplayNeverOverHTTP2(t *testing.T) {
	t.Parallel()
	for _, fault := range testutil.H2Faults() {
		for _, tt := range replayCases() {
			t.Run(string(fault)+"_"+tt.name, func(t *testing.T) {
				t.Parallel()
				srv := testutil.NewH2Server(t, fault)
				counter := &testutil.CountingTransport{Next: srv.Transport()}
				c := newClient(t, counter, treg.WithBaseURL(srv.URL()), treg.WithTimeout(10*time.Second))
				_, err := c.CallBytes(t.Context(), tt.request())
				assert.Equal(t, 1, counter.Attempts(), "one SDK dispatch")
				if tt.emptyWrite && h2ResendsEmptyWrite(fault) {
					require.NoError(t, err)
					assert.Equal(t, 2, srv.Headers(), "the empty write was resent once")
					return
				}
				require.Error(t, err, "a request with a non-rewindable body, or a fault that may follow processing, is never resent")
				assert.Equal(t, 1, srv.Headers(), "HEADERS frames: the request was sent once")
			})
		}
	}
}

func TestReplayRequestsCannotBeRewound(t *testing.T) {
	t.Parallel()
	for _, tt := range replayCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var seen *http.Request
			c := newClient(t, testutil.RoundTripFunc(func(r *http.Request) (*http.Response, error) {
				seen = r
				testutil.Drain(r)
				return testutil.Response(r, 200, io.NopCloser(bytes.NewReader([]byte(`{}`))), 2), nil
			}))
			_, err := c.CallBytes(t.Context(), tt.request())
			require.NoError(t, err)
			require.NotNil(t, seen)
			assert.Nil(t, seen.GetBody, "no GetBody: nothing can rewind the body")
			if tt.emptyWrite {
				assert.Equal(t, http.NoBody, seen.Body, "the documented residual")
			} else {
				assert.NotNil(t, seen.Body)
				assert.NotEqual(t, http.NoBody, seen.Body)
			}
			assert.NotContains(t, seen.Header, "Idempotency-Key", "never the canonical spelling isReplayable checks")
		})
	}
}
