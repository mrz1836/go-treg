package treg_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// getRequest returns a valid bodyless GET call.
func getRequest() treg.CallRequest {
	return treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet}
}

// statusErrorMethods lists the ways to get the error of a non-2xx /call/
// answer from c: CallJSON's own error, and the StatusError of CallBytes and
// of Call, which closes the stream.
func statusErrorMethods() []struct {
	name      string
	statusErr func(t *testing.T, c *treg.Client) error
} {
	return []struct {
		name      string
		statusErr func(t *testing.T, c *treg.Client) error
	}{
		{"CallJSON", func(t *testing.T, c *treg.Client) error {
			t.Helper()
			_, err := treg.CallJSON[any](t.Context(), c, getRequest())
			return err
		}},
		{"CallBytes", func(t *testing.T, c *treg.Client) error {
			t.Helper()
			raw, err := c.CallBytes(t.Context(), getRequest())
			require.NoError(t, err)
			return raw.StatusError()
		}},
		{"Call", func(t *testing.T, c *treg.Client) error {
			t.Helper()
			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			serr := resp.StatusError(t.Context())
			_, rerr := resp.Read(make([]byte, 1))
			require.Error(t, rerr, "StatusError closed the stream")
			return serr
		}},
	}
}

func TestStatusInformational(t *testing.T) {
	t.Parallel()
	for _, m := range callMethods(getRequest()) {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBlockingBody("")
			c := newClient(t, testutil.TransportWith(http.StatusSwitchingProtocols, http.Header{"Upgrade": {"websocket"}}, -1, body))
			requireCallError(t, m.call(c, t.Context()), callErrorWant{
				Op: treg.OpCall, Kind: treg.FailureUnexpectedResponse, Reason: "informational", Status: http.StatusSwitchingProtocols,
			})
			assert.Zero(t, body.Reads(), "the raw connection is never read")
			assert.Positive(t, body.Closes(), "it is closed")
		})
	}
}

func TestStatusRedirect(t *testing.T) {
	t.Parallel()
	const location = "https://evil.example/x"
	methods := []struct {
		name  string
		check func(t *testing.T, c *treg.Client, code int)
	}{
		{"CallBytes", func(t *testing.T, c *treg.Client, code int) {
			t.Helper()
			raw, err := c.CallBytes(t.Context(), getRequest())
			require.NoError(t, err)
			assert.Equal(t, code, raw.StatusCode())
			assert.Nil(t, raw.Body(), "a 3xx body is never read")
			assert.Equal(t, location, raw.Header().Get("Location"))
			requireCallError(t, raw.StatusError(), callErrorWant{Kind: treg.FailureUnexpectedResponse, Reason: "redirect"})
		}},
		{"CallJSON", func(t *testing.T, c *treg.Client, _ int) {
			t.Helper()
			_, err := treg.CallJSON[any](t.Context(), c, getRequest())
			require.ErrorIs(t, err, treg.ErrUnexpectedResponse)
		}},
		{"Call", func(t *testing.T, c *treg.Client, _ int) {
			t.Helper()
			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			assert.Equal(t, location, resp.Header().Get("Location"))
			require.ErrorIs(t, resp.StatusError(t.Context()), treg.ErrUnexpectedResponse)
			require.NoError(t, resp.Close())
		}},
	}
	for _, code := range []int{301, 302, 303, 304, 307, 308, 300} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()
			for _, m := range methods {
				t.Run(m.name, func(t *testing.T) {
					t.Parallel()
					header := http.Header{"Location": {location}, "Content-Type": {"application/json"}}
					body := testutil.NewBody(`{"moved":true}`)
					m.check(t, newClient(t, testutil.TransportWith(code, header, -1, body)), code)
					assert.Zero(t, body.Reads(), "a 3xx body is never read, nor does StatusError read it")
				})
			}
		})
	}
}

