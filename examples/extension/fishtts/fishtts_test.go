package fishtts_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/examples/extension/fishtts"
)

// The tests use only what an external package has: httptest, the public
// treg API and the adapter. Every value is synthetic.
const (
	testToken = "test-token-0123456789abcdef"
	callRef   = "fedcba9876543210fedcba9876543210"
	greeting  = "Hello from example.com"
)

// errDiskFull is the destination's own failure in the write tests.
var errDiskFull = errors.New("disk full")

// seen is one request as the fake Treg saw it.
type seen struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// fakeTreg records every request and answers it with answer.
type fakeTreg struct {
	answer http.HandlerFunc

	mu   sync.Mutex
	reqs []seen
}

// ServeHTTP records r and hands it to answer.
func (f *fakeTreg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, seen{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	f.mu.Unlock()
	f.answer(w, r)
}

// requests returns a copy of the requests received so far.
func (f *fakeTreg) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// newClient serves f over TLS and returns a Client that talks to it: the
// pattern any package outside go-treg uses.
func newClient(t *testing.T, f *fakeTreg, opts ...treg.Option) *treg.Client {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	all := append([]treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}, opts...)
	c, err := treg.NewClient(testToken, all...)
	require.NoError(t, err)
	return c
}

// success returns a handler that answers 200 with contentType, a call ref
// and a charge, and then writes each chunk, flushed.
func success(contentType string, chunks ...[]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Treg-Call-Id", callRef)
		w.Header().Set("X-Treg-Cost-Micro", "330")
		w.WriteHeader(http.StatusOK)
		for _, chunk := range chunks {
			_, _ = w.Write(chunk)
			_ = http.NewResponseController(w).Flush()
		}
	}
}

// syntheticAudio returns n bytes that start like an MP3 frame and are not
// valid UTF-8.
func syntheticAudio(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(0xff - i%251)
	}
	copy(b, "\xff\xfb\x90\x64")
	return b
}

// writerFunc adapts a function to io.Writer.
type writerFunc func(p []byte) (int, error)

// Write returns f(p).
func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// failingWriter keeps the first limit bytes and then fails with errDiskFull.
type failingWriter struct {
	buf   bytes.Buffer
	limit int
}

// Write keeps what fits and fails when p does not.
func (w *failingWriter) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if len(p) <= room {
		return w.buf.Write(p)
	}
	n, _ := w.buf.Write(p[:room])
	return n, errDiskFull
}

func TestSynthesizeStreamsAudio(t *testing.T) {
	t.Parallel()
	audio := syntheticAudio(100_000)
	require.False(t, utf8.Valid(audio), "the fixture is binary, not text")
	f := &fakeTreg{answer: success("audio/mpeg", audio[:10], audio[10:70_000], audio[70_000:])}
	c := newClient(t, f)
	var dst bytes.Buffer

	res, err := fishtts.Synthesize(t.Context(), c,
		fishtts.Request{Text: greeting, Format: "mp3", Normalize: new(false)}, &dst,
		treg.Controls{MaxCost: new(treg.MicroUSD(10_000))})
	require.NoError(t, err)

	assert.Equal(t, int64(len(audio)), res.Bytes)
	assert.Equal(t, audio, dst.Bytes(), "every byte arrives unchanged")
	assert.Equal(t, "audio/mpeg", res.ContentType)
	assert.Equal(t, callRef, res.Meta.CallRef)
	assert.Equal(t, new(treg.MicroUSD(330)), res.Meta.CostMicro)

	reqs := f.requests()
	require.Len(t, reqs, 1)
	got := reqs[0]
	assert.Equal(t, http.MethodPost, got.Method)
	assert.Equal(t, "/call/fishaudio.tts.s2-1-pro", got.Path)
	assert.Equal(t, "s2.1-pro", got.Header.Get("Model"), "the provider header the descriptor requires")
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, testToken, got.Header.Get("X-Treg-Token"))
	assert.Equal(t, "0.01", got.Header.Get("X-Treg-Route-Max-Cost"))
	assert.JSONEq(t, `{"text":"Hello from example.com","format":"mp3","normalize":false}`, string(got.Body))
}

func TestSynthesizeRejectsInvalidInputWithoutSending(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      fishtts.Request
		nilDst  bool
		wantErr error
	}{
		{name: "empty text", in: fishtts.Request{}, wantErr: fishtts.ErrText},
		{name: "whitespace text", in: fishtts.Request{Text: " \n\t"}, wantErr: fishtts.ErrText},
		{
			name:    "text over the bound",
			in:      fishtts.Request{Text: strings.Repeat("a", fishtts.MaxTextBytes+1)},
			wantErr: fishtts.ErrText,
		},
		{name: "invalid UTF-8", in: fishtts.Request{Text: "hello \xff"}, wantErr: fishtts.ErrText},
		{name: "unknown format", in: fishtts.Request{Text: greeting, Format: "flac"}, wantErr: fishtts.ErrFormat},
		{name: "nil destination", in: fishtts.Request{Text: greeting}, nilDst: true, wantErr: fishtts.ErrNoWriter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTreg{answer: success("audio/mpeg", []byte("ID3"))}
			c := newClient(t, f)
			var dst io.Writer = &bytes.Buffer{}
			if tt.nilDst {
				dst = nil
			}

			res, err := fishtts.Synthesize(t.Context(), c, tt.in, dst, treg.Controls{})
			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, fishtts.Result{}, res)
			assert.Empty(t, f.requests(), "nothing is sent")
		})
	}
}

