package treg_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// largeDescriptor returns a synthetic descriptor with n siblings.
func largeDescriptor(tb testing.TB, n int) string {
	tb.Helper()
	small := synthetic(tb, "catalog/endpoint.json")
	i := strings.Index(small, `"siblings"`)
	require.Positive(tb, i)
	row := `{"id":"vendor.web.url.metrics","provider":"vendor","provider_display":"Vendor","name":"n","summary":"s",` +
		`"method":"POST","path":"/p","scope":"any_account","tier":"core","kind":"data","domain":"url","call_template":"t",` +
		`"cost":{"type":"per_call","value":0.01,"currency":"USD","per":1,"unit":"call","usd":0.01},"async":null,` +
		`"platform_eligible":true,"platform_blocked":null,"miss":null,"status":null,"status_note":null,"superseded_by":null,` +
		`"verified":"2026-01-01","added":"2026-01-01","docs_url":"https://example.com/docs","has_example":true,` +
		`"input":{"bodyType":"json","body":{"targets":{"type":"array[string]","required":true}}},"test_request":null}`
	rows := strings.TrimSuffix(strings.Repeat(row+",", n), ",")
	return `{"endpoint":` + row + `,"provider":{"service":"vendor","display_name":"Vendor"},"siblings":[` + rows +
		`],"call_template":"t","example_response":null,"hints":[]}`
}

func BenchmarkDescriptorDecode(b *testing.B) {
	for _, bc := range []struct {
		name string
		body string
	}{
		{"small", synthetic(b, "catalog/endpoint.json")},
		{"large", largeDescriptor(b, 40)},
	} {
		b.Run(bc.name, func(b *testing.B) {
			c := newCatalog(b, testutil.StaticTransport(http.StatusOK, bc.body))
			b.SetBytes(int64(len(bc.body)))
			b.ReportAllocs()
			for b.Loop() {
				_, err := c.Endpoint(b.Context(), treg.EndpointRequest{ID: "moz.web.url.metrics"})
				require.NoError(b, err)
			}
		})
	}
}

func BenchmarkCallJSON(b *testing.B) {
	body := `{"results":[` + strings.TrimSuffix(strings.Repeat(`{"page":"example.com","domain_authority":91,"spam_score":1},`, 50), ",") + `]}`
	type out struct {
		Results []struct {
			Page            string `json:"page"`
			DomainAuthority *int   `json:"domain_authority"`
		} `json:"results"`
	}
	req := treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"), Method: http.MethodPost,
		Body:     treg.JSONBody(map[string][]string{"targets": {"example.com"}}),
		Controls: treg.Controls{IdempotencyKey: "job-1", Tags: []treg.Tag{{Key: "customer", Value: "c1"}}},
	}
	for _, capture := range []bool{false, true} {
		name := "capture_off"
		if capture {
			name = "capture_on"
		}
		b.Run(name, func(b *testing.B) {
			c := newClient(b, testutil.StaticTransport(http.StatusOK, body))
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				_, err := treg.CallJSON[out](b.Context(), c, req, treg.CaptureResponse(capture))
				require.NoError(b, err)
			}
		})
	}
}

func BenchmarkStreamCopy(b *testing.B) {
	const size = 1 << 20
	c := newClient(b, testutil.Transport(http.StatusOK, size, func() io.ReadCloser {
		return io.NopCloser(io.LimitReader(&testutil.EndlessBody{}, size))
	}))
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		resp, err := c.Call(b.Context(), getRequest())
		require.NoError(b, err)
		_, err = io.Copy(io.Discard, resp)
		require.NoError(b, err)
		require.NoError(b, resp.Close())
	}
}

func BenchmarkDefensiveCopies(b *testing.B) {
	c := newClient(b, testutil.NewScript(testutil.Step{
		Header: http.Header{"Content-Type": {"application/json"}, "X-A": {"1"}, "X-B": {"2"}, "Set-Cookie": {"s=1"}},
		Body:   strings.Repeat("x", 64<<10),
	}), treg.WithResponseCapture())
	raw, err := c.CallBytes(b.Context(), getRequest())
	require.NoError(b, err)
	b.Run("header_clone", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = raw.Header()
		}
	})
	cc := newClient(b, testutil.StaticTransport(http.StatusOK, `"`+strings.Repeat("x", 64<<10)+`"`), treg.WithResponseCapture())
	res, err := treg.CallJSON[string](b.Context(), cc, getRequest())
	require.NoError(b, err)
	b.Run("capture_bytes", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = res.Captured.Bytes()
		}
	})
}
