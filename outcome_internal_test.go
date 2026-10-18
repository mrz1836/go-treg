package treg

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg/internal/testutil"
)

const internalToken = "trg_internal_aaaabbbbaaaabbbb"

// asOwnTransport returns a Client on hc whose settings say it uses the SDK's
// own transport, so that the WroteHeaders evidence decides the outcome.
func asOwnTransport(t *testing.T, base string, hc *http.Client) *Client {
	t.Helper()
	c, err := NewClient(internalToken, WithBaseURL(base), WithHTTPClient(hc))
	require.NoError(t, err)
	c.cfg().injected = false
	return c
}

// requireDirectCallError requires err to be a *CallError itself, as the SDK
// returns it, not one wrapped in another error, and returns it.
func requireDirectCallError(t *testing.T, err error) *CallError {
	t.Helper()
	ce := testutil.RequireErrorAs[*CallError](t, err)
	require.Same(t, ce, err, "the SDK returns *CallError directly")
	return ce
}

func TestOutcomeOwnTransportBeforeHeaders(t *testing.T) {
	t.Parallel()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			_ = conn.Close() // before any TLS handshake: nothing was ever sent
		}
	}()
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	c := asOwnTransport(t, "https://"+ln.Addr().String(), hc)
	_, err = c.CallBytes(t.Context(), CallRequest{
		Target: CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Body: JSONBody(1),
	})
	ce := requireDirectCallError(t, err)
	assert.Equal(t, FailureTransport, ce.Kind)
	assert.False(t, ce.OutcomeUnknown, "the own transport never wrote the request headers")
}

func TestOutcomeOwnTransportAfterHeaders(t *testing.T) {
	t.Parallel()
	drop := testutil.NewDropServer(t, testutil.Step{Body: `{}`}, func(int) bool { return true })
	c := asOwnTransport(t, drop.URL(), drop.Client())
	_, err := c.CallBytes(t.Context(), CallRequest{
		Target: CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Body: JSONBody(1),
	})
	ce := requireDirectCallError(t, err)
	assert.True(t, ce.OutcomeUnknown, "WroteHeaders fired before the failure")
	assert.Equal(t, 1, drop.Hits())
}

func TestOutcomeInjectedWithoutEvidence(t *testing.T) {
	t.Parallel()
	fail := testutil.Step{Err: io.ErrUnexpectedEOF}
	script := testutil.NewScript(fail, fail)
	c, err := NewClient(internalToken, WithBaseURL("https://treg.test"), WithHTTPClient(&http.Client{Transport: script}))
	require.NoError(t, err)
	_, err = c.CallBytes(t.Context(), CallRequest{Target: CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	assert.True(t, requireDirectCallError(t, err).OutcomeUnknown)

	c.cfg().injected = false
	_, err = c.CallBytes(t.Context(), CallRequest{Target: CatalogEndpoint("x.y.z"), Method: http.MethodGet})
	assert.False(t, requireDirectCallError(t, err).OutcomeUnknown,
		"the same failure on the own transport, without WroteHeaders, is known")
	assert.Equal(t, 2, script.Calls(), "one attempt per call")
}
