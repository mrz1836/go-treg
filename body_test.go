package treg_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// ownedReader is a caller's reader that records whether anything closed it,
// reads after its owner took it back, and how many bytes were read.
type ownedReader struct {
	r        io.Reader
	closed   atomic.Bool
	released atomic.Bool
	late     atomic.Int32
	n        atomic.Int64
}

func (o *ownedReader) Read(p []byte) (int, error) {
	if o.released.Load() {
		o.late.Add(1)
	}
	n, err := o.r.Read(p)
	o.n.Add(int64(n))
	return n, err
}

func (o *ownedReader) Close() error {
	o.closed.Store(true)
	return nil
}

func readerReq(r io.Reader, size int64) treg.CallRequest {
	return treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Body: treg.ReaderBody("application/octet-stream", r, size),
	}
}

func TestReaderBodyExact(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Body: `{}`}))
			c := newClient(t, srv)
			src := &ownedReader{r: strings.NewReader("hello world")}
			_, err := c.CallBytes(t.Context(), readerReq(src, 11))
			require.NoError(t, err)
			src.released.Store(true)
			assert.Equal(t, "hello world", string(testutil.OnlyRequest(t, srv).Body))
			assert.False(t, src.closed.Load(), "the SDK never closes the caller's reader")
			assert.Zero(t, src.late.Load(), "nothing reads the reader after the method returns")
		})
	}
}

func TestReaderBodyShortAndLong(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		r      io.Reader
		size   int64
		reason string
	}{
		{"short", strings.NewReader("abc"), 10, "short_body"},
		{"long", strings.NewReader("abcdefghij"), 3, "long_body"},
		{"endless", &testutil.EndlessBody{}, 1024, "long_body"},
		{"failing", io.MultiReader(strings.NewReader("ab"), iotestErrReader{}), 10, "body_read"},
		{"stalled", stallReader{}, 10, "stalled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The server never answers: it waits for the client to drop the
			// connection. It reads only the declared bytes, so it never sees
			// a long body's extra byte, and an answer sent once it has them
			// would race the client's own check of byte size+1: a response
			// that won the race would let the call succeed.
			release := make(chan struct{})
			srv := testutil.NewServer(t, func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-release:
				}
			})
			t.Cleanup(func() { close(release) }) // runs before the server closes
			c := newClient(t, srv)
			_, err := c.CallBytes(t.Context(), readerReq(tt.r, tt.size))
			ce := testutil.RequireErrorAs[*treg.CallError](t, err)
			require.ErrorIs(t, err, treg.ErrTransport)
			assert.Equal(t, tt.reason, ce.Reason)
			if eb, ok := tt.r.(*testutil.EndlessBody); ok {
				assert.LessOrEqual(t, eb.BytesRead(), int64(1024+64<<10), "no unbounded drain past size+1")
			}
		})
	}
}

// iotestErrReader always fails with a message a test can look for.
type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("disk failure at /secret/path: %w", errTestRead)
}

// errTestRead is the cause iotestErrReader wraps.
var errTestRead = errors.New("test read failure")

// stallReader never makes progress.
type stallReader struct{}

func (stallReader) Read([]byte) (int, error) { return 0, nil }

func TestReaderBodyValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		body   treg.Body
		field  string
		reason string
	}{
		{"nil reader", treg.ReaderBody("a/b", nil, 1), "Body", "nil"},
		{"zero size", treg.ReaderBody("a/b", strings.NewReader(""), 0), "Body", "out_of_range"},
		{"over limit", treg.ReaderBody("a/b", strings.NewReader("123456789"), 9), "Body", "out_of_range"},
		{"no content type", treg.ReaderBody("", strings.NewReader("1"), 1), "Body.ContentType", "required"},
		{"bad content type", treg.ReaderBody("a/b\r\n", strings.NewReader("1"), 1), "Body.ContentType", "control_character"},
		{"non-ascii content type", treg.ReaderBody("a/bé", strings.NewReader("1"), 1), "Body.ContentType", "invalid_character"},
		{"bytes over limit", treg.BytesBody("a/b", []byte("123456789")), "Body", "too_large"},
		{"json over limit", treg.JSONBody("12345678"), "Body", "too_large"},
		{"form over limit", treg.FormBody(map[string][]string{"a": {"12345678"}}), "Body", "too_large"},
		{"json unencodable", treg.JSONBody(func() {}), "Body", "json_marshal"},
		{"json NaN", treg.JSONBody(map[string]any{"x": nan()}), "Body", "json_marshal"},
		{"bytes no content type", treg.BytesBody("", []byte("1")), "Body.ContentType", "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script, treg.WithMaxRequestBytes(8))
			_, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost, Body: tt.body})
			requireValidation(t, err, tt.field, tt.reason)
			assert.Zero(t, script.Calls())
		})
	}
}

// nan returns a NaN without a constant expression.
func nan() float64 {
	zero := 0.0
	return zero / zero
}

func TestReaderBodyInUseUntilClose(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("streamed"))
	})
	c := newClient(t, srv)
	src := &ownedReader{r: bytes.NewReader([]byte("payload"))}
	resp, err := c.Call(t.Context(), readerReq(src, 7))
	require.NoError(t, err)
	got, err := io.ReadAll(resp)
	require.NoError(t, err)
	assert.Equal(t, "streamed", string(got))
	require.NoError(t, resp.Close())
	src.released.Store(true)
	assert.Equal(t, "payload", string(testutil.OnlyRequest(t, srv).Body))
	assert.False(t, src.closed.Load())
	assert.Zero(t, src.late.Load())
}

func TestBodyInputsSnapshotted(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.Serve(testutil.Step{Body: `{}`}))
	c := newClient(t, srv)
	data := []byte("original")
	h := http.Header{"X-A": {"1"}}
	resp, err := c.Call(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost, Header: h,
		Body: treg.BytesBody("text/plain", data),
	})
	require.NoError(t, err)
	copy(data, "MUTATED!")
	h.Set("X-A", "2")
	_, err = io.ReadAll(resp)
	require.NoError(t, err)
	require.NoError(t, resp.Close())
	r := testutil.OnlyRequest(t, srv)
	assert.Equal(t, "original", string(r.Body))
	assert.Equal(t, "1", r.Header.Get("X-A"))
}

func TestBodyPrintsShapeOnly(t *testing.T) {
	t.Parallel()
	marker := "BODY-MARKER-123"
	for _, tt := range []struct {
		name string
		body treg.Body
	}{
		{"json", treg.JSONBody(map[string]string{"s": marker})},
		{"bytes", treg.BytesBody("text/plain", []byte(marker))},
		{"form", treg.FormBody(map[string][]string{"s": {marker}})},
		{"reader", treg.ReaderBody("text/plain", strings.NewReader(marker), 15)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			testutil.AssertShapesNeverPrint(t, &tt.body, marker, "BODY-MARKER")
		})
	}
	assert.Equal(t, "treg.Body(bytes, 3 bytes)", treg.BytesBody("a/b", []byte("abc")).String())
	assert.Equal(t, "treg.Body(json)", treg.JSONBody(1).String())
	assert.Equal(t, "treg.Body(none)", treg.Body{}.String())
}
