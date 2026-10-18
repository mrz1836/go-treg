package treg_test

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestNewClientToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		token  string
		reason string
	}{
		{"empty", "", "required"},
		{"space", "abc def", "invalid_character"},
		{"newline", "abc\n", "control_character"},
		{"non-ascii", "tökén", "invalid_character"},
		{"too long", strings.Repeat("t", 4097), "too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := treg.NewClient(tt.token)
			assert.Nil(t, c)
			requireConfigValidation(t, err, "token", tt.reason)
			if tt.token != "" {
				assert.NotContains(t, err.Error(), tt.token)
			}
		})
	}
	c, err := treg.NewClient(strings.Repeat("t", 4096))
	require.NoError(t, err)
	assert.NotNil(t, c)
}

func TestNewClientOptions(t *testing.T) {
	t.Parallel()
	hc := &http.Client{}
	tests := []struct {
		name   string
		opts   []treg.Option
		field  string
		reason string
	}{
		{"nil http client", []treg.Option{treg.WithHTTPClient(nil)}, "HTTPClient", "nil"},
		{"empty org", []treg.Option{treg.WithOrg("")}, "Org", "required"},
		{"org control", []treg.Option{treg.WithOrg("team\r\nX: y")}, "Org", "control_character"},
		{"org non-ascii", []treg.Option{treg.WithOrg("équipe")}, "Org", "invalid_character"},
		{"org spaces", []treg.Option{treg.WithOrg(" team")}, "Org", "surrounding_whitespace"},
		{"empty user agent", []treg.Option{treg.WithUserAgent("")}, "UserAgent", "required"},
		{"user agent control", []treg.Option{treg.WithUserAgent("a\x00")}, "UserAgent", "control_character"},
		{"user agent DEL", []treg.Option{treg.WithUserAgent("a\x7f")}, "UserAgent", "control_character"},
		{"user agent non-ascii", []treg.Option{treg.WithUserAgent("agent/1.0 \xff")}, "UserAgent", "invalid_character"},
		{"zero timeout", []treg.Option{treg.WithTimeout(0)}, "Timeout", "out_of_range"},
		{"negative timeout", []treg.Option{treg.WithTimeout(-time.Second)}, "Timeout", "out_of_range"},
		{"timeout too long", []treg.Option{treg.WithTimeout(31 * time.Minute)}, "Timeout", "out_of_range"},
		{"zero stream timeout", []treg.Option{treg.WithStreamTimeout(0)}, "StreamTimeout", "out_of_range"},
		{"stream timeout too long", []treg.Option{treg.WithStreamTimeout(13 * time.Hour)}, "StreamTimeout", "out_of_range"},
		{"zero request bytes", []treg.Option{treg.WithMaxRequestBytes(0)}, "MaxRequestBytes", "out_of_range"},
		{"request bytes too big", []treg.Option{treg.WithMaxRequestBytes(4<<30 + 1)}, "MaxRequestBytes", "out_of_range"},
		{"zero response bytes", []treg.Option{treg.WithMaxResponseBytes(0)}, "MaxResponseBytes", "out_of_range"},
		{"response bytes too big", []treg.Option{treg.WithMaxResponseBytes(256<<20 + 1)}, "MaxResponseBytes", "out_of_range"},
		{"zero stream bytes", []treg.Option{treg.WithMaxStreamBytes(0)}, "MaxStreamBytes", "out_of_range"},
		{"stream bytes too big", []treg.Option{treg.WithMaxStreamBytes(64<<30 + 1)}, "MaxStreamBytes", "out_of_range"},
		{"empty base url", []treg.Option{treg.WithBaseURL("")}, "BaseURL", "invalid_url"},
		{"http base url", []treg.Option{treg.WithBaseURL("http://treg.example")}, "BaseURL", "invalid_url"},
		{"base url path", []treg.Option{treg.WithBaseURL("https://treg.example/api")}, "BaseURL", "invalid_url"},
		{"base url query", []treg.Option{treg.WithBaseURL("https://treg.example?x=1")}, "BaseURL", "invalid_url"},
		{"base url fragment", []treg.Option{treg.WithBaseURL("https://treg.example#f")}, "BaseURL", "invalid_url"},
		{"base url userinfo", []treg.Option{treg.WithBaseURL("https://u:p@treg.example")}, "BaseURL", "invalid_url"},
		{"base url scheme case", []treg.Option{treg.WithBaseURL("HTTPS://treg.example")}, "BaseURL", "invalid_url"},
		{"base url bad host", []treg.Option{treg.WithBaseURL("https://tr eg.example")}, "BaseURL", "invalid_url"},
		{"base url bad port", []treg.Option{treg.WithBaseURL("https://treg.example:99999")}, "BaseURL", "invalid_url"},
		{"base url numeric tld", []treg.Option{treg.WithBaseURL("https://999.1.1.1")}, "BaseURL", "invalid_url"},
		{"base url zone", []treg.Option{treg.WithBaseURL("https://[fe80::1%25en0]")}, "BaseURL", "invalid_url"},
		{"first error wins", []treg.Option{treg.WithTimeout(0), treg.WithHTTPClient(nil)}, "HTTPClient", "nil"},
		{"last option wins", []treg.Option{treg.WithHTTPClient(nil), treg.WithHTTPClient(hc), treg.WithTimeout(0)}, "Timeout", "out_of_range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := treg.NewClient(testToken, tt.opts...)
			assert.Nil(t, c)
			requireConfigValidation(t, err, tt.field, tt.reason)
		})
	}
}

