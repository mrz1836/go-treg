package treg

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// reader is what a typed read needs from its client: the settings, the
// authentication (nil for the public routes) and the credential scan (nil
// when no token is known), or err when the client is not initialized.
type reader struct {
	cfg        *settings
	auth       func(http.Header)
	holdsToken func(string) bool
	err        error
}

// reader returns the typed-read plumbing of c, or one that fails with
// ErrInvalidConfig when c is not initialized.
func (c *Client) reader() reader {
	if err := c.ready(); err != nil {
		return reader{err: err}
	}
	return reader{cfg: c.cfg(), auth: c.auth, holdsToken: c.holdsToken}
}

// reader returns the typed-read plumbing of c, or one that fails with
// ErrInvalidConfig when c is not initialized. Its requests never carry a
// token.
func (c *CatalogClient) reader() reader {
	if err := c.ready(); err != nil {
		return reader{err: err}
	}
	return reader{cfg: c.cfg(), holdsToken: c.holdsToken}
}

// readRequest is one typed GET: the operation, the escaped path, the query
// and the accepted media type (JSON when empty). verbatim marks a body the
// SDK does not model, decoded without the strict rules.
type readRequest struct {
	op       Operation
	rawPath  string
	query    url.Values
	accept   string
	verbatim bool
}

// read makes a typed read: route validates the request and returns its
// readRequest. It makes the one GET and decodes its 2xx JSON body into T,
// under the strict rules (no repeated keys outside the raw islands) for an
// SDK model and without them for a verbatim body the SDK does not model,
// such as one json.RawMessage kept byte for byte. Typed reads accept the
// CaptureResponse option only.
func read[T any](ctx context.Context, r reader, route func() (readRequest, error), opts []CallOption,
) (*Result[T], error) {
	co := applyCallOptions(opts)
	rr, w, err := r.prepare(ctx, route, co.typedOnly)
	if err != nil {
		return nil, err
	}
	strict := rawIsland
	if rr.verbatim {
		strict = nil
	}
	return decodeExchange[T](ctx, r.cfg, w, co, strict)
}

// prepare runs the local checks of a typed read or Find, in the one order
// they all share, and returns the request and its wire: the client, the
// request route returns, the context, then options, the typed read's call
// option check (nil for Find, which takes none), then the URL bound and the
// credential scan.
func (r reader) prepare(ctx context.Context, route func() (readRequest, error), options func() error,
) (readRequest, *wire, error) {
	if r.err != nil {
		return readRequest{}, nil, r.err
	}
	rr, err := route()
	if err != nil {
		return readRequest{}, nil, err
	}
	if ctx == nil {
		return readRequest{}, nil, nilContext()
	}
	if options != nil {
		if err = options(); err != nil {
			return readRequest{}, nil, err
		}
	}
	w, err := r.wire(rr)
	if err != nil {
		return readRequest{}, nil, err
	}
	return rr, w, nil
}

// wire builds the request of a typed read.
func (r reader) wire(rr readRequest) (*wire, error) {
	rawQuery := rr.query.Encode()
	if len(rr.rawPath)+1+len(rawQuery) > maxURLBytes {
		return nil, invalidRequest("Request", reasonTooLong)
	}
	if r.holdsToken != nil {
		if r.holdsToken(percentDecode(rr.rawPath)) {
			return nil, invalidRequest("Request", reasonCredentialInRequest)
		}
		for k, vs := range rr.query {
			if r.holdsToken(k) || slices.ContainsFunc(vs, r.holdsToken) {
				return nil, invalidRequest("Request", reasonCredentialInRequest)
			}
		}
	}
	accept := rr.accept
	if accept == "" {
		accept = jsonContentType
	}
	return &wire{
		op:       rr.op,
		method:   http.MethodGet,
		rawPath:  rr.rawPath,
		rawQuery: rawQuery,
		header:   http.Header{headerAccept: {accept}},
		auth:     r.auth,
	}, nil
}

// readQuery validates the fields of a typed read and builds its query. It
// keeps the first error: once a field is refused, later calls do nothing,
// so a request checks its fields top to bottom and names the first invalid
// one.
type readQuery struct {
	values url.Values
	err    error
}

// get returns the GET of op at rawPath with the query, or the first error.
func (q *readQuery) get(op Operation, rawPath string) (readRequest, error) {
	if q.err != nil {
		return readRequest{}, q.err
	}
	return readRequest{op: op, rawPath: rawPath, query: q.values}, nil
}

