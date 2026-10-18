package treg_test

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestBoundsResponseLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		body     string
		declared int64
		ok       bool
	}{
		{"below", "1234567", -1, true},
		{"at", "12345678", -1, true},
		{"above", "123456789", -1, false},
		{"declared at", "12345678", 8, true},
		{"declared above", "123456789", 9, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody(tt.body)
			c := newClient(t, testutil.TransportWith(http.StatusOK, nil, tt.declared, body), treg.WithMaxResponseBytes(8))
			raw, err := c.CallBytes(t.Context(), getRequest())
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.body, string(raw.Body()))
				return
			}
			// OutcomeUnknown is false: a 2xx executed.
			requireCallError(t, err, callErrorWant{Kind: treg.FailureResponseTooLarge, Status: http.StatusOK})
			if tt.declared > 0 {
				assert.Zero(t, body.Reads(), "a declared length over the limit is refused before reading")
			}
		})
	}
}

func TestBoundsDeclaredLengthExemptions(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		method string
		status int
	}{
		{"HEAD", http.MethodHead, 200},
		{"204", http.MethodGet, 204},
		{"304", http.MethodGet, 304},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.TransportWith(tt.status, nil, 1<<30, testutil.NewBody("")), treg.WithMaxResponseBytes(8))
			raw, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: tt.method})
			require.NoError(t, err, "the declared length of a body that is never sent is not checked")
			assert.Empty(t, raw.Body())
		})
	}
}

func TestBoundsGzipExpansion(t *testing.T) {
	t.Parallel()
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, err := zw.Write([]byte(`{"pad":"` + strings.Repeat("a", 64<<10) + `"}`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	srv := testutil.NewServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(zipped.Bytes())
	})
	small := newClient(t, srv, treg.WithMaxResponseBytes(16<<10))
	_, err = small.CallBytes(t.Context(), getRequest())
	require.ErrorIs(t, err, treg.ErrResponseTooLarge, "the limit counts decompressed bytes")

	big := newClient(t, srv)
	raw, err := big.CallBytes(t.Context(), getRequest())
	require.NoError(t, err)
	assert.True(t, raw.Decompressed())
	assert.Len(t, raw.Body(), 64<<10+10)
	res, err := treg.CallJSON[map[string]string](t.Context(), big, getRequest())
	require.NoError(t, err)
	assert.Len(t, res.Value["pad"], 64<<10)
}

func TestBoundsResponseHeaders(t *testing.T) {
	t.Parallel()
	for _, m := range callMethods(getRequest()) {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody(`{}`)
			header := http.Header{"Content-Type": {"application/json"}, "X-Big": {strings.Repeat("h", 65<<10)}}
			c := newClient(t, testutil.TransportWith(http.StatusOK, header, 2, body))
			ce := requireCallError(t, m.call(c, t.Context()), callErrorWant{Kind: treg.FailureUnexpectedResponse, Reason: "headers_too_large"})
			assert.Equal(t, http.StatusOK, ce.StatusCode)
			assert.Positive(t, body.Closes(), "the refused response's body is closed")
		})
	}
}

func TestBoundsRequestLimitExact(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script, treg.WithMaxRequestBytes(4))
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost, Body: treg.BytesBody("a/b", []byte("1234")),
	})
	require.NoError(t, err)
	assert.Equal(t, "1234", string(testutil.OnlyRequest(t, script).Body))
}