func TestNewClientValidOptions(t *testing.T) {
	t.Parallel()
	c, err := treg.NewClient(testToken, nil,
		treg.WithBaseURL("https://treg.example/"),
		treg.WithBaseURL("https://127.0.0.1:8443"),
		treg.WithBaseURL("https://[::1]:8443"),
		treg.WithTimeout(30*time.Minute), treg.WithStreamTimeout(12*time.Hour),
		treg.WithMaxRequestBytes(1), treg.WithMaxResponseBytes(256<<20), treg.WithMaxStreamBytes(64<<30),
		treg.WithOrg("example-team"), treg.WithUserAgent("x"), treg.WithResponseCapture(), treg.WithErrorBodies(),
	)
	require.NoError(t, err)
	s := c.String()
	assert.Contains(t, s, "baseURL: custom")
	assert.Contains(t, s, "timeout: 30m0s")
	assert.Contains(t, s, "org: set")
	assert.Contains(t, s, "capture: true")
	assert.Contains(t, s, "errorBodies: true")
	assert.NotContains(t, s, "example-team")
	assert.NotContains(t, s, "127.0.0.1")
	assert.NotContains(t, s, "[::1]")
	assert.Equal(t, s, fmt.Sprintf("%#v", c))
}

func TestNewCatalogClientApplicability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		opt   treg.Option
		field string
	}{
		{"org", treg.WithOrg("team"), "Org"},
		{"max request", treg.WithMaxRequestBytes(10), "MaxRequestBytes"},
		{"max stream", treg.WithMaxStreamBytes(10), "MaxStreamBytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := treg.NewCatalogClient(tt.opt)
			assert.Nil(t, c)
			requireConfigValidation(t, err, tt.field, "not_applicable")
		})
	}
	c, err := treg.NewCatalogClient(treg.WithBaseURL("https://treg.example"), treg.WithStreamTimeout(time.Minute),
		treg.WithResponseCapture(), treg.WithErrorBodies(), treg.WithUserAgent("ua"), treg.WithTimeout(time.Second),
		treg.WithMaxResponseBytes(1<<10), treg.WithHTTPClient(&http.Client{}))
	require.NoError(t, err)
	assert.Contains(t, c.String(), "treg.CatalogClient{")
	assert.NotContains(t, c.String(), "org:")
	assert.Equal(t, c.String(), fmt.Sprintf("%#v", c))
}

func TestStringNilAndZero(t *testing.T) {
	t.Parallel()
	var c *treg.Client
	assert.Equal(t, "treg.Client(nil)", c.String())
	assert.Equal(t, "treg.Client(uninitialized)", (&treg.Client{}).String())
	var cc *treg.CatalogClient
	assert.Equal(t, "treg.CatalogClient(nil)", cc.String())
	assert.Equal(t, "treg.CatalogClient(uninitialized)", (&treg.CatalogClient{}).GoString())
}

func TestInjectedClientIsCopied(t *testing.T) {
	t.Parallel()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	redirects := 0
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{
		Status: http.StatusFound,
		Header: http.Header{"Location": {"https://evil.example/steal"}, "Set-Cookie": {"s=1; Path=/"}},
	}))
	hc := srv.Client()
	hc.Jar = jar
	hc.Timeout = time.Minute
	hc.CheckRedirect = func(*http.Request, []*http.Request) error {
		redirects++
		return nil
	}
	before := *hc
	c := newClient(t, srv, treg.WithHTTPClient(hc))
	resp, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode())
	assert.Equal(t, "https://evil.example/steal", resp.Header().Get("Location"), "Location is kept in the sensitive header clone")
	assert.Zero(t, redirects, "the caller's redirect policy is never consulted")
	assert.Equal(t, 1, srv.Hits(), "the redirect is not followed")
	assert.Same(t, jar, hc.Jar, "the caller's client is not modified")
	assert.Equal(t, time.Minute, hc.Timeout)
	assert.Equal(t, before.Transport, hc.Transport)
	assert.Empty(t, jar.Cookies(mustParse(t, srv.URL())), "the SDK's copy keeps no cookies")
}

// TestInjectedTimeoutCapsDeadlines runs in a testing/synctest bubble, over an
// in-memory transport, so that the injected timeout elapses on the bubble's
// fake clock, exactly.
func TestInjectedTimeoutCapsDeadlines(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := testutil.NewScript(testutil.Step{Respond: testutil.BlockUntilDone})
		c := newClient(t, script)
		cc := newClient(t, script, treg.WithHTTPClient(&http.Client{Transport: script, Timeout: 20 * time.Millisecond}))
		assert.Contains(t, cc.String(), "timeout: 20ms")
		assert.Contains(t, cc.String(), "streamTimeout: 20ms")
		start := time.Now()
		_, err := cc.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
		require.ErrorIs(t, err, treg.ErrTimeout)
		assert.Equal(t, 20*time.Millisecond, time.Since(start), "the call ends exactly at the injected timeout")
		assert.NotContains(t, c.String(), "20ms")
	})
}

// TestNilTransportUsesOwnTransport is not parallel: t.Setenv sets the proxy
// variables for the whole process.
func TestNilTransportUsesOwnTransport(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: `{}`}))
	c, err := treg.NewClient(testToken, treg.WithBaseURL(srv.URL()), treg.WithHTTPClient(&http.Client{}))
	require.NoError(t, err)
	_, err = c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	// The SDK's own transport ignores the proxy variables and does not trust
	// the test server's certificate: a TLS failure, not a proxy failure, which
	// proves nothing was sent (the outcome is known).
	requireCallError(t, err, callErrorWant{Kind: treg.FailureTransport, Reason: "tls"})
	assert.Zero(t, srv.Hits())
}

func TestCloseIdleConnections(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: `{}`}))
	c := newClient(t, srv)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	require.NoError(t, err)
	c.CloseIdleConnections()
	(*treg.Client)(nil).CloseIdleConnections()
	(&treg.Client{}).CloseIdleConnections()
	_, err = c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	require.NoError(t, err)
}
