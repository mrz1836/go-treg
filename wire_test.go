package treg_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestWireMethods(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		for _, method := range []string{
			http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodOptions,
		} {
			t.Run(proto.Name()+"_"+method, func(t *testing.T) {
				t.Parallel()
				srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Body: `{}`}))
				c := newClient(t, srv)
				resp, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: method})
				require.NoError(t, err)
				require.NoError(t, resp.StatusError())
				r := testutil.OnlyRequest(t, srv)
				assert.Equal(t, method, r.Method)
				assert.Equal(t, "/call/x.y.z", r.URL)
				assert.Empty(t, r.Body)
				assert.Equal(t, string(proto), r.Proto)
				assert.Equal(t, testToken, r.Header.Get("X-Treg-Token"))
				assert.Equal(t, "go-treg", r.Header.Get("User-Agent"))
				assert.Empty(t, r.Header.Get("X-Treg-Org"))
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("Cookie"))
				assert.Empty(t, r.Header.Get("Content-Type"))
			})
		}
	}
}

func TestWireRequestBodies(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			binary := make([]byte, 256)
			for i := range binary {
				binary[i] = byte(i)
			}
			var mp bytes.Buffer
			mw := multipart.NewWriter(&mp)
			fw, err := mw.CreateFormFile("voice", "sample.mp3")
			require.NoError(t, err)
			_, err = fw.Write(binary)
			require.NoError(t, err)
			require.NoError(t, mw.Close())

			tests := []struct {
				name string
				body treg.Body
				ct   string
				want []byte
			}{
				{
					"json", treg.JSONBody(map[string]any{"b": []int{1, 2}, "a": "x<y"}), "application/json",
					[]byte(`{"a":"x\u003cy","b":[1,2]}`),
				},
				{"json null", treg.JSONBody(nil), "application/json", []byte("null")},
				{
					"form", treg.FormBody(url.Values{"z": {"1", "2"}, "a": {"x y"}}), "application/x-www-form-urlencoded",
					[]byte("a=x+y&z=1&z=2"),
				},
				{"bytes binary", treg.BytesBody("application/octet-stream", binary), "application/octet-stream", binary},
				{"multipart", treg.BytesBody(mw.FormDataContentType(), mp.Bytes()), mw.FormDataContentType(), mp.Bytes()},
				{"reader", treg.ReaderBody("audio/mpeg", bytes.NewReader(binary), int64(len(binary))), "audio/mpeg", binary},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Body: `{}`}))
					c := newClient(t, srv)
					_, err := c.CallBytes(t.Context(), treg.CallRequest{
						Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost, Body: tt.body,
					})
					require.NoError(t, err)
					r := testutil.OnlyRequest(t, srv)
					assert.Equal(t, tt.want, r.Body)
					assert.Equal(t, tt.ct, r.Header.Get("Content-Type"))
					assert.Equal(t, int64(len(tt.want)), r.ContentLength)
				})
			}
		})
	}
}

func TestWireEmptyWrites(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		method string
		// noBody is whether the request carries http.NoBody: an empty write
		// does, with Content-Length: 0, and a bodyless request carries a
		// non-rewindable empty body instead.
		noBody bool
	}{
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodDelete, false},
		{http.MethodOptions, false},
	} {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: `{}`})
			c := newClient(t, script)
			_, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: tt.method})
			require.NoError(t, err)
			r := testutil.OnlyRequest(t, script)
			assert.Equal(t, tt.noBody, r.NoBody)
			assert.Equal(t, int64(0), r.ContentLength)
			assert.False(t, r.GetBody)
		})
	}
}

