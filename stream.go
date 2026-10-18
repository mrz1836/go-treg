package treg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// stream is an open response body read incrementally: Response and
// FindStream. Read is for one goroutine; Close may come from any.
type stream struct {
	received

	op          Operation
	declared    int64
	limit       int64
	timeout     time.Duration
	errorBodies bool

	//nolint:containedctx // a stream classifies late read errors against the contexts of its one request
	caller context.Context
	//nolint:containedctx // see caller
	life   context.Context
	cancel func()
	stop   func() bool
	body   io.ReadCloser
	reader *readerBody

	// closeBody closes body exactly once (see closeOnce). The stream's
	// context watcher, close and StatusError's own watcher may call it at
	// the same time, and a body from an injected transport need not
	// tolerate a second Close.
	closeBody func()

	// mu serializes the reads of body, read's and StatusError's, and guards
	// n, err and probe.
	mu     sync.Mutex
	n      int64
	err    error
	probe  [1]byte // the buffer of the read past the limit (see window)
	closed atomic.Bool
	once   sync.Once
}

// openStream makes the one attempt of w and returns its response as a
// stream: its life ends at WithStreamTimeout, and the wait for headers at
// the lower of WithTimeout and WithStreamTimeout.
func (s *settings) openStream(ctx context.Context, w *wire, limit int64) (*stream, *CallError) {
	if err := ctx.Err(); err != nil {
		w.reader.detach()
		return nil, contextFailure(w.op, err, 0)
	}
	base, cancelBase := context.WithCancelCause(ctx)
	life, cancelLife := context.WithTimeout(base, s.streamTimeout)
	cancel := func() {
		cancelLife()
		cancelBase(nil)
	}
	timer := time.AfterFunc(min(s.timeout, s.streamTimeout), func() { cancelBase(errHeaderTimeout) })
	at := &attempt{}
	// The stream closes the body: Close, its deadline or the caller's context.
	resp, err := s.send(life, w, at) //nolint:bodyclose // closeOnce(resp.Body) below closes it
	timer.Stop()
	if err != nil {
		ce := s.dispatchFailure(ctx, life, w, err, at)
		cancel()
		w.reader.detach()
		return nil, ce
	}
	st := &stream{
		received:    receive(w.op, resp, &at.loc),
		op:          w.op,
		declared:    declaredLength(resp),
		limit:       limit,
		timeout:     s.timeout,
		errorBodies: s.errorBodies,
		caller:      ctx,
		life:        life,
		cancel:      cancel,
		body:        resp.Body,
		reader:      w.reader,
		closeBody:   closeOnce(resp.Body),
	}
	st.stop = context.AfterFunc(life, st.closeBody)
	if st.status < 200 {
		_ = st.close()
		return nil, newCallError(w.op, FailureUnexpectedResponse, reasonInformational, st.status, errInformational).
			withResponse(st.meta, nil)
	}
	return st, nil
}

// close ends the stream once: it stops the context watcher, closes the body,
// cancels the request and its timers, and stops reading a ReaderBody.
func (st *stream) close() error {
	st.once.Do(func() {
		st.closed.Store(true)
		st.stop()
		st.closeBody()
		st.cancel()
		st.reader.detach()
	})
	return nil
}

// read reads the next part of the body, within the stream limit.
func (st *stream) read(p []byte) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch {
	case st.err != nil:
		return 0, st.err
	case st.closed.Load():
		return 0, st.fail(transportFailure(st.op, st.status, errStreamClosed))
	case len(p) == 0:
		return 0, nil
	}
	p, probe := st.window(p)
	// Every return below either delivers bytes or ends the stream, so the
	// count of empty reads never needs to outlive one call.
	n, stalled, err := readProgress(st.body, p)
	switch {
	case stalled:
		return 0, st.fail(transportFailure(st.op, st.status, errTransportStalled))
	case probe && n > 0:
		return 0, st.fail(newCallError(st.op, FailureResponseTooLarge, "", st.status, errResponseTooLarge))
	}
	st.n += int64(n)
	return n, st.settle(err)
}

// window returns the part of p, which is not empty, that the stream limit
// leaves room for. Once the limit is reached, it returns the stream's
// one-byte probe buffer instead, with probe set: the body must end there.
// The caller holds mu.
func (st *stream) window(p []byte) (_ []byte, probe bool) {
	if st.n >= st.limit {
		return st.probe[:], true
	}
	return p[:min(int64(len(p)), st.limit-st.n)], false
}

