package treg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
)

// bodyKind names the form of a Body.
type bodyKind uint8

const (
	bodyNone bodyKind = iota
	bodyJSON
	bodyBytes
	bodyForm
	bodyReader
)

// String names the kind.
func (k bodyKind) String() string {
	switch k {
	case bodyJSON:
		return "json"
	case bodyBytes:
		return "bytes"
	case bodyForm:
		return "form"
	case bodyReader:
		return "reader"
	case bodyNone:
	}
	return "none"
}

// Body is a request body. Build one with JSONBody, BytesBody, FormBody or
// ReaderBody; the zero Body sends no body. The constructors only record
// their arguments: the call encodes or copies them, within
// WithMaxRequestBytes, and reports a failure before anything is sent.
//
// A Body prints its kind and, when known, its size only.
type Body struct {
	kind bodyKind
	size int64 // -1 when unknown before encoding
	data sealed[bodyData]
}

// bodyData is what a Body holds, hidden behind a function value.
type bodyData struct {
	contentType string
	value       any
	bytes       []byte
	form        url.Values
	reader      io.Reader
}

// JSONBody sends v encoded with encoding/json (json.Marshal) as
// application/json. The call encodes it, so v must not change until the
// call returns; an encoding failure is ErrInvalidRequest and nothing is
// sent.
func JSONBody(v any) Body {
	return Body{kind: bodyJSON, size: -1, data: seal(bodyData{value: v})}
}

// BytesBody sends a copy of b with the given Content-Type, such as a
// multipart body built with mime/multipart (pass its FormDataContentType).
// contentType must be a valid header value of at most 1 KiB.
func BytesBody(contentType string, b []byte) Body {
	return Body{
		kind: bodyBytes, size: int64(len(b)),
		data: seal(bodyData{contentType: contentType, bytes: b}),
	}
}

// FormBody sends v encoded with url.Values.Encode as
// application/x-www-form-urlencoded.
func FormBody(v url.Values) Body {
	return Body{kind: bodyForm, size: -1, data: seal(bodyData{form: v})}
}

// ReaderBody streams exactly size bytes of r with the given Content-Type.
// size must be from 1 byte to WithMaxRequestBytes. The SDK reads r only
// through Read, never closes it, and stops at byte size+1: a reader that
// ends early or runs long fails the call with ErrTransport, possibly after
// part of the body was sent. A buffered method stops reading r before it
// returns; Call stops at Response.Close. ReaderBody contents are not
// scanned for the token.
func ReaderBody(contentType string, r io.Reader, size int64) Body {
	return Body{
		kind: bodyReader, size: size,
		data: seal(bodyData{contentType: contentType, reader: r}),
	}
}

// String returns "treg.Body(<kind>)" or "treg.Body(<kind>, <size> bytes)".
func (b Body) String() string {
	if b.kind == bodyNone {
		return "treg.Body(none)"
	}
	if b.size >= 0 {
		return "treg.Body(" + b.kind.String() + ", " + strconv.FormatInt(b.size, 10) + " bytes)"
	}
	return "treg.Body(" + b.kind.String() + ")"
}

// GoString returns the same text as String.
func (b Body) GoString() string { return b.String() }

// Format prints String for every verb.
func (b Body) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, b.String()) }

// MarshalJSON encodes String as a JSON string.
func (b Body) MarshalJSON() ([]byte, error) { return json.Marshal(b.String()) }

// readerBody wraps a ReaderBody's reader for one request. It exposes only
// Read and Close, never forwards Close to the caller's reader, delivers
// exactly size bytes, and fails at byte size+1 or at an early end, which
// also stops net/http from draining the rest of a long reader. detach stops
// every later read of the caller's reader.
type readerBody struct {
	mu       sync.Mutex
	r        io.Reader
	size     int64
	n        int64
	detached bool
	fail     atomic.Pointer[error]
}

// newReaderBody returns the readerBody of a ReaderBody: r must not be nil,
// and its declared size must be from 1 byte to limit.
func newReaderBody(r io.Reader, size, limit int64) (*readerBody, error) {
	switch {
	case r == nil:
		return nil, invalidRequest("Body", reasonNil)
	case size < 1 || size > limit:
		return nil, invalidRequest("Body", reasonOutOfRange)
	}
	return &readerBody{r: r, size: size}, nil
}

// Read delivers the next part of the body.
func (b *readerBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.detached {
		return 0, errTransportClosed
	}
	if f := b.fail.Load(); f != nil {
		return 0, *f
	}
	if len(p) == 0 {
		return 0, nil
	}
	if b.n >= b.size {
		return b.probeEnd()
	}
	if rem := b.size - b.n; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, stalled, err := readProgress(b.r, p)
	b.n += int64(n)
	switch {
	case stalled:
		return 0, b.setFail(errTransportStalled)
	case err == io.EOF: //nolint:errorlint // readProgress returns the reader's own io.EOF
		if b.n < b.size {
			return n, b.setFail(errTransportShortBody)
		}
		return n, nil
	case err != nil:
		return n, b.setFail(errTransportBodyRead)
	}
	return n, nil
}

// Close is the transport's signal that it is done with the body. The
// caller's reader is never closed.
func (b *readerBody) Close() error { return nil }

// probeEnd reads past the declared size to confirm the reader ends there.
func (b *readerBody) probeEnd() (int, error) {
	var one [1]byte
	n, stalled, err := readProgress(b.r, one[:])
	switch {
	case stalled:
		return 0, b.setFail(errTransportStalled)
	case n > 0:
		return 0, b.setFail(errTransportLongBody)
	case err == io.EOF: //nolint:errorlint // readProgress returns the reader's own io.EOF
		return 0, io.EOF
	}
	return 0, b.setFail(errTransportBodyRead)
}

// setFail records the body's failure and returns it.
func (b *readerBody) setFail(err error) error {
	b.fail.CompareAndSwap(nil, &err)
	return err
}

// failure returns the body's own failure, or nil.
func (b *readerBody) failure() error {
	if b == nil {
		return nil
	}
	if f := b.fail.Load(); f != nil {
		return *f
	}
	return nil
}

// detach stops every later read of the caller's reader. It waits for a read
// in progress to return.
func (b *readerBody) detach() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.detached = true
	b.mu.Unlock()
}
