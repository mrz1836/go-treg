package treg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
)

// decodeExchange makes the one attempt of w and decodes its 2xx answer into
// T. strict, when set, applies the strict check of SDK models with that
// skip rule.
func decodeExchange[T any](ctx context.Context, cfg *settings, w *wire, co callOptions,
	strict func(string) bool,
) (*Result[T], error) {
	capture := co.captureOn(cfg)
	ex, ce := cfg.roundTrip(ctx, w, modeDecode)
	if ce != nil {
		if capture {
			d := ce.detail()
			d.captured = unavailable(true)
			ce = ce.withDetail(d)
		}
		return nil, ce
	}
	if ex.status < 200 || ex.status >= 300 {
		return nil, ex.statusError(w.op, capture)
	}
	fail := func(kind FailureKind, reason string, cause error, captured Capture) error {
		ce := newCallError(w.op, kind, reason, ex.status, cause).withResponse(ex.meta, nil)
		return ce.withDetail(callDetail{cause: cause, captured: captured})
	}
	switch {
	case encoded(ex.header):
		return nil, fail(FailureUnexpectedResponse, reasonContentEncoding, errContentEncoding, unavailable(capture))
	case !co.ignoreContentType && !jsonMediaType(ex.header):
		return nil, fail(FailureUnexpectedResponse, reasonContentType, errContentType, captureOf(capture, ex.body))
	case len(bytes.TrimSpace(ex.body)) == 0:
		return nil, fail(FailureUnexpectedResponse, reasonEmptyBody, errEmptyBody, captureOf(capture, ex.body))
	}
	captured := captureOf(capture, ex.body)
	if strict != nil {
		if df := strictFailure(ex.body, strict); df != nil {
			return nil, fail(df.kind, df.reason, df.cause, captured)
		}
	}
	var v T
	if df := decodeValue(ex.body, &v, co.useNumber); df != nil {
		return nil, fail(df.kind, df.reason, df.cause, captured)
	}
	return &Result[T]{Value: v, Meta: ex.meta, Captured: captured}, nil
}

// captureOf returns a complete capture of body when capture is requested,
// and the disabled capture otherwise.
func captureOf(requested bool, body []byte) Capture {
	if requested {
		return newCapture(body)
	}
	return Capture{}
}

// unavailable returns the capture of a 2xx body that was requested but is
// not available.
func unavailable(requested bool) Capture {
	if requested {
		return Capture{state: CaptureUnavailable}
	}
	return Capture{}
}

// readMode selects how roundTrip reads a body.
type readMode uint8

const (
	// modeRaw reads the 2xx and 4xx/5xx bodies whole, within the limit
	// (CallBytes).
	modeRaw readMode = iota

	// modeDecode reads the 2xx body whole and only an excerpt of a 4xx or
	// 5xx body (CallJSON and typed reads).
	modeDecode
)

// received is the head of a response: what exchange and stream share.
type received struct {
	status       int
	header       http.Header // a private clone, Location restored
	meta         Metadata
	decompressed bool
}

// receive returns the head of resp, the response of op, with the Location
// the redirect guard moved into loc restored.
func receive(op Operation, resp *http.Response, loc *locationHolder) received {
	h := responseHeader(resp.Header, loc)
	return received{
		status:       resp.StatusCode,
		header:       h,
		meta:         parseMetadata(op, resp.StatusCode, h),
		decompressed: resp.Uncompressed,
	}
}

// exchange is a received response, read by roundTrip.
type exchange struct {
	received

	body    []byte
	refusal *Refusal
	errBody []byte // the 4xx/5xx excerpt, kept with WithErrorBodies
}

// attempt holds the evidence one dispatch leaves behind.
type attempt struct {
	wrote atomic.Bool
	loc   locationHolder
}

