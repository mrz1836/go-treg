package treg_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// audioHeader returns the header of an audio answer.
func audioHeader() http.Header { return http.Header{"Content-Type": {"audio/mpeg"}} }

// neverDone returns a context with the values of t's that never ends. A
// goroutine left waiting on a call made from it never wakes, so a synctest
// bubble that leaks one deadlocks and fails the test, where t.Context(),
// which ends with the test, would wake it.
func neverDone(t *testing.T) context.Context {
	t.Helper()
	return context.WithoutCancel(t.Context())
}

func TestStreamReadsAll(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			audio := strings.Repeat("\x00\xffID3", 50_000)
			srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Header: audioHeader(), Body: audio}))
			c := newClient(t, srv)
			resp, err := c.Call(t.Context(), treg.CallRequest{
				Target: treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"), Method: http.MethodPost,
				Header: http.Header{"Model": {"s2.1-pro"}},
				Body:   treg.JSONBody(map[string]string{"text": "Hello from example.com"}),
			})
			require.NoError(t, err)
			require.NoError(t, resp.StatusError(t.Context()))
			var dst strings.Builder
			n, err := io.Copy(&dst, resp)
			require.NoError(t, err)
			assert.Equal(t, int64(len(audio)), n)
			assert.Equal(t, audio, dst.String())
			assert.Equal(t, "audio/mpeg", resp.Header().Get("Content-Type"))
			assert.Equal(t, "s2.1-pro", testutil.OnlyRequest(t, srv).Header.Get("Model"))
			require.NoError(t, resp.Close())
		})
	}
}

func TestStreamHeaderWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newClient(t, testutil.NewScript(testutil.Step{Respond: testutil.BlockUntilDone}),
			treg.WithTimeout(15*time.Millisecond), treg.WithStreamTimeout(time.Hour))
		start := time.Now()
		_, err := c.Call(t.Context(), getRequest())
		requireCallError(t, err, callErrorWant{Kind: treg.FailureTimeout, Reason: "header_timeout", OutcomeUnknown: true})
		assert.Equal(t, 15*time.Millisecond, time.Since(start), "the header wait is the lower of the two timeouts")
	})
}

func TestStreamLifetime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody("first")
		c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body), treg.WithStreamTimeout(30*time.Millisecond))
		start := time.Now()
		resp, err := c.Call(neverDone(t), getRequest())
		require.NoError(t, err)
		buf := make([]byte, 64)
		n, err := resp.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "first", string(buf[:n]))
		_, err = resp.Read(buf) // blocks until the stream deadline closes the body
		require.ErrorIs(t, err, treg.ErrTimeout)
		assert.Equal(t, 30*time.Millisecond, time.Since(start), "the stream lives exactly WithStreamTimeout")
		assert.Positive(t, body.Closes())
		require.NoError(t, resp.Close())
		_, err = resp.Read(buf)
		require.ErrorIs(t, err, treg.ErrTimeout, "the first failure is sticky")
	})
}

func TestStreamCloseOrdering(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody("x")
		ctx, cancel := context.WithCancel(neverDone(t))
		c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body))
		resp, err := c.Call(ctx, getRequest())
		require.NoError(t, err)
		require.NoError(t, resp.Close())
		require.NoError(t, resp.Close(), "idempotent")
		assert.Equal(t, 1, body.Closes())
		cancel()
		synctest.Wait()
		assert.Equal(t, 1, body.Closes(), "after Close, the context watcher never fires")
		_, err = resp.Read(make([]byte, 1))
		requireCallError(t, err, callErrorWant{Kind: treg.FailureTransport, Reason: "closed"})
	})
}

func TestStreamCloseInterruptsRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody("")
		c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body))
		resp, err := c.Call(t.Context(), getRequest())
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			_, rerr := resp.Read(make([]byte, 8))
			done <- rerr
		}()
		synctest.Wait() // the Read is blocked on the body
		require.NoError(t, resp.Close())
		// A Read that Close did not end would deadlock the bubble.
		requireCallError(t, <-done, callErrorWant{Kind: treg.FailureTransport, Reason: "closed"})
	})
}

func TestStreamCallerCancel(t *testing.T) {
	t.Parallel()
	body := testutil.NewBlockingBody("")
	ctx, cancel := context.WithCancel(t.Context())
	c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body))
	resp, err := c.Call(ctx, getRequest())
	require.NoError(t, err)
	go cancel()
	_, err = resp.Read(make([]byte, 8))
	requireCallError(t, err, callErrorWant{Kind: treg.FailureCanceled})
	require.NoError(t, resp.Close())
}

func TestStreamMaxBytes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		body    func() io.ReadCloser
		limit   int64
		want    string
		tooLong bool
	}{
		{"over the limit", func() io.ReadCloser { return testutil.NewBody("0123456789") }, 4, "0123", true},
		{"at the limit", func() io.ReadCloser { return testutil.NewBody("0123") }, 4, "0123", false},
		{"endless", func() io.ReadCloser { return &testutil.EndlessBody{} }, 1 << 20, strings.Repeat("x", 1<<20), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, tt.body()), treg.WithMaxStreamBytes(tt.limit))
			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			got, err := io.ReadAll(resp)
			assert.Equal(t, tt.want, string(got))
			if tt.tooLong {
				require.ErrorIs(t, err, treg.ErrResponseTooLarge)
			} else {
				require.NoError(t, err, "a body of exactly the limit is fine")
			}
			require.NoError(t, resp.Close())
		})
	}
}