// fail records reason, when it is not empty, as the error of field.
func (q *readQuery) fail(field, reason string) {
	if reason != "" && q.err == nil {
		q.err = invalidRequest(field, reason)
	}
}

// refuse records reason as the error of field when bad is true.
func (q *readQuery) refuse(bad bool, field, reason string) {
	if bad {
		q.fail(field, reason)
	}
}

// set adds key=v.
func (q *readQuery) set(key, v string) {
	if q.err != nil {
		return
	}
	if q.values == nil {
		q.values = url.Values{}
	}
	q.values.Set(key, v)
}

// setIf adds key=v when on is true.
func (q *readQuery) setIf(on bool, key, v string) {
	if on {
		q.set(key, v)
	}
}

// segment validates a path segment: valid returns "" for a valid one, else
// the reason field is refused with.
func (q *readQuery) segment(field, v string, valid func(string) string) { q.fail(field, valid(v)) }

// orgID validates an org id: a positive integer.
func (q *readQuery) orgID(id int64) { q.refuse(id <= 0, "OrgID", reasonRequired) }

// endpointID validates an endpoint id: the shape CatalogEndpoint requires.
func (q *readQuery) endpointID(id EndpointID) { q.segment("ID", string(id), checkCatalogID) }

// check adds key=v when v is not empty and valid accepts it: valid returns
// "" for a valid value, else the reason field is refused with.
func (q *readQuery) check(field, key, v string, valid func(string) string) {
	if v != "" {
		q.fail(field, valid(v))
		q.set(key, v)
	}
}

// date adds key=v when v is a YYYY-MM-DD date, and nothing when v is empty.
func (q *readQuery) date(field, key, v string) { q.check(field, key, v, checkDate) }

// text adds key=v, free text such as a search query: at most maxBytes bytes
// of valid UTF-8 without control characters. An empty v is sent too.
func (q *readQuery) text(field, key, v string, maxBytes int) {
	q.fail(field, checkText(v, maxBytes))
	q.set(key, v)
}

// intIn adds key=v for an optional number: 0 means omitted, anything else
// must lie in [1, hi]. Values are never clamped.
func (q *readQuery) intIn(field, key string, v, hi int64) {
	q.refuse(v < 0 || v > hi, field, reasonOutOfRange)
	q.setIf(v != 0, key, strconv.FormatInt(v, 10))
}

// id adds key=v for an optional positive id: 0 means omitted.
func (q *readQuery) id(field, key string, v int64) { q.intIn(field, key, v, math.MaxInt64) }

// nonNegative adds key=v for an optional count that must not be negative:
// 0 means omitted.
func (q *readQuery) nonNegative(field, key string, v int64) {
	q.refuse(v < 0, field, reasonNegative)
	q.setIf(v != 0, key, strconv.FormatInt(v, 10))
}

// oneOf adds key=v to q when v is one of allowed, and nothing when v is
// empty; any other value refuses field as invalid_format.
func oneOf[S ~string](q *readQuery, field, key string, v S, allowed ...S) {
	if v == "" {
		return
	}
	q.refuse(!slices.Contains(allowed, v), field, reasonInvalidFormat)
	q.set(key, string(v))
}

// checkText returns "" for free text sent as a query value, such as a
// search query: at most maxBytes bytes of valid UTF-8 without control
// characters. Otherwise it returns the reason.
func checkText(s string, maxBytes int) string {
	if len(s) > maxBytes {
		return reasonTooLong
	}
	if !utf8.ValidString(s) {
		return reasonInvalidCharacter
	}
	// unicode.IsControl is exactly C0, DEL and C1: U+0000-U+001F and
	// U+007F-U+009F.
	if strings.ContainsFunc(s, unicode.IsControl) {
		return reasonControlCharacter
	}
	return ""
}

// checkDate returns "" for a YYYY-MM-DD date, else invalid_format.
func checkDate(s string) string {
	if _, err := time.Parse(time.DateOnly, s); err != nil || len(s) != len(time.DateOnly) {
		return reasonInvalidFormat
	}
	return ""
}

// checkRef returns "" for a call reference or audit row id, 1 to 256 bytes
// of visible ASCII, else a reason.
func checkRef(ref string) string { return checkPathSegment(ref, maxRefBytes, isVisible) }