// settle returns the error of a read whose bytes are counted: io.EOF after
// a body that ended at its declared length, if any, and a sticky
// *CallError after any other failure.
func (st *stream) settle(err error) error {
	switch {
	case err == io.EOF: //nolint:errorlint // readProgress returns the body's own io.EOF
		if truncated(st.n, st.declared) {
			return st.fail(transportFailure(st.op, st.status, errTransportClosed))
		}
		st.err = io.EOF
		return io.EOF
	case err != nil:
		return st.fail(st.readFailure(err))
	}
	return nil
}

// fail records ce as the stream's sticky error and returns it.
func (st *stream) fail(ce *CallError) error {
	st.err = ce.withResponse(st.meta, nil)
	return st.err
}

// readFailure classifies a body read error.
func (st *stream) readFailure(err error) *CallError {
	if st.closed.Load() {
		return transportFailure(st.op, st.status, errStreamClosed)
	}
	return classify(st.caller, st.life, st.op, err, st.status, nil)
}

// Response is a streamed /call/ response from Call. It implements
// io.ReadCloser: Read returns the body, after any transport decompression,
// up to WithMaxStreamBytes, and Close ends the request. io.EOF comes only
// after a clean end of a body whose declared length, if any, matched; every
// other read failure is a *CallError.
//
// It prints its status, metadata and declared length only.
type Response struct {
	// stream returns the open stream. It sits behind a function value so
	// that no fmt verb or holding can reach the headers it keeps.
	stream sealed[*stream]
}

// StatusCode returns the HTTP status, or 0 for the zero Response.
func (r *Response) StatusCode() int {
	if st := r.open(); st != nil {
		return st.status
	}
	return 0
}

// Meta returns the response metadata.
func (r *Response) Meta() Metadata {
	if st := r.open(); st != nil {
		return st.meta
	}
	return Metadata{}
}

// Read reads the body. The zero Response returns ErrInvalidConfig.
func (r *Response) Read(p []byte) (int, error) {
	st := r.open()
	if st == nil {
		return 0, notInitialized("response")
	}
	return st.read(p)
}

// Close ends the request, its timers and any read of a ReaderBody. It is
// idempotent and always returns nil for a Response from Call; the zero
// Response returns ErrInvalidConfig.
func (r *Response) Close() error {
	st := r.open()
	if st == nil {
		return notInitialized("response")
	}
	return st.close()
}

// Header returns a clone of every response header, Location and Set-Cookie
// included. Headers are sensitive: an upstream can put anything in them.
func (r *Response) Header() http.Header {
	if st := r.open(); st != nil {
		return st.header.Clone()
	}
	return http.Header{}
}

// Decompressed reports whether the transport decompressed a gzip body.
func (r *Response) Decompressed() bool {
	st := r.open()
	return st != nil && st.decompressed
}

// StatusError returns nil for a 2xx. For a 3xx it returns
// ErrUnexpectedResponse without reading the body. For a 4xx or 5xx it
// reads up to 64 KiB of the body, within ctx, WithTimeout and the stream's
// own deadline, closes the Response and returns a *CallError matching
// ErrHTTPStatus, with the Refusal parsed when the body allows.
func (r *Response) StatusError(ctx context.Context) error {
	st := r.open()
	if st == nil {
		return notInitialized("response")
	}
	if ctx == nil {
		return nilContext()
	}
	ex := &exchange{received: st.received}
	switch {
	case st.status >= 200 && st.status < 300:
		return nil
	case st.status < 400:
		return ex.statusError(st.op, false)
	}
	readCtx, cancel := context.WithTimeout(ctx, st.timeout)
	defer cancel()
	stop := context.AfterFunc(readCtx, st.closeBody)
	// An encoded error body is never read: it would yield nothing.
	if !encoded(st.header) && !st.closed.Load() {
		st.mu.Lock()
		part := excerpt(st.body)
		st.mu.Unlock()
		ex.noteRefusal(part, st.errorBodies)
	}
	stop()
	_ = st.close()
	return ex.statusError(st.op, false)
}

// String returns the status, the metadata and the declared length.
func (r Response) String() string {
	st := r.open()
	if st == nil {
		return "treg.Response{}"
	}
	return "treg.Response{status: " + strconv.Itoa(st.status) + ", declaredBytes: " +
		strconv.FormatInt(st.declared, 10) + ", meta: " + st.meta.String() + "}"
}

// GoString returns the same text as String.
func (r Response) GoString() string { return r.String() }

// Format prints String for every verb.
func (r Response) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the status, the metadata and the declared length.
func (r Response) MarshalJSON() ([]byte, error) {
	st := r.open()
	if st == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(responseJSON{Status: st.status, BodyBytes: st.declared, Meta: st.meta})
}

// newResponse wraps st.
func newResponse(st *stream) *Response { return &Response{stream: seal(st)} }

// open returns the stream, or nil for the zero Response.
func (r *Response) open() *stream {
	if r == nil || r.stream == nil {
		return nil
	}
	return r.stream()
}