// send builds the HTTP request for w on the configured origin, with the
// token headers only when w carries auth, and makes the one attempt. The
// error is errBuildRequest or the http.Client's own error, which the caller
// classifies and drops.
func (s *settings) send(ctx context.Context, w *wire, at *attempt) (*http.Response, error) {
	u := &url.URL{Scheme: "https", Host: s.host, Path: percentDecode(w.rawPath), RawPath: w.rawPath, RawQuery: w.rawQuery}
	if u.EscapedPath() != w.rawPath {
		return nil, errBuildRequest
	}
	ctx = context.WithValue(ctx, locationKey{}, &at.loc)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteHeaders: func() { at.wrote.Store(true) }})
	req, err := http.NewRequestWithContext(ctx, w.method, "", nil)
	if err != nil {
		// The standard library's error can quote the method.
		return nil, errBuildRequest
	}
	req.URL, req.Host = u, u.Host
	h := w.header
	h[headerUserAgent] = []string{s.userAgent}
	if w.auth != nil {
		w.auth(h)
	}
	req.Header = h
	// The standard library resends a request on its own when it thinks that
	// is safe: HTTP/1 retries a request with a nil body, http.NoBody or a
	// GetBody, and HTTP/2 resends such requests after some stream and
	// connection errors. Every request therefore has GetBody nil, and every
	// body but the empty POST, PUT or PATCH is neither nil nor http.NoBody:
	// one attempt per call.
	switch {
	case w.reader != nil:
		req.Body, req.ContentLength = w.reader, w.reader.size
	case len(w.body) > 0:
		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(w.body)), int64(len(w.body))
	case bodylessMethod(w.method):
		req.Body, req.ContentLength = noReplayBody{}, 0
		req.TransferEncoding = []string{"identity"}
	default:
		// An empty POST, PUT or PATCH is sent with Content-Length: 0, which
		// servers expect. This is the documented residual: HTTP/2 may resend
		// it after GOAWAY, REFUSED_STREAM or a peer PROTOCOL_ERROR.
		req.Body, req.ContentLength = http.NoBody, 0 //nolint:forbidigo // the documented empty-write case
	}
	req.GetBody = nil
	return s.hc.Do(req) //nolint:gosec // the URL is the validated configured origin plus a validated path
}

// roundTrip makes the one attempt of w and reads its response within the
// configured timeout. A context that is already done sends nothing.
func (s *settings) roundTrip(ctx context.Context, w *wire, mode readMode) (*exchange, *CallError) {
	defer w.reader.detach()
	if err := ctx.Err(); err != nil {
		return nil, contextFailure(w.op, err, 0)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	at := &attempt{}
	resp, err := s.send(callCtx, w, at) //nolint:bodyclose // closeOnce(resp.Body) below closes it
	if err != nil {
		return nil, s.dispatchFailure(ctx, callCtx, w, err, at)
	}
	closeBody := closeOnce(resp.Body)
	stop := context.AfterFunc(callCtx, closeBody)
	defer func() {
		stop()
		closeBody()
	}()
	ex := &exchange{received: receive(w.op, resp, &at.loc)}
	if ce := s.readResponse(ctx, callCtx, w.op, resp, ex, mode); ce != nil {
		// No Refusal was read: every failure comes before the excerpt.
		return nil, ce.withResponse(ex.meta, nil)
	}
	return ex, nil
}

// closeOnce returns a function that closes body exactly once. It holds the
// body only: holding the response would keep its request, whose header
// holds the token, reachable as long as the function lives.
func closeOnce(body io.Closer) func() {
	return sync.OnceFunc(func() { _ = body.Close() })
}

// readResponse reads into ex what mode needs of resp's body: a 2xx whole,
// unless modeDecode will refuse its encoding unread; a 4xx or 5xx whole in
// modeRaw and as an excerpt otherwise; never a 1xx or 3xx.
func (s *settings) readResponse(ctx, callCtx context.Context, op Operation, resp *http.Response, ex *exchange,
	mode readMode,
) *CallError {
	switch {
	case ex.status < 200:
		// A 1xx that reaches here, such as 101 Switching Protocols, has the
		// raw connection as its body, which no deadline watches: never read it.
		return newCallError(op, FailureUnexpectedResponse, reasonInformational, ex.status, errInformational)
	case ex.status < 300:
		if mode == modeDecode && encoded(ex.header) {
			return nil // decodeExchange refuses it without reading
		}
		return s.readWhole(ctx, callCtx, op, resp, ex)
	case ex.status < 400:
		// A redirect is never followed and its body never read.
	case mode == modeRaw:
		if ce := s.readWhole(ctx, callCtx, op, resp, ex); ce != nil {
			return ce
		}
		// The whole body is read even when it is encoded, so its error body
		// is kept as received; only the Refusal needs it decoded. ex.body is
		// the caller's too (RawResponse.Body), so the error body is a copy.
		head := ex.body[:min(len(ex.body), maxRefusalBytes)]
		if s.errorBodies {
			head = bytes.Clone(head)
		}
		ex.noteRefusal(head, s.errorBodies)
	default:
		// An encoded error body is never read: it would yield nothing.
		if !encoded(ex.header) {
			ex.noteRefusal(excerpt(resp.Body), s.errorBodies)
		}
	}
	return nil
}

// readWhole reads resp's whole body into ex.body, within the response limit
// of op.
func (s *settings) readWhole(ctx, callCtx context.Context, op Operation, resp *http.Response, ex *exchange) *CallError {
	limit := s.responseLimit(op)
	declared := declaredLength(resp)
	if declared > limit {
		return newCallError(op, FailureResponseTooLarge, "", ex.status, errResponseTooLarge)
	}
	data, err := readBody(resp.Body, declared, limit)
	switch {
	case err != nil:
		return classify(ctx, callCtx, op, err, ex.status, nil)
	case int64(len(data)) > limit:
		return newCallError(op, FailureResponseTooLarge, "", ex.status, errResponseTooLarge)
	case truncated(int64(len(data)), declared):
		// The standard transport reports a short body as io.ErrUnexpectedEOF;
		// an injected one might end it cleanly instead. Either way it is
		// incomplete.
		return transportFailure(op, ex.status, errTransportClosed)
	}
	ex.body = data
	return nil
}

// declaredLength returns the body length resp's Content-Length declares, or
// -1 when it is unknown or describes a body that is never sent: for HEAD,
// 204 or 304.
func declaredLength(resp *http.Response) int64 {
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return -1
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return -1
	}
	return resp.ContentLength
}

