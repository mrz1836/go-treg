package treg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestTargetValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target treg.Target
		kind   treg.TargetKind
		url    string // the request URL the transport sees
	}{
		{"catalog", treg.CatalogEndpoint("moz.web.url.metrics"), treg.TargetCatalogEndpoint, testBaseURL + "/call/moz.web.url.metrics"},
		{"routed", treg.CatalogEndpoint("treg.web.search"), treg.TargetCatalogEndpoint, testBaseURL + "/call/treg.web.search"},
		{"future id", treg.CatalogEndpoint("future.vendor.resource.read"), treg.TargetCatalogEndpoint, testBaseURL + "/call/future.vendor.resource.read"},
		{"unreserved id", treg.CatalogEndpoint("a~b_c-d.e"), treg.TargetCatalogEndpoint, testBaseURL + "/call/a~b_c-d.e"},
		{"tool no path", treg.RegisteredTool("stripe", ""), treg.TargetRegisteredTool, testBaseURL + "/call/stripe"},
		{"tool slash only", treg.RegisteredTool("stripe", "/"), treg.TargetRegisteredTool, testBaseURL + "/call/stripe"},
		{"tool path", treg.RegisteredTool("stripe", "v1/charges"), treg.TargetRegisteredTool, testBaseURL + "/call/stripe/v1/charges"},
		{"tool leading slash", treg.RegisteredTool("stripe", "/v1/charges"), treg.TargetRegisteredTool, testBaseURL + "/call/stripe/v1/charges"},
		{"tool trailing slash", treg.RegisteredTool("intercom", "conversations/"), treg.TargetRegisteredTool, testBaseURL + "/call/intercom/conversations/"},
		{"tool escapes kept", treg.RegisteredTool("files", "a%2Fb/c%7Ed/e%41"), treg.TargetRegisteredTool, testBaseURL + "/call/files/a%2Fb/c%7Ed/e%41"},
		{"tool sub-delims", treg.RegisteredTool("x", "a:b@c!$&'()*+,;=d"), treg.TargetRegisteredTool, testBaseURL + "/call/x/a:b@c!$&'()*+,;=d"},
		{"url https", treg.UpstreamURL("https://api.stripe.com/v1/charges"), treg.TargetUpstreamURL, testBaseURL + "/call/https://api.stripe.com/v1/charges"},
		{"url http port", treg.UpstreamURL("http://example.com:8080/a"), treg.TargetUpstreamURL, testBaseURL + "/call/http://example.com:8080/a"},
		{"url bare host", treg.UpstreamURL("https://example.com"), treg.TargetUpstreamURL, testBaseURL + "/call/https://example.com"},
		{"url host slash", treg.UpstreamURL("https://example.com/"), treg.TargetUpstreamURL, testBaseURL + "/call/https://example.com/"},
		{"url ipv4", treg.UpstreamURL("https://192.0.2.1/x"), treg.TargetUpstreamURL, testBaseURL + "/call/https://192.0.2.1/x"},
		{"url ipv6", treg.UpstreamURL("https://[2001:db8::1]:443/x"), treg.TargetUpstreamURL, testBaseURL + "/call/https://[2001:db8::1]:443/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, tt.target.Validate())
			assert.Equal(t, tt.kind, tt.target.Kind())
			assertTargetSent(t, tt.target, tt.url)
		})
	}
}