func TestRequestValidateBounds(t *testing.T) {
	t.Parallel()
	require.NoError(t, fishtts.Request{Text: strings.Repeat("a", fishtts.MaxTextBytes)}.Validate())
	require.NoError(t, fishtts.Request{Text: "héllo", Format: "opus"}.Validate())
	for _, format := range []string{"", "mp3", "wav", "pcm", "opus"} {
		require.NoError(t, fishtts.Request{Text: greeting, Format: format}.Validate(), format)
	}
	require.ErrorIs(t, fishtts.Request{Text: greeting, Format: "MP3"}.Validate(), fishtts.ErrFormat)
}

func TestSynthesizeReturnsCallErrorForRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		status         int
		gateway        bool
		body           string
		wantRefusal    string
		outcomeUnknown bool
	}{
		{
			name:        "Treg refusal",
			status:      http.StatusPaymentRequired,
			gateway:     true,
			body:        `{"detail":{"error":"insufficient_balance","balance_micro":0,"estimated_cost_micro":330}}`,
			wantRefusal: "insufficient_balance",
		},
		{
			name:   "provider refusal",
			status: http.StatusBadRequest,
			body:   `{"status":400,"message":"reference_id not found"}`,
		},
		{
			name:           "server error",
			status:         http.StatusBadGateway,
			body:           `{"message":"upstream failed"}`,
			outcomeUnknown: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeTreg{answer: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Treg-Call-Id", callRef)
				if tt.gateway {
					w.Header().Set("X-Treg-Error", "1")
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}}
			c := newClient(t, f)
			var dst bytes.Buffer

			res, err := fishtts.Synthesize(t.Context(), c, fishtts.Request{Text: greeting}, &dst, treg.Controls{})
			require.ErrorIs(t, err, treg.ErrHTTPStatus)
			var ce *treg.CallError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, tt.status, ce.StatusCode)
			assert.Equal(t, tt.gateway, errors.Is(err, treg.ErrGatewayRefusal))
			assert.Equal(t, tt.outcomeUnknown, errors.Is(err, treg.ErrOutcomeUnknown))
			if tt.wantRefusal != "" {
				require.NotNil(t, ce.Refusal)
				assert.Equal(t, tt.wantRefusal, ce.Refusal.Code)
				assert.Equal(t, new(treg.MicroUSD(330)), ce.Refusal.EstimatedCostMicro)
			}
			assert.Equal(t, callRef, res.Meta.CallRef)
			assert.Zero(t, res.Bytes)
			assert.Zero(t, dst.Len(), "an error body never reaches dst")
			assert.Len(t, f.requests(), 1)
		})
	}
}

func TestSynthesizeRefusesNonAudioSuccess(t *testing.T) {
	t.Parallel()
	f := &fakeTreg{answer: success("application/json",
		[]byte(`{"content_type":"audio/mpeg","body":"<binary audio omitted>"}`))}
	c := newClient(t, f)
	var dst bytes.Buffer

	res, err := fishtts.Synthesize(t.Context(), c, fishtts.Request{Text: greeting}, &dst, treg.Controls{})
	require.ErrorIs(t, err, fishtts.ErrNotAudio)
	require.NotErrorIs(t, err, treg.ErrOutcomeUnknown, "a 2xx executed")
	assert.Equal(t, "application/json", res.ContentType)
	assert.Zero(t, res.Bytes)
	assert.Zero(t, dst.Len())
	assert.Equal(t, callRef, res.Meta.CallRef, "the evidence of the executed call")
	assert.Equal(t, new(treg.MicroUSD(330)), res.Meta.CostMicro)
}

func TestSynthesizeAcceptsOctetStream(t *testing.T) {
	t.Parallel()
	audio := syntheticAudio(4096)
	f := &fakeTreg{answer: success("application/octet-stream", audio)}
	c := newClient(t, f)
	var dst bytes.Buffer

	res, err := fishtts.Synthesize(t.Context(), c, fishtts.Request{Text: greeting, Format: "pcm"}, &dst, treg.Controls{})
	require.NoError(t, err, "raw PCM may come as application/octet-stream")
	assert.Equal(t, "application/octet-stream", res.ContentType)
	assert.Equal(t, int64(len(audio)), res.Bytes)
	assert.Equal(t, audio, dst.Bytes())
}