// dispatchFailure classifies a failed http.Client.Do.
func (s *settings) dispatchFailure(caller, sdk context.Context, w *wire, err error, at *attempt) *CallError {
	if errors.Is(err, errBuildRequest) {
		return transportFailure(w.op, 0, errBuildRequest)
	}
	ce := classify(caller, sdk, w.op, err, 0, w.reader)
	if w.op != OpCall {
		return ce
	}
	switch {
	case ce.Kind == FailureUnexpectedResponse:
		ce.OutcomeUnknown = statusOutcome(ce.StatusCode, nil)
	case ce.Kind == FailureTransport && transportClassOf(ce.Unwrap()).preDispatch:
	case at.wrote.Load():
		ce.OutcomeUnknown = true
	default:
		// The SDK's own transport reports WroteHeaders; an injected one may
		// not, so without that evidence the outcome stays open.
		ce.OutcomeUnknown = s.injected
	}
	return ce
}

// statusOutcome reports whether a received status leaves a /call/ outcome
// open: only a 5xx that is not a proven pre-relay refusal.
func statusOutcome(status int, r *Refusal) bool {
	return status >= 500 && !preRelayRefusal(r)
}

// responseHeader returns a private clone of h with the Location the redirect
// guard removed restored.
func responseHeader(h http.Header, loc *locationHolder) http.Header {
	out := h.Clone()
	if out == nil {
		out = http.Header{}
	}
	if v := loc.get(); len(v) > 0 {
		out[headerLocation] = v
	}
	return out
}

// noteRefusal records the start of a 4xx or 5xx body: the Refusal parsed
// from it, unless the body is still encoded, and, when keep is set
// (WithErrorBodies), its first maxRefusalBytes as the error body. The error
// body is kept without a copy and reaches the caller through CallError.Body,
// so with keep set, body must be memory nothing else holds, such as a fresh
// excerpt; a caller passing a buffer it shares clones it first.
func (ex *exchange) noteRefusal(body []byte, keep bool) {
	if !encoded(ex.header) {
		ex.refusal = parseRefusal(body)
	}
	if keep {
		ex.errBody = body[:min(len(body), maxRefusalBytes)]
	}
}

// statusError returns the CallError for a non-2xx exchange. Its body is the
// excerpt kept with WithErrorBodies, if any.
func (ex *exchange) statusError(op Operation, capture bool) *CallError {
	var ce *CallError
	switch {
	case ex.status >= 300 && ex.status < 400:
		ce = newCallError(op, FailureUnexpectedResponse, reasonRedirect, ex.status, errRedirect)
	case ex.status < 300:
		ce = newCallError(op, FailureUnexpectedResponse, reasonInformational, ex.status, errInformational)
	default:
		ce = newCallError(op, FailureHTTPStatus, "", ex.status, errStatus)
	}
	// A 1xx or 3xx body is never read, so only a 4xx or 5xx has a Refusal.
	ce = ce.withResponse(ex.meta, ex.refusal)
	return ce.withDetail(callDetail{cause: ce.Unwrap(), body: ex.errBody, captured: unavailable(capture)})
}