func TestTargetInvalid(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 250) + ".bcdefg"
	tests := []struct {
		name   string
		target treg.Target
		field  string
		reason string
	}{
		{"zero", treg.Target{}, "Target", "invalid_target"},
		{"id empty", treg.CatalogEndpoint(""), "Target.ID", "required"},
		{"id no dot", treg.CatalogEndpoint("moz"), "Target.ID", "invalid_format"},
		{"id slash", treg.CatalogEndpoint("moz/web.x"), "Target.ID", "invalid_character"},
		{"id escaped slash", treg.CatalogEndpoint("moz%2Fweb.x"), "Target.ID", "invalid_character"},
		{"id http", treg.CatalogEndpoint("http.example.x"), "Target.ID", "invalid_format"},
		{"id HTTP", treg.CatalogEndpoint("HTTPS.example.x"), "Target.ID", "invalid_format"},
		{"id leading dot", treg.CatalogEndpoint(".moz.x"), "Target.ID", "invalid_format"},
		{"id trailing dot", treg.CatalogEndpoint("moz.x."), "Target.ID", "invalid_format"},
		{"id dotdot", treg.CatalogEndpoint("moz..x"), "Target.ID", "invalid_format"},
		{"id dot", treg.CatalogEndpoint("."), "Target.ID", "invalid_format"},
		{"id too long", treg.CatalogEndpoint(treg.EndpointID(long)), "Target.ID", "too_long"},
		{"id space", treg.CatalogEndpoint("moz.web x"), "Target.ID", "invalid_character"},
		{"id crlf", treg.CatalogEndpoint("moz.x\r\nHost: evil"), "Target.ID", "control_character"},
		{"name empty", treg.RegisteredTool("", "x"), "Target.Name", "required"},
		{"name http", treg.RegisteredTool("http:", "x"), "Target.Name", "invalid_character"},
		{"name https prefix", treg.RegisteredTool("HTTPS:x", ""), "Target.Name", "invalid_character"},
		{"name slash", treg.RegisteredTool("a/b", ""), "Target.Name", "invalid_character"},
		{"name control", treg.RegisteredTool("a\tb", ""), "Target.Name", "control_character"},
		{"name dotdot", treg.RegisteredTool("..", ""), "Target.Name", "dot_segment"},
		{"name long", treg.RegisteredTool(strings.Repeat("n", 129), ""), "Target.Name", "too_long"},
		{"path query", treg.RegisteredTool("t", "a?b=c"), "Target.Path", "invalid_character"},
		{"path fragment", treg.RegisteredTool("t", "a#b"), "Target.Path", "invalid_character"},
		{"path space", treg.RegisteredTool("t", "a b"), "Target.Path", "invalid_character"},
		{"path backslash", treg.RegisteredTool("t", `a\b`), "Target.Path", "invalid_character"},
		{"path escaped backslash", treg.RegisteredTool("t", "a%5Cb"), "Target.Path", "invalid_character"},
		{"path escaped query", treg.RegisteredTool("t", "a%3Fb"), "Target.Path", "invalid_character"},
		{"path escaped fragment", treg.RegisteredTool("t", "a%23b"), "Target.Path", "invalid_character"},
		{"path control", treg.RegisteredTool("t", "a\x01b"), "Target.Path", "control_character"},
		{"path escaped NUL", treg.RegisteredTool("t", "a%00b"), "Target.Path", "control_character"},
		{"path escaped CRLF", treg.RegisteredTool("t", "a%0D%0Ab"), "Target.Path", "control_character"},
		{"path escaped space", treg.RegisteredTool("t", "a%20b"), "Target.Path", "invalid_character"},
		{"path DEL", treg.RegisteredTool("t", "a\x7fb"), "Target.Path", "control_character"},
		{"path bad escape", treg.RegisteredTool("t", "a%zzb"), "Target.Path", "invalid_escape"},
		{"path truncated escape", treg.RegisteredTool("t", "a%2"), "Target.Path", "invalid_escape"},
		{"path dot", treg.RegisteredTool("t", "a/./b"), "Target.Path", "dot_segment"},
		{"path dotdot", treg.RegisteredTool("t", "a/../b"), "Target.Path", "dot_segment"},
		{"path leading dotdot", treg.RegisteredTool("t", "../b"), "Target.Path", "dot_segment"},
		{"path escaped dotdot", treg.RegisteredTool("t", "a/%2E%2E/b"), "Target.Path", "dot_segment"},
		{"path decoded traversal", treg.RegisteredTool("t", "a%2F..%2Fb"), "Target.Path", "dot_segment"},
		{"path double slash", treg.RegisteredTool("t", "a//b"), "Target.Path", "empty_segment"},
		{"path network ref", treg.RegisteredTool("t", "//evil.example/x"), "Target.Path", "empty_segment"},
		{"path decoded double slash", treg.RegisteredTool("t", "a%2F%2Fb"), "Target.Path", "empty_segment"},
		{"path non-ascii", treg.RegisteredTool("t", "caf\xc3\xa9"), "Target.Path", "invalid_character"},
		{"url empty", treg.UpstreamURL(""), "Target.URL", "required"},
		{"url scheme case", treg.UpstreamURL("HTTPS://example.com/x"), "Target.URL", "invalid_url"},
		{"url ftp", treg.UpstreamURL("ftp://example.com/x"), "Target.URL", "invalid_url"},
		{"url relative", treg.UpstreamURL("example.com/x"), "Target.URL", "invalid_url"},
		{"url collapsed", treg.UpstreamURL("https:/example.com/x"), "Target.URL", "invalid_url"},
		{"url userinfo", treg.UpstreamURL("https://user:pw@example.com/x"), "Target.URL", "invalid_url"},
		{"url query", treg.UpstreamURL("https://example.com/x?a=b"), "Target.URL", "invalid_url"},
		{"url fragment", treg.UpstreamURL("https://example.com/x#f"), "Target.URL", "invalid_url"},
		{"url host query", treg.UpstreamURL("https://example.com?x"), "Target.URL", "invalid_url"},
		{"url bad host", treg.UpstreamURL("https://exa_mple.com/x"), "Target.URL", "invalid_url"},
		{"url bad port", treg.UpstreamURL("https://example.com:0/x"), "Target.URL", "invalid_url"},
		{"url empty host", treg.UpstreamURL("https:///x"), "Target.URL", "invalid_url"},
		{"url dotdot", treg.UpstreamURL("https://example.com/a/../b"), "Target.URL", "dot_segment"},
		{"url double slash", treg.UpstreamURL("https://example.com//b"), "Target.URL", "empty_segment"},
		{"url backslash host", treg.UpstreamURL(`https://example.com\@evil/x`), "Target.URL", "invalid_url"},
		{"url crlf", treg.UpstreamURL("https://example.com/a\r\nb"), "Target.URL", "control_character"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.target.Validate()
			requireValidation(t, err, tt.field, tt.reason)

			script := testutil.NewScript()
			c := newClient(t, script)
			_, callErr := c.CallBytes(t.Context(), treg.CallRequest{Target: tt.target, Method: http.MethodGet})
			require.ErrorIs(t, callErr, treg.ErrInvalidRequest)
			assert.Zero(t, script.Calls(), "nothing is sent")
		})
	}
}

