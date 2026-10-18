package treg

import (
	"context"
	"net/http"
)

// Call sends one /call/ request and returns the response as a stream, for
// any content: binary audio, server-sent events, large downloads or plain
// JSON. It makes exactly one attempt, never retries and never follows a
// redirect.
//
// Any status from 2xx to 5xx returns a *Response; check it with
// StatusError. A 1xx is ErrUnexpectedResponse. The caller must Close the
// Response, which ends the request and its timers. Response.Read stops at
// WithMaxStreamBytes, and the whole stream at WithStreamTimeout; the wait
// for response headers is bounded by the lower of WithTimeout and
// WithStreamTimeout.
//
// Errors before a response are *ValidationError (nothing was sent) or
// *CallError.
func (c *Client) Call(ctx context.Context, req CallRequest) (*Response, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, nilContext()
	}
	w, err := c.buildCall(req)
	if err != nil {
		return nil, err
	}
	st, ce := c.cfg().openStream(ctx, w, c.cfg().maxStreamBytes)
	if ce != nil {
		return nil, ce
	}
	return newResponse(st), nil
}

// CallBytes sends one /call/ request and reads the whole response body,
// within WithMaxResponseBytes and WithTimeout. Any status from 2xx to 5xx
// returns a *RawResponse, so a refusal stays inspectable: check it with
// StatusError. A 3xx body is never read. A 1xx, a body over the limit, a
// transport failure or a timeout is an error.
func (c *Client) CallBytes(ctx context.Context, req CallRequest) (*RawResponse, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, nilContext()
	}
	w, err := c.buildCall(req)
	if err != nil {
		return nil, err
	}
	ex, ce := c.cfg().roundTrip(ctx, w, modeRaw)
	if ce != nil {
		return nil, ce
	}
	return newRawResponse(ex), nil
}

// CallJSON sends one /call/ request and decodes a 2xx JSON body into T, a
// type the caller owns. The body must be one JSON value followed only by
// whitespace, with Content-Type application/json, a "+json" type or none
// (see IgnoreContentType). Objects, arrays, scalars and null all decode when
// T allows them.
//
// Every non-2xx is an error: a 4xx or 5xx is ErrHTTPStatus with the
// Refusal parsed, a 1xx or 3xx is ErrUnexpectedResponse. A 2xx that is
// empty (204 included), too large or undecodable is an error too, but the
// call executed: Meta keeps the call ref and cost, and the request must not
// be resubmitted. HEAD is refused locally.
//
// It accepts the CaptureResponse, UseNumber and IgnoreContentType options.
func CallJSON[T any](ctx context.Context, c *Client, req CallRequest, opts ...CallOption) (*Result[T], error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, nilContext()
	}
	co := applyCallOptions(opts)
	if req.Method == http.MethodHead {
		return nil, invalidRequest("Method", reasonNotApplicable)
	}
	w, err := c.buildCall(req)
	if err != nil {
		return nil, err
	}
	return decodeExchange[T](ctx, c.cfg(), w, co, nil)
}

// CallOption adjusts one CallJSON or typed read. A nil CallOption is
// skipped. An option a method does not accept is ErrInvalidRequest with
// reason "not_applicable".
type CallOption func(*callOptions)

// callOptions is the per-call option record.
type callOptions struct {
	capture           *bool
	useNumber         bool
	ignoreContentType bool
}

// CaptureResponse overrides WithResponseCapture for one call.
func CaptureResponse(on bool) CallOption {
	return func(o *callOptions) { o.capture = &on }
}

// UseNumber decodes numbers held in interface values as json.Number
// (json.Decoder.UseNumber). CallJSON only.
func UseNumber() CallOption {
	return func(o *callOptions) { o.useNumber = true }
}

// IgnoreContentType decodes a 2xx body whatever its Content-Type. CallJSON
// only.
func IgnoreContentType() CallOption {
	return func(o *callOptions) { o.ignoreContentType = true }
}

// applyCallOptions runs each non-nil option.
func applyCallOptions(opts []CallOption) callOptions {
	var o callOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// typedOnly reports an option a typed read does not accept.
func (o callOptions) typedOnly() error {
	switch {
	case o.useNumber:
		return invalidRequest("UseNumber", reasonNotApplicable)
	case o.ignoreContentType:
		return invalidRequest("IgnoreContentType", reasonNotApplicable)
	}
	return nil
}

// captureOn reports whether this call captures its 2xx body.
func (o callOptions) captureOn(cfg *settings) bool {
	if o.capture != nil {
		return *o.capture
	}
	return cfg.capture
}