func TestStatusSuccessAndErrors(t *testing.T) {
	t.Parallel()
	callRef := strings.Repeat("ab", 16)
	for _, code := range []int{200, 201, 202, 400, 402, 404, 429, 500, 502, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()
			payload := `{"detail":{"error":"insufficient_balance","balance_micro":5,"estimated_cost_micro":20000}}`
			if code < 300 {
				payload = `{"ok":true}`
			}
			header := gatewayHeader()
			header.Set("X-Treg-Call-Id", callRef)
			step := testutil.Step{Status: code, Body: payload, Header: header}
			c := newClient(t, testutil.NewScript(step, step, step))

			raw, err := c.CallBytes(t.Context(), getRequest())
			require.NoError(t, err, "any 2xx-5xx is a RawResponse")
			assert.Equal(t, code, raw.StatusCode())
			assert.Equal(t, payload, string(raw.Body()))

			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			assert.Equal(t, code, resp.StatusCode())

			res, err := treg.CallJSON[map[string]any](t.Context(), c, getRequest())
			if code < 300 {
				require.NoError(t, raw.StatusError())
				require.NoError(t, resp.StatusError(t.Context()))
				require.NoError(t, err)
				assert.Equal(t, true, res.Value["ok"])
				assert.Equal(t, code, res.Meta.StatusCode)
				require.NoError(t, resp.Close())
				return
			}
			for _, failure := range []struct {
				name string
				err  error
			}{
				{"CallBytes", raw.StatusError()},
				{"Call", resp.StatusError(t.Context())},
				{"CallJSON", err},
			} {
				t.Run(failure.name, func(t *testing.T) {
					t.Parallel()
					ce := requireCallError(t, failure.err, callErrorWant{
						Op: treg.OpCall, Kind: treg.FailureHTTPStatus, Status: code, Marked: new(true), OutcomeUnknown: code >= 500,
					})
					require.NotNil(t, ce.Refusal)
					assert.Equal(t, "insufficient_balance", ce.Refusal.Code)
					assert.Equal(t, callRef, ce.Meta.CallRef)
					assert.Nil(t, ce.Body(), "error bodies are kept only with WithErrorBodies")
				})
			}
		})
	}
}

func TestStatusCallJSONEmptyAndHead(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		step testutil.Step
	}{
		{"no content", testutil.Step{Status: http.StatusNoContent}},
		{"blank body", testutil.Step{Status: http.StatusOK, Body: " \n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(tt.step))
			_, err := treg.CallJSON[any](t.Context(), c, getRequest())
			// OutcomeUnknown is false: a 2xx executed, so do not resubmit.
			requireCallError(t, err, callErrorWant{Kind: treg.FailureUnexpectedResponse, Reason: "empty_body"})
		})
	}
	t.Run("HEAD", func(t *testing.T) {
		t.Parallel()
		script := testutil.NewScript()
		c := newClient(t, script)
		_, err := treg.CallJSON[any](t.Context(), c, treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodHead})
		requireValidation(t, err, "Method", "not_applicable")
		assert.Zero(t, script.Calls())
	})
}

func TestStatusErrorExcerptBound(t *testing.T) {
	t.Parallel()
	big := `{"detail":{"error":"route_failed"},"pad":"` + strings.Repeat("p", 200<<10) + `"}`
	body := testutil.NewBody(big)
	c := newClient(t, testutil.TransportWith(http.StatusBadGateway, nil, -1, body))
	_, err := treg.CallJSON[any](t.Context(), c, getRequest())
	ce := requireCallError(t, err, callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusBadGateway, OutcomeUnknown: true})
	assert.Nil(t, ce.Refusal, "a body over 64 KiB is cut, so it does not parse")
	assert.LessOrEqual(t, body.BytesRead(), int64(64<<10), "at most 64 KiB is read")
}

func TestStatusErrorBodiesOption(t *testing.T) {
	t.Parallel()
	payload := `{"detail":"admin role in this org is required"}`
	for _, m := range statusErrorMethods() {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{Status: http.StatusForbidden, Body: payload}), treg.WithErrorBodies())
			ce := requireCallError(t, m.statusErr(t, c), callErrorWant{
				Kind: treg.FailureHTTPStatus, Status: http.StatusForbidden, Marked: new(false),
			})
			assert.Equal(t, payload, string(ce.Body()))
			assert.Nil(t, ce.Refusal, "a string detail is never parsed into a code")
			assert.NotContains(t, ce.Error(), "admin")
		})
	}
}

