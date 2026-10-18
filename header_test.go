package treg_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// callWithHeader makes one CallBytes with h and returns its error and the
// number of requests sent.
func callWithHeader(t *testing.T, h http.Header, providerAuth string) (int, error) {
	t.Helper()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet, Header: h, ProviderAuthorization: providerAuth,
	})
	return script.Calls(), err
}

func TestHeaderReservedNames(t *testing.T) {
	t.Parallel()
	reserved := []string{
		"Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Proxy-Authorization",
		"Proxy-Connection", "TE", "Trailer", "Trailers", "Upgrade", "Expect", "Forwarded", "Via", "X-Forwarded-For",
		"X-Forwarded-Host", "X-Real-IP", "CF-Connecting-IP", "CF-Ray", "True-Client-IP", "CDN-Loop", "Cookie",
		"Cookie2", "Authorization", "X-Treg-Token", "X-Treg-Org", "X-Treg-Meta", "X-Treg-Anything", "Idempotency-Key",
		"X-Idempotency-Key", "Content-Type", "Content-Encoding", "Accept-Encoding", "User-Agent",
	}
	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, spelling := range []struct {
				name  string
				spell func(string) string
			}{
				{"as given", func(s string) string { return s }},
				{"lower case", strings.ToLower},
				{"upper case", strings.ToUpper},
			} {
				t.Run(spelling.name, func(t *testing.T) {
					t.Parallel()
					spelled := spelling.spell(name)
					calls, err := callWithHeader(t, http.Header{spelled: {"v"}}, "")
					requireValidation(t, err, "Header["+spelled+"]", "reserved_header")
					assert.Zero(t, calls)
				})
			}
		})
	}
}

func TestHeaderAllowedNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Model", "Accept", "X-Api-Version", "anthropic-version", "Prefer", "Cache-Control", "Range"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			calls, err := callWithHeader(t, http.Header{name: {"v"}}, "")
			require.NoError(t, err)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestHeaderValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		value  string
		reason string
	}{
		{"ok", "a value\twith tab", ""},
		{"empty ok", "", ""},
		{"SOH", "a\x01b", "control_character"},
		{"DEL", "a\x7fb", "control_character"},
		{"CR", "a\rb", "control_character"},
		{"LF", "a\nb", "control_character"},
		{"NUL", "a\x00b", "control_character"},
		{"non-ascii", "café", "invalid_character"},
		{"byte 0x80", "a\x80b", "invalid_character"},
		{"byte 0xff", "a\xffb", "invalid_character"},
		{"control byte first", "\x01é", "control_character"},
		{"non-ascii byte first", "é\x01", "invalid_character"},
		{"leading space", " a", "surrounding_whitespace"},
		{"trailing tab", "a\t", "surrounding_whitespace"},
		{"too long", strings.Repeat("v", 8<<10+1), "too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls, err := callWithHeader(t, http.Header{"X-Custom": {tt.value}}, "")
			if tt.reason == "" {
				require.NoError(t, err)
				return
			}
			requireValidation(t, err, "Header[X-Custom]", tt.reason)
			assert.Zero(t, calls)
			assert.NotContains(t, err.Error(), tt.value)

			calls, err = callWithHeader(t, nil, tt.value)
			requireValidation(t, err, "ProviderAuthorization", tt.reason)
			assert.Zero(t, calls)
		})
	}
}

// numberedHeader returns a header of n fields, prefix0 to prefix(n-1), each
// holding value.
func numberedHeader(prefix string, n int, value string) http.Header {
	h := make(http.Header, n)
	for i := range n {
		h[prefix+strconv.Itoa(i)] = []string{value}
	}
	return h
}

func TestHeaderNamesAndBounds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header http.Header
		reason string // "" when the header is sent
	}{
		{"space in a name", http.Header{"Bad Name": {"v"}}, "invalid_character"},
		{"colon in a name", http.Header{"Bad:Name": {"v"}}, "invalid_character"},
		{"control byte in a name", http.Header{"Bad\x00Name": {"v"}}, "control_character"},
		{"empty name", http.Header{"": {"v"}}, "required"},
		{"name over 256 bytes", http.Header{strings.Repeat("n", 257): {"v"}}, "too_long"},
		{"name of 256 bytes is within the bound", http.Header{strings.Repeat("n", 256): {"v"}}, ""},
		{"65 fields", numberedHeader("X-H", 65, "v"), "too_many"},
		{"64 fields", numberedHeader("X-H", 64, "v"), ""},
		{"too large", numberedHeader("X-Big", 3, strings.Repeat("v", 6<<10)), "too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls, err := callWithHeader(t, tt.header, "")
			if tt.reason == "" {
				require.NoError(t, err)
				assert.Equal(t, 1, calls)
				return
			}
			requireValidation(t, err, "Header", tt.reason)
			assert.Zero(t, calls)
		})
	}
}

func TestHeaderCallerMapUntouched(t *testing.T) {
	t.Parallel()
	h := http.Header{"Model": {"s2.1-pro"}}
	_, err := callWithHeader(t, h, "Bearer k")
	require.NoError(t, err)
	assert.Equal(t, http.Header{"Model": {"s2.1-pro"}}, h, "the caller's map is never modified")
}