func TestTargetPrintsKindOnly(t *testing.T) {
	t.Parallel()
	secret := "secretvendor.private.MARKER"
	tgt := treg.CatalogEndpoint(treg.EndpointID(secret))
	testutil.AssertShapesNeverPrint(t, &tgt, secret, "MARKER")
	out, err := json.Marshal(tgt)
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"catalog_endpoint"}`, string(out))
	assert.Equal(t, "treg.Target(invalid)", fmt.Sprint(treg.Target{}))
	assert.Equal(t, "upstream_url", treg.TargetUpstreamURL.String())
	assert.Equal(t, "registered_tool", treg.TargetRegisteredTool.String())
	assert.Equal(t, treg.TargetKind(0), treg.Target{}.Kind())
}

// assertTargetSent calls tgt with a GET and checks the one request's URL.
func assertTargetSent(t *testing.T, tgt treg.Target, wantURL string) {
	t.Helper()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	_, err := newClient(t, script).CallBytes(t.Context(), treg.CallRequest{Target: tgt, Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, wantURL, testutil.OnlyRequest(t, script).URL)
}

// onlyUnreserved reports whether s is ASCII letters, digits and "._~-" only.
func onlyUnreserved(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-._~", rune(c)) {
			return false
		}
	}
	return true
}

// wellFormedCatalogID is FuzzTarget's oracle for a catalog id, written from
// CatalogEndpoint's documentation rather than the SDK's code: 1 to 256 bytes
// of letters, digits and "._~-", holding a ".", not starting with "http" in
// any case, and not beginning or ending with "." or holding "..".
func wellFormedCatalogID(s string) bool {
	return len(s) >= 1 && len(s) <= 256 && onlyUnreserved(s) && strings.Contains(s, ".") &&
		!strings.HasPrefix(strings.ToLower(s), "http") &&
		!strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".") && !strings.Contains(s, "..")
}

// wellFormedToolName is FuzzTarget's oracle for a tool name, written from
// RegisteredTool's documentation: 1 to 128 bytes of letters, digits and
// "._~-", and not "." or "..".
func wellFormedToolName(s string) bool {
	return len(s) >= 1 && len(s) <= 128 && onlyUnreserved(s) && s != "." && s != ".."
}

// FuzzTarget tries each input as a catalog id, as a tool name (bare and with
// a path), as a tool path and as an upstream URL. Ids and names must be
// accepted exactly when the documented rules allow them, and then sent as
// /call/<id> or /call/<name>[/<path>], unchanged; no accepted form may
// smuggle a query, a fragment, a traversal or a second request line.
func FuzzTarget(f *testing.F) {
	seeds := []string{
		// Catalog ids, tool paths and upstream URLs.
		"moz.web.url.metrics", "treg.web.search", "a.b", "..", "a%2F..%2Fb", "a//b", `a\b`, "a%5Cb", "https://x.example/a",
		"HTTPS://x", "a?b", "a#b", "a\r\nb", "%2e%2e", "x/%2E/y", "::1", "[::1]:80/x", "a%zz",
		"http.example.x", "HttP.example.x", ".moz.x", "moz.x.", strings.Repeat("a", 254) + ".b", strings.Repeat("a", 255) + ".b",
		// Tool names.
		"stripe", "intercom", "x", "http", "http:", "HTTPS:x", "a/b", ".", "a b", "n~a_m-e.1", "tool%2F", "caf\xc3\xa9",
		strings.Repeat("n", 128), strings.Repeat("n", 129),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id := treg.CatalogEndpoint(treg.EndpointID(s))
		name, namePath := treg.RegisteredTool(s, ""), treg.RegisteredTool(s, "v1/x")
		assert.Equal(t, wellFormedCatalogID(s), id.Validate() == nil, "catalog id %q", s)
		assert.Equal(t, wellFormedToolName(s), name.Validate() == nil, "tool name %q", s)
		assert.Equal(t, wellFormedToolName(s), namePath.Validate() == nil, "tool name %q with a path", s)
		if wellFormedCatalogID(s) {
			assertTargetSent(t, id, testBaseURL+"/call/"+s)
		}
		if wellFormedToolName(s) {
			assertTargetSent(t, name, testBaseURL+"/call/"+s)
			assertTargetSent(t, namePath, testBaseURL+"/call/"+s+"/v1/x")
		}
		for _, tgt := range []treg.Target{id, name, treg.RegisteredTool("tool", s), treg.UpstreamURL(s)} {
			if tgt.Validate() != nil {
				continue
			}
			// Whatever passes may never smuggle a query, fragment, traversal
			// or a second request line through the path.
			for _, bad := range []string{"?", "#", "\r", "\n", " ", `\`, "/../", "/./", "%5c", "%5C"} {
				assert.NotContains(t, s, bad)
			}
			decoded := strings.NewReplacer("%2F", "/", "%2f", "/", "%2E", ".", "%2e", ".").Replace(s)
			for _, seg := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(decoded, "https://"), "http://"), "/")[1:] {
				assert.NotContains(t, []string{".", ".."}, seg, "no dot segment")
			}
		}
	})
}