func TestStatusErrorBodyIsNotTheRawBody(t *testing.T) {
	t.Parallel()
	payload := `{"detail":{"error":"insufficient_balance"}}`
	c := newClient(t, testutil.NewScript(testutil.Step{Status: http.StatusPaymentRequired, Body: payload}), treg.WithErrorBodies())
	raw, err := c.CallBytes(t.Context(), getRequest())
	require.NoError(t, err)
	ce := requireCallError(t, raw.StatusError(), callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusPaymentRequired})
	body := raw.Body()
	require.Equal(t, payload, string(body))
	for i := range body {
		body[i] = 'x'
	}
	assert.Equal(t, payload, string(ce.Body()), "the error body is a copy: the caller owns RawResponse.Body")
}

func TestStatusBusinessFailureIsSuccess(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{
		Header: http.Header{"Content-Type": {"application/json"}, "X-Treg-Upstream-Failed": {"status_code=40501"}},
		Body:   `{"status_code":40501,"status_message":"invalid field"}`,
	}))
	res, err := treg.CallJSON[map[string]any](t.Context(), c, getRequest())
	require.NoError(t, err, "a provider failure inside a 2xx is provider data")
	assert.InDelta(t, 40501, res.Value["status_code"], 0)
	assert.True(t, res.Meta.UpstreamFailed)
}

func TestStatusNonJSONError(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{
		Status: http.StatusTeapot, Header: http.Header{"Content-Type": {"text/html"}}, Body: "<html>teapot</html>",
	}))
	_, err := treg.CallJSON[any](t.Context(), c, getRequest())
	ce := requireCallError(t, err, callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusTeapot, Marked: new(false)})
	assert.Nil(t, ce.Refusal)
	assert.Equal(t, "treg: call: http_status (HTTP 418)", err.Error())
}

func TestStatusGatewayMarkOnEveryFailure(t *testing.T) {
	t.Parallel()
	marked := func(ct string) http.Header { return http.Header{"Content-Type": {ct}, "X-Treg-Error": {"1"}} }
	cases := []struct {
		name string
		run  func(t *testing.T) error
		text string
	}{
		{"decode", func(t *testing.T) error {
			t.Helper()
			c := newClient(t, testutil.NewScript(testutil.Step{Header: marked("application/json")}))
			_, err := treg.CallJSON[any](t.Context(), c, getRequest())
			return err
		}, "treg: call: unexpected_response (HTTP 200, gateway): empty_body"},
		{"find response", func(t *testing.T) error {
			t.Helper()
			_, err := findOver(t, http.StatusOK, marked("text/html"), testutil.NewBody("{}")).
				Find(t.Context(), treg.FindRequest{Query: findQuery})
			return err
		}, "treg: catalog_find: unexpected_response (HTTP 200, gateway): content_type"},
		{"find line", func(t *testing.T) error {
			t.Helper()
			s, err := findOver(t, http.StatusOK, marked("application/x-ndjson"), testutil.NewBody("[]\n")).
				Find(t.Context(), treg.FindRequest{Query: findQuery})
			require.NoError(t, err)
			_, errs := drainFind(s)
			require.Len(t, errs, 1)
			return errs[0]
		}, "treg: catalog_find: malformed_response (HTTP 200, gateway): invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.run(t)
			// Marked: set whenever the response carried X-Treg-Error: 1.
			requireCallError(t, err, callErrorWant{Marked: new(true)})
			assert.Equal(t, tc.text, err.Error())
			require.NotErrorIs(t, err, treg.ErrGatewayRefusal, "only an HTTP status failure is a refusal")
		})
	}
}

func TestStatusReadIsBounded(t *testing.T) {
	t.Parallel()
	body := &testutil.EndlessBody{}
	c := newClient(t, testutil.TransportWith(http.StatusOK, nil, -1, body), treg.WithMaxResponseBytes(1<<10))
	_, err := c.CallBytes(t.Context(), getRequest())
	require.ErrorIs(t, err, treg.ErrResponseTooLarge)
	assert.LessOrEqual(t, body.BytesRead(), int64(64<<10))
}
