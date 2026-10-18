package treg_test

import (
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestRedirectsNeverFollowed(t *testing.T) {
	t.Parallel()
	other := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: `{"stolen":true}`}))
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, target := range []struct{ name, loc string }{
			{"same origin", "/call/other.endpoint"},
			{"cross origin", other.URL() + "/steal"},
			{"unparsable", "http://[::1%zz/x"},
		} {
			t.Run(strconv.Itoa(code)+"_"+target.name, func(t *testing.T) {
				t.Parallel()
				srv := testutil.NewServer(t, testutil.Serve(testutil.Step{
					Status: code, Header: http.Header{"Location": {target.loc}},
				}))
				permissive := srv.Client()
				permissive.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
				c := newClient(t, srv, treg.WithHTTPClient(permissive))

				raw, err := c.CallBytes(t.Context(), treg.CallRequest{
					Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost, Body: treg.JSONBody(1),
				})
				require.NoError(t, err)
				assert.Equal(t, code, raw.StatusCode())
				assert.Equal(t, target.loc, raw.Header().Get("Location"), "Location is only in the sensitive header clone")
				assert.Equal(t, 1, srv.Hits())
				assert.Zero(t, other.Hits(), "the token never reaches another URL")

				_, err = treg.CallJSON[any](t.Context(), c, treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
				require.ErrorIs(t, err, treg.ErrUnexpectedResponse)
				assert.NotContains(t, err.Error(), target.loc)
				ce := testutil.RequireErrorAs[*treg.CallError](t, err)
				assert.NotContains(t, ce.GoString(), target.loc)
			})
		}
	}
}

func TestCookiesNeverKept(t *testing.T) {
	t.Parallel()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{
		Header: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"treg_session=abc; Path=/"}}, Body: `{}`,
	}))
	hc := srv.Client()
	hc.Jar = jar
	c := newClient(t, srv, treg.WithHTTPClient(hc))
	for range 2 {
		raw, err := c.CallBytes(t.Context(), getRequest())
		require.NoError(t, err)
		assert.Equal(t, "treg_session=abc; Path=/", raw.Header().Get("Set-Cookie"), "Set-Cookie only in Header()")
	}
	for _, r := range srv.Requests() {
		assert.Empty(t, r.Header.Get("Cookie"), "no cookie is ever sent")
	}
	assert.Empty(t, jar.Cookies(mustParse(t, srv.URL())), "the caller's jar is untouched")
}

// TestProxyEnvironmentIgnored dials a closed local port: a real socket, which
// a synctest bubble cannot hold, so its timeout is a wall-clock bound. It is
// not parallel: t.Setenv sets the proxy variables for the whole process.
func TestProxyEnvironmentIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	c, err := treg.NewClient(testToken, treg.WithBaseURL("https://127.0.0.1:1"), treg.WithTimeout(5e9))
	require.NoError(t, err)
	_, err = c.CallBytes(t.Context(), getRequest())
	// A direct dial to the origin, not a proxy connection; nothing was sent.
	requireCallError(t, err, callErrorWant{Kind: treg.FailureTransport, Reason: "dial"})
}