func TestSynthesizeWriteFailureAfterResponseStarted(t *testing.T) {
	t.Parallel()
	audio := syntheticAudio(100_000)
	f := &fakeTreg{answer: success("audio/mpeg", audio)}
	c := newClient(t, f)
	dst := &failingWriter{limit: 4096}

	res, err := fishtts.Synthesize(t.Context(), c, fishtts.Request{Text: greeting}, dst, treg.Controls{})
	require.ErrorIs(t, err, fishtts.ErrWrite)
	require.ErrorIs(t, err, errDiskFull, "the destination's own error is kept")
	require.NotErrorIs(t, err, treg.ErrOutcomeUnknown, "the call executed: its outcome is known")
	assert.Equal(t, int64(4096), res.Bytes, "what reached dst")
	assert.Equal(t, audio[:4096], dst.buf.Bytes())
	assert.Equal(t, callRef, res.Meta.CallRef)
	assert.Equal(t, new(treg.MicroUSD(330)), res.Meta.CostMicro, "the call was charged: do not resubmit")
	assert.Len(t, f.requests(), 1)
}

func TestSynthesizeContextCanceledMidStream(t *testing.T) {
	t.Parallel()
	first := syntheticAudio(1024)
	release := make(chan struct{})
	f := &fakeTreg{answer: func(w http.ResponseWriter, r *http.Request) {
		success("audio/mpeg", first)(w, r)
		select { // the rest of the audio never comes
		case <-r.Context().Done():
		case <-release:
		}
		// The body never ends cleanly. Returning would let net/http write
		// the final chunk, and the client's TLS close sends close_notify
		// (which cancels r's context) before it closes the socket, so a
		// client still reading could see a complete body and io.EOF.
		panic(http.ErrAbortHandler)
	}}
	c := newClient(t, f)
	t.Cleanup(func() { close(release) }) // runs before the server closes

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var got bytes.Buffer
	dst := writerFunc(func(p []byte) (int, error) {
		n, err := got.Write(p)
		cancel() // the caller gives up after the first audio arrives
		return n, err
	})

	res, err := fishtts.Synthesize(ctx, c, fishtts.Request{Text: greeting}, dst, treg.Controls{})
	require.ErrorIs(t, err, context.Canceled)
	var ce *treg.CallError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, treg.FailureCanceled, ce.Kind)
	require.NotErrorIs(t, err, fishtts.ErrWrite, "a read failure, not a write failure")
	require.NotErrorIs(t, err, treg.ErrOutcomeUnknown, "a 2xx executed")
	assert.Positive(t, res.Bytes)
	assert.Equal(t, int64(got.Len()), res.Bytes)
	assert.True(t, bytes.HasPrefix(first, got.Bytes()), "only audio the server sent")
	assert.Equal(t, callRef, res.Meta.CallRef)
	assert.Len(t, f.requests(), 1)
}

func TestSynthesizeStopsAtMaxStreamBytes(t *testing.T) {
	t.Parallel()
	audio := syntheticAudio(64 << 10)
	f := &fakeTreg{answer: success("audio/mpeg", audio)}
	c := newClient(t, f, treg.WithMaxStreamBytes(16<<10))
	var dst bytes.Buffer

	res, err := fishtts.Synthesize(t.Context(), c, fishtts.Request{Text: greeting}, &dst, treg.Controls{})
	require.ErrorIs(t, err, treg.ErrResponseTooLarge)
	require.NotErrorIs(t, err, fishtts.ErrWrite)
	assert.Equal(t, int64(16<<10), res.Bytes, "the stream stops at the Client's bound")
	assert.Equal(t, audio[:16<<10], dst.Bytes())
}

func ExampleSynthesize() {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("X-Treg-Call-Id", "fedcba9876543210fedcba9876543210")
		w.Header().Set("X-Treg-Cost-Micro", "7")
		_, _ = io.WriteString(w, "ID3\x04\x00\x00fake-audio")
	}))
	defer srv.Close()
	client, err := treg.NewClient("test-token-0123456789abcdef", treg.WithBaseURL(srv.URL),
		treg.WithHTTPClient(srv.Client()))
	if err != nil {
		panic(err)
	}

	var audio bytes.Buffer // or an *os.File: the audio is never held whole by the adapter
	res, err := fishtts.Synthesize(context.Background(), client,
		fishtts.Request{Text: "Hello from example.com", Format: "mp3"}, &audio, treg.Controls{})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Bytes, res.ContentType, audio.Len())
	fmt.Println(res.Meta.CallRef, res.Meta.CostMicro.DecimalUSD())
	// Output:
	// 16 audio/mpeg 16
	// fedcba9876543210fedcba9876543210 0.000007
}
