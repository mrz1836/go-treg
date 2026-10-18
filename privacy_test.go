package treg_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// Synthetic markers a test plants in requests and responses; none may ever
// surface in printed or serialized SDK values.
const (
	markerHeader   = "HDRMARKER7"
	markerBody     = "BODYMARKER7"
	markerPath     = "PATHMARKER7"
	markerQuery    = "QUERYMARKER7"
	markerTag      = "TAGMARKER7"
	markerProvider = "provider_marker_code_seven"
	markerAuth     = "PROVAUTHMARKER7"
)

func allMarkers() []string {
	return []string{testToken, markerHeader, markerBody, markerPath, markerQuery, markerTag, markerProvider, markerAuth}
}

// markedRequest is a request carrying every marker.
func markedRequest() treg.CallRequest {
	return treg.CallRequest{
		Target:                treg.RegisteredTool("tool", "v1/"+markerPath),
		Method:                http.MethodPost,
		Query:                 url.Values{"q": {markerQuery}},
		Header:                http.Header{"X-Custom": {markerHeader}},
		ProviderAuthorization: "Bearer " + markerAuth,
		Body:                  treg.JSONBody(map[string]string{"s": markerBody}),
		Controls:              treg.Controls{Tags: []treg.Tag{{Key: "customer", Value: markerTag}}, IdempotencyKey: "IDEM" + markerTag},
	}
}

// assertErrorChainClean checks err's text, GoString, JSON and every error in
// its unwrap chain.
func assertErrorChainClean(t *testing.T, err error, secrets ...string) {
	t.Helper()
	require.Error(t, err)
	testutil.AssertNeverPrints(t, err, secrets...)
	if ce, ok := errors.AsType[*treg.CallError](err); ok {
		testutil.AssertRedacted(t, ce.GoString(), secrets)
		out, jerr := json.Marshal(ce)
		require.NoError(t, jerr)
		testutil.AssertRedacted(t, string(out), secrets)
		// Refusal.Code is exported data: an unknown code never prints through
		// the CallError's own methods, but a caller who holds the error in an
		// unexported field, or walks it by reflection, reaches the field.
		hidden := slices.DeleteFunc(slices.Clone(secrets), func(s string) bool { return s == markerProvider })
		testutil.AssertShapesNeverPrint(t, ce, hidden...)
		for _, verb := range testutil.FormatVerbs() {
			for _, holding := range []any{ce, *ce, []*treg.CallError{ce}, map[string]treg.CallError{"k": *ce}} {
				testutil.AssertRedacted(t, fmt.Sprintf(verb, holding), secrets, "%s on %T", verb, holding)
			}
		}
		for _, s := range testutil.ReflectStrings(ce) {
			testutil.AssertRedacted(t, s, hidden, "reflection reaches only safe strings")
		}
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		testutil.AssertRedacted(t, e.Error(), secrets, "unwrap chain")
		_, isURLErr := errors.AsType[*url.Error](e)
		assert.False(t, isURLErr, "no *url.Error in the chain")
	}
}

func TestPrivacyClient(t *testing.T) {
	t.Parallel()
	c, err := treg.NewClient(testToken, treg.WithOrg("org-"+markerTag), treg.WithBaseURL("https://treg.example"))
	require.NoError(t, err)
	testutil.AssertShapesNeverPrint(t, c, testToken, markerTag)
	for _, s := range testutil.ReflectStrings(c) {
		testutil.AssertRedacted(t, s, []string{testToken}, "the token lives only inside closures")
	}
	cat := c.Catalog()
	testutil.AssertShapesNeverPrint(t, cat, testToken, markerTag)
	for _, s := range testutil.ReflectStrings(cat) {
		testutil.AssertRedacted(t, s, []string{testToken})
	}
}

func TestPrivacyTransportErrors(t *testing.T) {
	t.Parallel()
	failures := []struct {
		name string
		err  error
	}{
		{"url error", &url.Error{
			Op: "Post", URL: testBaseURL + "/call/tool/v1/" + markerPath + "?q=" + markerQuery,
			Err: fmt.Errorf("dial %s: %w", testToken, errTestRefused),
		}},
		{"proxy error", fmt.Errorf("proxyconnect tcp: user:%s@proxy refused %s: %w", testToken, markerHeader, errTestRefused)},
		{"timeout", testutil.TimeoutError{Msg: "timeout reading " + markerBody}},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			t.Parallel()
			for _, m := range callMethods(markedRequest()) {
				t.Run(m.name, func(t *testing.T) {
					t.Parallel()
					c := newClient(t, testutil.NewScript(testutil.Step{Err: failure.err}))
					assertErrorChainClean(t, m.call(c, t.Context()), allMarkers()...)
				})
			}
		})
	}
}

