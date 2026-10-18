//go:build live

package treg

// Live smoke tests against the hosted gateway. They are excluded from every
// default run by the "live" build tag (see .github/env/90-project.env), and
// each skips unless the owner opts in:
//
//	TREG_LIVE=1 go test -tags live -count=1 -run TestLive .
//
// The default set reads only free, public routes. TREG_TOKEN adds the
// caller's own usage, a free read. The one /call/ test runs only with both
// TREG_LIVE_CALL_ID (a catalog id, GET, no body) and TREG_LIVE_MAX_COST (a
// decimal USD reserve ceiling, sent as X-Treg-Route-Max-Cost), and makes
// exactly one request.

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveOptIn skips t unless TREG_LIVE=1.
func liveOptIn(t *testing.T) {
	t.Helper()
	if os.Getenv("TREG_LIVE") != "1" {
		t.Skip("live tests run only with TREG_LIVE=1")
	}
}

//nolint:paralleltest // live smoke tests run serially, by hand, against the real API
func TestLivePublicCatalog(t *testing.T) {
	liveOptIn(t)
	cat, err := NewCatalogClient(WithTimeout(time.Minute))
	require.NoError(t, err)
	info, err := cat.ServerInfo(t.Context(), ServerInfoRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, info.Value.TregVersion)
	found, err := cat.Search(t.Context(), SearchRequest{Query: "url metrics", Limit: 3})
	require.NoError(t, err)
	require.NotEmpty(t, found.Value.Results)
	desc, err := cat.Endpoint(t.Context(), EndpointRequest{ID: found.Value.Results[0].ID})
	require.NoError(t, err)
	t.Logf("treg %s; first result kind %s, %d siblings", info.Value.TregVersion, desc.Value.Endpoint.Kind, len(desc.Value.Siblings))
}

//nolint:paralleltest // live smoke tests run serially, by hand, against the real API
func TestLiveAccountUsage(t *testing.T) {
	liveOptIn(t)
	token := os.Getenv("TREG_TOKEN")
	if token == "" {
		t.Skip("set TREG_TOKEN to read your own usage")
	}
	c, err := NewClient(token, WithTimeout(time.Minute))
	require.NoError(t, err)
	usage, err := c.Account().Usage(t.Context(), MemberUsageRequest{})
	require.NoError(t, err)
	t.Logf("used today %d, cap %d", usage.Value.UsedToday, usage.Value.Cap)
}

//nolint:paralleltest // live smoke tests run serially, by hand, against the real API
func TestLiveOneCall(t *testing.T) {
	liveOptIn(t)
	token, id, capUSD := os.Getenv("TREG_TOKEN"), os.Getenv("TREG_LIVE_CALL_ID"), os.Getenv("TREG_LIVE_MAX_COST")
	if token == "" || id == "" || capUSD == "" {
		t.Skip("set TREG_TOKEN, TREG_LIVE_CALL_ID and TREG_LIVE_MAX_COST to make one /call/ request")
	}
	maxCost, err := ParseDecimalUSD(capUSD)
	require.NoError(t, err)
	c, err := NewClient(token, WithTimeout(2*time.Minute))
	require.NoError(t, err)
	raw, err := c.CallBytes(t.Context(), CallRequest{
		Target: CatalogEndpoint(EndpointID(id)), Method: http.MethodGet, Controls: Controls{MaxCost: &maxCost},
	})
	require.NoError(t, err)
	t.Logf("status %d, %d bytes, meta %s", raw.StatusCode(), len(raw.Body()), raw.Meta())
}
