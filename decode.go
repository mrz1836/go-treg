package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
)

// maxPrealloc bounds the buffer sized from a declared length before any
// byte arrives, so that a response that declares a large body and then
// stalls cannot pin that much memory until the timeout.
const maxPrealloc = 1 << 20

// readBody reads r to EOF, but never more than maxBytes+1 bytes, so a result
// longer than maxBytes means the body is too large. A small declared length,
// already known to be within the limit, sizes the buffer exactly, with one
// spare byte to see EOF without growing; any other body grows as bytes
// arrive.
func readBody(r io.Reader, contentLength, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(&progressReader{r: r}, maxBytes+1)
	if contentLength < 0 || contentLength > maxPrealloc {
		return io.ReadAll(limited)
	}
	buf := make([]byte, 0, contentLength+1)
	for len(buf) < cap(buf) {
		n, err := limited.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
	// The body is longer than declared: read on, still within the limit.
	rest, err := io.ReadAll(limited)
	return append(buf, rest...), err
}

// truncated reports whether a body of n bytes falls short of, or runs past,
// its declared length (-1 for unknown).
func truncated(n, declared int64) bool { return declared >= 0 && n != declared }

// maxEmptyReads is how many reads in a row may return neither bytes nor an
// error before a body counts as stalled. Every body read goes through
// readProgress, which stops there.
const maxEmptyReads = 100

// readProgress reads r into p, which is not empty, until a read returns
// bytes or an error, and returns that read's result. stalled reports instead
// that maxEmptyReads reads in a row returned neither, which each caller
// fails in its own way. Without it, a body from an injected transport, or a
// ReaderBody, that keeps answering (0, nil) would spin a CPU until the
// deadline.
func readProgress(r io.Reader, p []byte) (n int, stalled bool, err error) {
	for range maxEmptyReads {
		if n, err = r.Read(p); n > 0 || err != nil {
			return n, false, err
		}
	}
	return 0, true, nil
}

// progressReader fails a body that makes no progress with
// errTransportStalled; see readProgress.
type progressReader struct {
	r io.Reader
}

// Read reads from the wrapped reader.
func (p *progressReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return p.r.Read(b)
	}
	n, stalled, err := readProgress(p.r, b)
	if stalled {
		return 0, errTransportStalled
	}
	return n, err
}

// excerpt returns the first maxRefusalBytes of an error body, or nil when its
// read fails. A stalled read ends with the request's context, which closes
// the body.
func excerpt(body io.Reader) []byte {
	b, err := io.ReadAll(io.LimitReader(&progressReader{r: body}, maxRefusalBytes))
	if err != nil {
		return nil
	}
	return b
}

// encoded reports whether h carries a Content-Encoding other than identity.
// The transport removes the header when it decodes gzip itself, so one that
// remains names an encoding nobody decoded. Every value counts, under any
// spelling of the header name an injected transport may set.
func encoded(h http.Header) bool {
	for v := range foldedValues(h, headerContentEncoding) {
		for coding := range strings.SplitSeq(v, ",") {
			coding = strings.Trim(coding, " \t")
			if coding != "" && !strings.EqualFold(coding, "identity") {
				return true
			}
		}
	}
	return false
}

// mediaType returns the media type of h's single Content-Type, lowercased,
// "" when there is none, and ok false when it is repeated or unparsable.
func mediaType(h http.Header) (string, bool) {
	values := slices.Collect(foldedValues(h, headerContentType))
	switch len(values) {
	case 0:
		return "", true
	case 1:
	default:
		return "", false
	}
	mt, _, err := mime.ParseMediaType(values[0])
	if err != nil {
		return "", false
	}
	return strings.ToLower(mt), true
}

// jsonMediaType reports whether h declares JSON (application/json or a
// "+json" type) or no Content-Type at all.
func jsonMediaType(h http.Header) bool {
	mt, ok := mediaType(h)
	return ok && (mt == "" || mt == jsonContentType || strings.HasSuffix(mt, "+json"))
}

// decodeFailure is a decoding failure: a kind, a fixed reason and a cause.
type decodeFailure struct {
	kind   FailureKind
	reason string
	cause  error
}

// decodeValue decodes b, which must hold exactly one JSON value followed only
// by whitespace, into v. With useNumber, numbers in interfaces decode as
// json.Number.
func decodeValue(b []byte, v any, useNumber bool) *decodeFailure {
	dec := json.NewDecoder(bytes.NewReader(b))
	if useNumber {
		dec.UseNumber()
	}
	if err := dec.Decode(v); err != nil {
		return jsonFailure(err)
	}
	if rest := b[dec.InputOffset():]; len(bytes.TrimLeft(rest, " \t\r\n")) != 0 {
		return &decodeFailure{FailureMalformedResponse, reasonTrailingData, errTrailingData}
	}
	return nil
}

// jsonFailure classifies an encoding/json error without keeping it.
func jsonFailure(err error) *decodeFailure {
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return &decodeFailure{FailureMalformedResponse, reasonInvalidJSON, errInvalidJSON}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return &decodeFailure{FailureMalformedResponse, reasonTruncatedJSON, errInvalidJSON}
	}
	return &decodeFailure{FailureMalformedResponse, reasonDecode, errDecode}
}

// isJSONObject reports whether b, without surrounding white space, starts
// as a JSON object. It does not check the rest.
func isJSONObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{'
}
