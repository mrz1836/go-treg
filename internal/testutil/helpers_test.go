package testutil_test

import (
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errBoom is a planted failure.
var errBoom = errors.New("boom")

// recorder is a testing.TB that records a failure instead of failing the
// test, so a test can prove an assertion helper fails when it should. Like
// testing.T's, its FailNow ends the goroutine that calls it, so run the
// helper under record. Only the methods the helpers call are implemented; any
// other panics.
type recorder struct {
	testing.TB

	failed bool
}

func (r *recorder) Helper() {}

func (r *recorder) Name() string { return "recorder" }

func (r *recorder) Errorf(string, ...any) { r.failed = true }

// FailNow records the failure and ends the calling goroutine, as
// testing.T.FailNow does, so nothing after a failed require runs.
func (r *recorder) FailNow() {
	r.failed = true
	runtime.Goexit()
}

// record runs f with a fresh recorder on a goroutine of its own, as the
// testing package runs a test, and waits for f to end. It returns the
// recorder and whether f returned rather than being stopped by FailNow.
func record(f func(*recorder)) (*recorder, bool) {
	rec := &recorder{}
	returned := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(rec)
		returned = true
	}()
	<-done
	return rec, returned
}

// TestRecorderStopsAtFailNow proves that a function run under record stops at
// a failed require, as a test does, and goes on after a failed assert.
func TestRecorderStopsAtFailNow(t *testing.T) {
	t.Parallel()
	after := false
	rec, returned := record(func(r *recorder) {
		require.Fail(r, "planted")
		after = true
	})
	assert.True(t, rec.failed, "the failure is recorded")
	assert.False(t, after, "code after a failed require never runs")
	assert.False(t, returned)

	rec, returned = record(func(r *recorder) { assert.Fail(r, "planted") })
	assert.True(t, rec.failed, "the failure is recorded")
	assert.True(t, returned, "a failed assert lets the function go on")
}

// newRequest returns a POST to a loopback URL with body.
func newRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://127.0.0.1/p?q=1", strings.NewReader(body))
	require.NoError(t, err)
	return req
}

// readAll reads and closes resp's body.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return string(data)
}

// reply is a response as a client saw it, its body read and closed.
type reply struct {
	Status int
	Proto  string
	Header http.Header
	Body   string
}

// send makes one request through hc and returns its reply or the error. A
// GET with a nil body is one the standard library counts as safe to resend.
func send(t *testing.T, hc *http.Client, method, url string, body io.Reader) (reply, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	return do(t, hc, req)
}

// do sends req through hc and returns its reply or the error.
func do(t *testing.T, hc *http.Client, req *http.Request) (reply, error) {
	t.Helper()
	resp, err := hc.Do(req)
	if err != nil {
		return reply{}, err
	}
	return reply{Status: resp.StatusCode, Proto: resp.Proto, Header: resp.Header, Body: readAll(t, resp)}, nil
}
