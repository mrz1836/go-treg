package treg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// RawResponse is a buffered /call/ response from CallBytes: the status, the
// metadata and the whole body. It prints its status, metadata and body
// length only.
type RawResponse struct {
	status       int
	meta         Metadata
	decompressed bool
	statusErr    *CallError
	data         sealed[rawData]
}

// rawData is the sensitive part of a RawResponse.
type rawData struct {
	body   []byte
	header http.Header
}

// newRawResponse wraps a buffered exchange.
func newRawResponse(ex *exchange) *RawResponse {
	r := &RawResponse{
		status: ex.status, meta: ex.meta, decompressed: ex.decompressed,
		data: seal(rawData{body: ex.body, header: ex.header}),
	}
	if ex.status < 200 || ex.status >= 300 {
		r.statusErr = ex.statusError(OpCall, false)
	}
	return r
}

// StatusCode returns the HTTP status, or 0 for the zero RawResponse.
func (r *RawResponse) StatusCode() int {
	if r == nil {
		return 0
	}
	return r.status
}

// Meta returns the response metadata.
func (r *RawResponse) Meta() Metadata {
	if r == nil {
		return Metadata{}
	}
	return r.meta
}

// Body returns the body, after any transport decompression. The caller owns
// it: the SDK never reads it again. It is nil for a 3xx, whose body is never
// read.
func (r *RawResponse) Body() []byte {
	if r == nil || r.data == nil {
		return nil
	}
	return r.data().body
}

// Header returns a clone of every response header, Location and Set-Cookie
// included. Headers are sensitive: an upstream can put anything in them.
func (r *RawResponse) Header() http.Header {
	if r == nil || r.data == nil {
		return http.Header{}
	}
	return r.data().header.Clone()
}

// Decompressed reports whether the transport decompressed a gzip body.
func (r *RawResponse) Decompressed() bool { return r != nil && r.decompressed }

// StatusError returns nil for a 2xx. For a 3xx it returns
// ErrUnexpectedResponse; for a 4xx or 5xx, a *CallError matching
// ErrHTTPStatus with the Refusal parsed when the body was read. The zero
// RawResponse returns ErrInvalidConfig.
func (r *RawResponse) StatusError() error {
	if r == nil || r.data == nil {
		return notInitialized("response")
	}
	if r.statusErr == nil {
		return nil
	}
	return r.statusErr
}

// String returns the status, the metadata and the body length.
func (r RawResponse) String() string {
	n := 0
	if r.data != nil {
		n = len(r.data().body)
	}
	return "treg.RawResponse{status: " + strconv.Itoa(r.status) + ", bodyBytes: " + strconv.Itoa(n) +
		", meta: " + r.meta.String() + "}"
}

// GoString returns the same text as String.
func (r RawResponse) GoString() string { return r.String() }

// Format prints String for every verb.
func (r RawResponse) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the status, the metadata and the body length.
func (r RawResponse) MarshalJSON() ([]byte, error) {
	n := 0
	if r.data != nil {
		n = len(r.data().body)
	}
	return json.Marshal(responseJSON{Status: r.status, BodyBytes: int64(n), Meta: r.meta})
}

// responseJSON is the JSON form of Response and RawResponse.
type responseJSON struct {
	Status    int      `json:"status"`
	BodyBytes int64    `json:"body_bytes"`
	Meta      Metadata `json:"meta"`
}

// Result is a decoded 2xx answer: the value, the metadata and, when
// requested, the exact bytes decoded.
type Result[T any] struct {
	Value    T
	Meta     Metadata
	Captured Capture
}

// CaptureState says whether a Capture holds bytes.
type CaptureState uint8

// Capture states.
const (
	// CaptureDisabled means capture was not requested.
	CaptureDisabled CaptureState = iota

	// CaptureComplete means the whole 2xx body was read and kept, even if
	// decoding it then failed.
	CaptureComplete

	// CaptureUnavailable means capture was requested but no complete 2xx
	// body exists: a non-2xx, a body too large, a transport failure or a
	// done context.
	CaptureUnavailable
)

// String names the state.
func (s CaptureState) String() string {
	switch s {
	case CaptureComplete:
		return "complete"
	case CaptureUnavailable:
		return "unavailable"
	case CaptureDisabled:
	}
	return "disabled"
}

// Capture holds a copy of the exact 2xx bytes given to the decoder:
// whitespace, key order and number spellings as received. Non-2xx bytes are
// never captured (see WithErrorBodies). It prints its state and length only.
type Capture struct {
	state CaptureState
	data  sealed[[]byte]
}

// newCapture returns a complete Capture holding a private copy of b.
func newCapture(b []byte) Capture {
	c := bytes.Clone(b)
	if c == nil {
		c = []byte{}
	}
	return Capture{state: CaptureComplete, data: seal(c)}
}

// State returns the capture state.
func (c Capture) State() CaptureState { return c.state }

// Bytes returns a copy of the captured bytes, or nil unless the state is
// CaptureComplete. They may hold personal or provider data.
func (c Capture) Bytes() []byte {
	if c.state != CaptureComplete || c.data == nil {
		return nil
	}
	return bytes.Clone(c.data())
}

// String returns the state and the length.
func (c Capture) String() string {
	if c.state == CaptureComplete && c.data != nil {
		return "treg.Capture{state: complete, bytes: " + strconv.Itoa(len(c.data())) + "}"
	}
	return "treg.Capture{state: " + c.state.String() + "}"
}

// GoString returns the same text as String.
func (c Capture) GoString() string { return c.String() }

// Format prints String for every verb.
func (c Capture) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, c.String()) }

// MarshalJSON encodes the state and the length.
func (c Capture) MarshalJSON() ([]byte, error) {
	n := 0
	if c.state == CaptureComplete && c.data != nil {
		n = len(c.data())
	}
	return json.Marshal(struct {
		State string `json:"state"`
		Bytes int    `json:"bytes"`
	}{c.state.String(), n})
}