func TestPrivacyResponses(t *testing.T) {
	t.Parallel()
	body := `{"detail":{"error":"` + markerProvider + `","message":"` + markerBody + ` ` + testToken + `"}}`
	header := http.Header{
		"Content-Type": {"application/json"}, "X-Treg-Error": {"1"}, "X-Echo": {testToken},
		"Set-Cookie": {"s=" + markerHeader}, "X-Treg-Served-By": {"https://" + markerPath},
		"X-Treg-Upstream-Failed": {markerQuery}, "Location": {"https://evil.example/" + markerPath},
	}
	step := testutil.Step{Status: 402, Header: header, Body: body}
	c := newClient(t, testutil.NewScript(step, step, step, step), treg.WithErrorBodies(), treg.WithResponseCapture())

	raw, err := c.CallBytes(t.Context(), markedRequest())
	require.NoError(t, err)
	testutil.AssertShapesNeverPrint(t, raw, allMarkers()...)
	out, err := json.Marshal(raw)
	require.NoError(t, err)
	testutil.AssertRedacted(t, string(out), allMarkers())
	meta := raw.Meta()
	testutil.AssertShapesNeverPrint(t, &meta, allMarkers()...)
	serr := raw.StatusError()
	assertErrorChainClean(t, serr, allMarkers()...)
	ce := testutil.RequireErrorAs[*treg.CallError](t, serr)
	assert.Contains(t, string(ce.Body()), markerBody, "the opt-in body keeps the data, reachable only through Body")
	require.NotNil(t, ce.Refusal)
	assert.Equal(t, markerProvider, ce.Refusal.Code, "an unknown code is kept as data but never printed")
	assert.Contains(t, raw.Header().Get("X-Echo"), testToken, "Header() is the sensitive escape hatch")

	resp, err := c.Call(t.Context(), markedRequest())
	require.NoError(t, err)
	testutil.AssertShapesNeverPrint(t, resp, allMarkers()...)
	assertErrorChainClean(t, resp.StatusError(t.Context()), allMarkers()...)

	_, err = treg.CallJSON[any](t.Context(), c, markedRequest())
	assertErrorChainClean(t, err, allMarkers()...)

	ok := testutil.Step{Body: `{"pii":"` + markerBody + `"}`}
	c2 := newClient(t, testutil.NewScript(ok), treg.WithResponseCapture())
	res, err := treg.CallJSON[json.RawMessage](t.Context(), c2, markedRequest())
	require.NoError(t, err)
	testutil.AssertShapesNeverPrint(t, &res.Captured, allMarkers()...)
	testutil.AssertShapesNeverPrint(t, &res.Meta, allMarkers()...)
}

func TestPrivacyValidationErrors(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript())
	bad := markedRequest()
	bad.Header = http.Header{"Cookie": {markerHeader + testToken}}
	_, err := c.CallBytes(t.Context(), bad)
	testutil.AssertNeverPrints(t, err, allMarkers()...)
	bad = markedRequest()
	bad.Query = url.Values{"t": {testToken}}
	_, err = c.CallBytes(t.Context(), bad)
	testutil.AssertNeverPrints(t, err, allMarkers()...)
	_, err = treg.NewClient(testToken + "\n" + markerHeader)
	testutil.AssertNeverPrints(t, err, allMarkers()...)
}

// TestPrivacyCallRequest checks the holdings in which a CallRequest's Format
// is reachable. Its exported fields are the caller's own data: held in an
// unexported field, or printed with %p as a value, fmt walks them, which no
// type can prevent.
func TestPrivacyCallRequest(t *testing.T) {
	t.Parallel()
	req := markedRequest()
	type exported struct {
		R treg.CallRequest
		P *treg.CallRequest
	}
	for _, h := range []testutil.Shape{
		{Name: "value", Value: req},
		{Name: "pointer", Value: &req},
		{Name: "exported fields", Value: exported{R: req, P: &req}},
		{Name: "slice of values", Value: []treg.CallRequest{req}},
		{Name: "map of pointers", Value: map[string]*treg.CallRequest{"k": &req}},
	} {
		t.Run(h.Name, func(t *testing.T) {
			t.Parallel()
			for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
				testutil.AssertRedacted(t, fmt.Sprintf(verb, h.Value), allMarkers(), "%s on %T", verb, h.Value)
			}
		})
	}
	out, err := json.Marshal(req)
	require.NoError(t, err)
	testutil.AssertRedacted(t, string(out), allMarkers())
	s := req.String()
	assert.Contains(t, s, "target: registered_tool")
	assert.Contains(t, s, "providerAuthorization: set")
	assert.Contains(t, s, "controls: idempotencyKey+tags")
	assert.True(t, strings.HasPrefix(s, "treg.CallRequest{"))
	assert.Contains(t, treg.CallRequest{Method: "bogus method"}.String(), "method: invalid")
}
