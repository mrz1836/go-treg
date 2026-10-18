package treg_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestCredentialScan(t *testing.T) {
	t.Parallel()
	jsonEscaped, err := json.Marshal(testToken + "<")
	require.NoError(t, err)
	tests := []struct {
		name  string
		req   treg.CallRequest
		field string
	}{
		{"header value", treg.CallRequest{Header: http.Header{"X-Key": {"x" + testToken}}}, "CallRequest"},
		{"provider authorization", treg.CallRequest{ProviderAuthorization: "Bearer " + testToken}, "CallRequest"},
		{"query value", treg.CallRequest{Query: url.Values{"k": {testToken}}}, "CallRequest"},
		{"query key", treg.CallRequest{Query: url.Values{testToken: {"v"}}}, "CallRequest"},
		{"raw query", treg.CallRequest{RawQuery: "k=" + testToken}, "CallRequest"},
		{"raw query escaped", treg.CallRequest{RawQuery: "k=" + url.QueryEscape(testToken)}, "CallRequest"},
		{"json body", treg.CallRequest{Method: http.MethodPost, Body: treg.JSONBody(map[string]string{"t": testToken})}, "Body"},
		{"json body escaped form", treg.CallRequest{Method: http.MethodPost, Body: treg.BytesBody("application/json", jsonEscaped)}, "Body"},
		{"bytes body", treg.CallRequest{Method: http.MethodPost, Body: treg.BytesBody("text/plain", []byte("pre"+testToken+"post"))}, "Body"},
		{"bytes body query-escaped", treg.CallRequest{Method: http.MethodPost, Body: treg.BytesBody("text/plain", []byte(url.QueryEscape(testToken)))}, "Body"},
		{"form body", treg.CallRequest{Method: http.MethodPost, Body: treg.FormBody(url.Values{"t": {testToken}})}, "CallRequest"},
		{"tag value", treg.CallRequest{Controls: treg.Controls{Tags: []treg.Tag{{Key: "k", Value: testToken}}}}, "CallRequest"},
		{"idempotency key", treg.CallRequest{Controls: treg.Controls{IdempotencyKey: testToken}}, "CallRequest"},
		{"extra value", treg.CallRequest{Controls: treg.Controls{Extra: []treg.Control{{Name: "X-Treg-New", Value: testToken}}}}, "CallRequest"},
		{"tool path", treg.CallRequest{Target: treg.RegisteredTool("t", "a/"+testToken)}, "CallRequest"},
		{"upstream url", treg.CallRequest{Target: treg.UpstreamURL("https://example.com/" + testToken)}, "CallRequest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Body: `{}`})
			c := newClient(t, script)
			if tt.req.Target.Kind() == 0 {
				tt.req.Target = treg.CatalogEndpoint("x.y.z")
			}
			if tt.req.Method == "" {
				tt.req.Method = http.MethodGet
			}
			_, err := c.CallBytes(t.Context(), tt.req)
			requireValidation(t, err, tt.field, "credential_in_request")
			assert.Zero(t, script.Calls(), "nothing is sent")
			assert.NotContains(t, err.Error(), testToken)
		})
	}
}

func TestCredentialScanSkipsReaderBodies(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	body := []byte(testToken)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Body: treg.ReaderBody("text/plain", bytes.NewReader(body), int64(len(body))),
	})
	require.NoError(t, err, "ReaderBody contents are documented as not scanned")
	assert.Equal(t, 1, script.Calls())
}

func TestCredentialScanFloor(t *testing.T) {
	t.Parallel()
	const short = "tok_short_15byt" // 15 bytes: below the scan floor
	require.Len(t, short, 15)
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c, err := treg.NewClient(short, treg.WithBaseURL(testBaseURL), treg.WithHTTPClient(&http.Client{Transport: script}))
	require.NoError(t, err)
	_, err = c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet, Query: url.Values{"k": {short}},
	})
	require.NoError(t, err, "tokens shorter than 16 bytes are not scanned")
}

func TestCredentialScanTypedReads(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.Catalog().Search(t.Context(), treg.SearchRequest{Query: "find " + testToken})
	requireValidation(t, err, "Request", "credential_in_request")
	_, err = c.Calls().Get(t.Context(), treg.GetCallRequest{Ref: testToken})
	require.ErrorIs(t, err, treg.ErrInvalidRequest)
	assert.Zero(t, script.Calls())
}