func TestWireQuery(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query url.Values
		raw   string
		want  string
	}{
		{"none", nil, "", testBaseURL + "/call/x.y.z"},
		{
			"sorted with repeats and blanks",
			url.Values{"limit": {"5"}, "id": {"b", "a"}, "empty": {""}},
			"",
			testBaseURL + "/call/x.y.z?empty=&id=b&id=a&limit=5",
		},
		{"escaped", url.Values{"q": {"a b&c=d/é"}}, "", testBaseURL + "/call/x.y.z?q=a+b%26c%3Dd%2F%C3%A9"},
		{
			"json array value",
			url.Values{"ad_reached_countries": {`["US"]`}},
			"",
			testBaseURL + "/call/x.y.z?ad_reached_countries=%5B%22US%22%5D",
		},
		{"raw byte for byte", nil, "b=2&a=%2F&flag&a=1;x", testBaseURL + "/call/x.y.z?b=2&a=%2F&flag&a=1;x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: `{}`})
			c := newClient(t, script)
			_, err := c.CallBytes(t.Context(), treg.CallRequest{
				Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet, Query: tt.query, RawQuery: tt.raw,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, testutil.OnlyRequest(t, script).URL)
		})
	}
}

func TestWireQueryInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		req    treg.CallRequest
		field  string
		reason string
	}{
		{"both", treg.CallRequest{Query: url.Values{"a": {"1"}}, RawQuery: "a=1"}, "RawQuery", "conflict"},
		{"raw fragment", treg.CallRequest{RawQuery: "a=1#f"}, "RawQuery", "invalid_character"},
		{"raw space", treg.CallRequest{RawQuery: "a=1 2"}, "RawQuery", "invalid_character"},
		{"raw control", treg.CallRequest{RawQuery: "a=1\r\n"}, "RawQuery", "control_character"},
		{"raw non-ascii", treg.CallRequest{RawQuery: "a=é"}, "RawQuery", "invalid_character"},
		{"raw bad escape", treg.CallRequest{RawQuery: "a=%zz"}, "RawQuery", "invalid_escape"},
		{"raw truncated escape", treg.CallRequest{RawQuery: "a=%2"}, "RawQuery", "invalid_escape"},
		{"raw too long", treg.CallRequest{RawQuery: "a=" + strings.Repeat("b", 16<<10)}, "RawQuery", "too_long"},
		{"url too long", treg.CallRequest{Query: url.Values{"a": {strings.Repeat("b", 16<<10)}}}, "Target", "too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			tt.req.Target, tt.req.Method = treg.CatalogEndpoint("x.y.z"), http.MethodGet
			_, err := c.CallBytes(t.Context(), tt.req)
			requireValidation(t, err, tt.field, tt.reason)
			assert.Zero(t, script.Calls())
		})
	}
}

func TestWireMethodInvalid(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, method, reason string }{
		{"empty", "", "required"},
		{"lower case", "get", "invalid_method"},
		{"TRACE", "TRACE", "invalid_method"},
		{"CONNECT", "CONNECT", "invalid_method"},
		{"PROPFIND", "PROPFIND", "invalid_method"},
		{"trailing space", "GET ", "invalid_method"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			_, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: tt.method})
			requireValidation(t, err, "Method", tt.reason)
			assert.Zero(t, script.Calls())
		})
	}
}

func TestWireAuthAndOrg(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: `{}`}))
	c := newClient(t, srv, treg.WithOrg("example-team"), treg.WithUserAgent("acme-bot/1.0 (+ops)"))
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.UpstreamURL("https://api.example.com/v1/things"), Method: http.MethodGet,
		ProviderAuthorization: "Bearer provider-key-123",
	})
	require.NoError(t, err)
	r := testutil.OnlyRequest(t, srv)
	assert.Equal(t, testToken, r.Header.Get("X-Treg-Token"))
	assert.Equal(t, "example-team", r.Header.Get("X-Treg-Org"))
	assert.Equal(t, "acme-bot/1.0 (+ops)", r.Header.Get("User-Agent"))
	assert.Equal(t, "Bearer provider-key-123", r.Header.Get("Authorization"))
	assert.Equal(t, "/call/https://api.example.com/v1/things", r.URL, "the upstream URL is only a path on the Treg origin")
}