func TestStreamDeclaredLength(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), 10, testutil.NewBody("short")))
	resp, err := c.Call(t.Context(), getRequest())
	require.NoError(t, err)
	got, err := io.ReadAll(resp)
	assert.Equal(t, "short", string(got))
	ce := testutil.RequireErrorAs[*treg.CallError](t, err)
	assert.Equal(t, treg.FailureTransport, ce.Kind, "EOF before the declared length is a failure")
	assert.Contains(t, resp.String(), "declaredBytes: 10")
	require.NoError(t, resp.Close())
}

func TestStreamStalled(t *testing.T) {
	t.Parallel()
	body := testutil.NewBody("")
	body.Stall = true
	c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body))
	resp, err := c.Call(t.Context(), getRequest())
	require.NoError(t, err)
	_, err = resp.Read(make([]byte, 8))
	assert.Equal(t, "stalled", testutil.RequireErrorAs[*treg.CallError](t, err).Reason)
	require.NoError(t, resp.Close())
}

// onceOnlyBody is a body whose Close is safe neither to repeat nor to run
// concurrently, as an injected transport's body may not be: a second Close
// counts, and -race reports two that overlap. Reads block until Close.
type onceOnlyBody struct {
	done    chan struct{}
	unsafeN int // written without synchronization
	closes  atomic.Int32
}

// newOnceOnlyBody returns an open onceOnlyBody.
func newOnceOnlyBody() *onceOnlyBody { return &onceOnlyBody{done: make(chan struct{})} }

// Read blocks until Close.
func (b *onceOnlyBody) Read([]byte) (int, error) {
	<-b.done
	return 0, net.ErrClosed
}

// Close counts the call and ends the body the first time.
func (b *onceOnlyBody) Close() error {
	b.unsafeN++
	if b.closes.Add(1) == 1 {
		close(b.done)
	}
	return nil
}

func TestStreamClosesBodyOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := newOnceOnlyBody()
		ctx, cancel := context.WithCancel(t.Context())
		resp, err := newClient(t, testutil.TransportWith(http.StatusOK, nil, -1, body)).Call(ctx, getRequest())
		require.NoError(t, err)
		cancel()
		synctest.Wait()
		require.Equal(t, int32(1), body.closes.Load(), "the context watcher closes the body")
		require.NoError(t, resp.Close())
		assert.Equal(t, int32(1), body.closes.Load(), "Close after the watcher does not close the body again")
	})
}

func TestStreamConcurrentCloses(t *testing.T) {
	t.Parallel()
	for range 20 {
		body := newOnceOnlyBody()
		ctx, cancel := context.WithCancel(t.Context())
		resp, err := newClient(t, testutil.TransportWith(http.StatusBadGateway, nil, -1, body)).Call(ctx, getRequest())
		require.NoError(t, err)
		statusCtx, statusCancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() { _ = resp.StatusError(statusCtx) }) // its watcher closes the body when statusCtx ends
		wg.Go(cancel)                                     // the stream's context watcher
		wg.Go(statusCancel)
		wg.Go(func() { _ = resp.Close() })
		wg.Wait()
		assert.Equal(t, int32(1), body.closes.Load())
		assert.Equal(t, 1, body.unsafeN, "every close after the first returns without touching the body")
	}
}

func TestStreamStatusErrorBounded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody(`{"detail":{"error":"route_fa`)
		c := newClient(t, testutil.TransportWith(http.StatusBadGateway, nil, -1, body), treg.WithTimeout(20*time.Millisecond))
		resp, err := c.Call(t.Context(), getRequest())
		require.NoError(t, err)
		start := time.Now()
		serr := resp.StatusError(t.Context())
		assert.Equal(t, 20*time.Millisecond, time.Since(start), "reading the excerpt is bounded by WithTimeout")
		ce := requireCallError(t, serr, callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusBadGateway, OutcomeUnknown: true})
		assert.Nil(t, ce.Refusal, "a body cut short does not parse")
		assert.Positive(t, body.Closes(), "StatusError closes the response")
		require.ErrorIs(t, resp.StatusError(nil), treg.ErrInvalidRequest) //nolint:staticcheck // a nil context is refused
	})
}

func TestStreamPrintsShapeOnly(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{
		Header: http.Header{"Set-Cookie": {"s=MARKER"}, "X-Secret": {"MARKER"}}, Body: "MARKER body",
	}))
	resp, err := c.Call(t.Context(), getRequest())
	require.NoError(t, err)
	testutil.AssertNeverPrints(t, resp, "MARKER")
	testutil.AssertNeverPrints(t, *resp, "MARKER")
	assert.Equal(t, "treg.Response{}", treg.Response{}.String())
	require.NoError(t, resp.Close())
}