func TestWireIdempotencyKeySpelling(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Controls: treg.Controls{IdempotencyKey: "job-42-urlmetrics"},
	})
	require.NoError(t, err)
	r := testutil.OnlyRequest(t, script)
	//nolint:staticcheck // the SDK stores the key non-canonically on purpose
	assert.Equal(t, []string{"job-42-urlmetrics"}, r.Header["Idempotency-key"],
		"stored under the non-canonical key, so net/http's isReplayable never sees it")
	assert.NotContains(t, r.Header, "Idempotency-Key")
	assert.NotContains(t, r.Header, "X-Idempotency-Key")

	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Body: `{}`}))
			sc := newClient(t, srv)
			_, err := sc.CallBytes(t.Context(), treg.CallRequest{
				Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost,
				Controls: treg.Controls{IdempotencyKey: "job-42"},
			})
			require.NoError(t, err)
			assert.Equal(t, "job-42", testutil.OnlyRequest(t, srv).Header.Get("Idempotency-Key"),
				"header names are case-insensitive on the wire")
		})
	}
}

func TestWireControls(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("treg.people.email.find"), Method: http.MethodPost,
		Header: http.Header{"Model": {"s2.1-pro"}, "accept": {"audio/mpeg"}},
		Controls: treg.Controls{
			MaxCost: new(treg.MicroUSD(100_000)),
			Tags:    []treg.Tag{{Key: "customer", Value: "cust_A"}, {Key: "feature", Value: "v1.2:beta-x"}},
			MaxAge:  new(90 * time.Second),
			NoCache: true,
			Route: treg.RouteControls{
				Waterfall: new(false), StrictFilters: true, Verify: true,
				Prefer: []string{"hunter", "prospeo"}, Exclude: []string{"pdl"},
			},
			Extra: []treg.Control{{Name: "X-Treg-Body-Encoding", Value: "base64"}, {Name: "x-treg-route-min-results", Value: "2"}},
		},
	})
	require.NoError(t, err)
	h := testutil.OnlyRequest(t, script).Header
	assert.Equal(t, "0.1", h.Get("X-Treg-Route-Max-Cost"))
	assert.Equal(t, "customer=cust_A,feature=v1.2:beta-x", h.Get("X-Treg-Meta"))
	assert.Equal(t, "90", h.Get("X-Treg-Max-Age"))
	assert.Equal(t, "no-cache", h.Get("Cache-Control"))
	assert.Equal(t, "0", h.Get("X-Treg-Route-Waterfall"))
	assert.Equal(t, "1", h.Get("X-Treg-Route-Strict-Filters"))
	assert.Equal(t, "true", h.Get("X-Treg-Route-Verify"))
	assert.Equal(t, "hunter,prospeo", h.Get("X-Treg-Route-Prefer"))
	assert.Equal(t, "pdl", h.Get("X-Treg-Route-Exclude"))
	assert.Equal(t, "base64", h.Get("X-Treg-Body-Encoding"))
	assert.Equal(t, "2", h.Get("X-Treg-Route-Min-Results"))
	assert.Equal(t, []string{"s2.1-pro"}, h["Model"])
	assert.Equal(t, []string{"audio/mpeg"}, h["accept"], "caller header names are kept as given") //nolint:staticcheck // the caller's spelling is the point
}

func TestWireZeroControlsSendNothing(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet,
		Controls: treg.Controls{MaxCost: new(treg.MicroUSD(0)), MaxAge: new(time.Duration(0)), Route: treg.RouteControls{Waterfall: new(true)}},
	})
	require.NoError(t, err)
	h := testutil.OnlyRequest(t, script).Header
	assert.Equal(t, "0", h.Get("X-Treg-Route-Max-Cost"), "an explicit zero is written")
	assert.Equal(t, "0", h.Get("X-Treg-Max-Age"))
	assert.Equal(t, "1", h.Get("X-Treg-Route-Waterfall"))
	for _, name := range []string{"Idempotency-Key", "X-Treg-Meta", "Cache-Control", "X-Treg-Route-Verify"} {
		assert.Empty(t, h.Get(name), name)
	}
}
