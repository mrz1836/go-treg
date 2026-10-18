package treg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Request bounds.
const (
	maxHeaderCount       = 64
	maxHeaderValueBytes  = 8 << 10
	maxHeaderTotalBytes  = 16 << 10
	maxHeaderNameBytes   = 256
	maxContentTypeBytes  = 1 << 10
	maxURLBytes          = 16 << 10
	callPathPrefix       = "/call/"
	formContentType      = "application/x-www-form-urlencoded"
	jsonContentType      = "application/json"
	maxProviderAuthBytes = maxHeaderValueBytes
)

// CallRequest is one /call/ request: the target, the upstream request and
// Treg's controls. Every field but Target and Method is optional.
//
// The call snapshots the request before dispatch: Query, Header and the
// in-memory bodies are encoded or copied, so the caller may reuse them once
// the method returns. A ReaderBody's reader is read until the method
// returns, or for Call until Response.Close.
//
// A CallRequest prints its shape only: never header values, query values,
// the provider authorization, the target or the body.
type CallRequest struct {
	// Target is what Treg resolves: see CatalogEndpoint, RegisteredTool and
	// UpstreamURL. Required.
	Target Target

	// Method is the upstream HTTP method: GET, HEAD, POST, PUT, PATCH,
	// DELETE or OPTIONS, in upper case. Required.
	Method string

	// Query is the upstream query, encoded with url.Values.Encode: keys
	// sorted, each key's values in order, repeats and blank values kept.
	// Path parameters of a catalog endpoint ("{id}") ride here too; Treg
	// fills them in. Set Query or RawQuery, not both.
	Query url.Values

	// RawQuery is an already-encoded query, sent to Treg byte for byte.
	// Treg decodes and re-encodes the query for the upstream, so byte
	// fidelity ends at Treg. It may not hold "#", spaces or control
	// characters, and every "%" must start a valid %HH escape.
	RawQuery string

	// Header holds upstream request headers, such as a provider's required
	// "model" header. Names must be valid tokens; values visible ASCII,
	// spaces and tabs, without leading or trailing whitespace. Framing,
	// routing, cookie, authorization, idempotency and X-Treg-* names, and
	// the SDK-owned Content-Type, Content-Encoding, Accept-Encoding and
	// User-Agent, are refused. At most 64 values, 8 KiB each, 16 KiB in
	// all. Header values are sensitive.
	Header http.Header

	// ProviderAuthorization is sent as the upstream Authorization header,
	// the only way to send one: Treg relays it to the provider verbatim.
	// Never put the Treg token here. Sensitive.
	ProviderAuthorization string

	// Body is the request body; the zero Body sends none.
	Body Body

	// Controls are Treg's request controls.
	Controls Controls
}

// String returns the request's shape, such as
// "treg.CallRequest{target: catalog_endpoint, method: POST, body: json}".
func (r CallRequest) String() string {
	fields := []string{"target: " + r.Target.Kind().String()}
	if validMethod(r.Method) {
		fields = append(fields, "method: "+r.Method)
	} else if r.Method != "" {
		fields = append(fields, "method: invalid")
	}
	if len(r.Query) > 0 {
		fields = append(fields, "query: "+strconv.Itoa(len(r.Query))+" keys")
	}
	if r.RawQuery != "" {
		fields = append(fields, "rawQuery: set")
	}
	if len(r.Header) > 0 {
		fields = append(fields, "header: "+strconv.Itoa(len(r.Header))+" names")
	}
	if r.ProviderAuthorization != "" {
		fields = append(fields, "providerAuthorization: set")
	}
	if r.Body.kind != bodyNone {
		fields = append(fields, "body: "+r.Body.kind.String())
	}
	if c := r.Controls.shape(); c != "" {
		fields = append(fields, "controls: "+c)
	}
	return "treg.CallRequest{" + strings.Join(fields, ", ") + "}"
}

// GoString returns the same text as String.
func (r CallRequest) GoString() string { return r.String() }

// Format prints String for every verb.
func (r CallRequest) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes String as a JSON string, so that a logged request
// shows its shape only.
func (r CallRequest) MarshalJSON() ([]byte, error) { return json.Marshal(r.String()) }

// wire is a validated, snapshotted request ready to dispatch.
type wire struct {
	op       Operation
	method   string
	rawPath  string // escaped path, starting with "/"
	rawQuery string
	header   http.Header
	body     []byte // in-memory body; nil or empty for none
	reader   *readerBody
	auth     func(http.Header) // sets the token headers; nil for the public routes
}

// scanList collects the caller strings of a request, which the credential
// scan checks before anything is sent.
type scanList []string

// add appends ss.
func (l *scanList) add(ss ...string) { *l = append(*l, ss...) }

// addPairs appends every key and value of m: a query, form or header.
func (l *scanList) addPairs(m map[string][]string) {
	for k, vs := range m {
		l.add(k)
		l.add(vs...)
	}
}

// buildCall validates req and snapshots it into a wire, in a fixed order.
// Nothing is sent when it fails.
func (c *Client) buildCall(req CallRequest) (*wire, error) {
	rest, err := req.Target.rest("Target")
	if err != nil {
		return nil, err
	}
	if !validMethod(req.Method) {
		if req.Method == "" {
			return nil, invalidRequest("Method", reasonRequired)
		}
		return nil, invalidRequest("Method", reasonInvalidMethod)
	}
	w := &wire{op: OpCall, method: req.Method, rawPath: callPathPrefix + rest, auth: c.auth}
	scan := scanList{rest, percentDecode(rest)}
	if w.rawQuery, err = buildQuery(req.Query, req.RawQuery, &scan); err != nil {
		return nil, err
	}
	if len(w.rawPath)+1+len(w.rawQuery) > maxURLBytes {
		return nil, invalidRequest("Target", reasonTooLong)
	}
	if w.header, err = copyUpstreamHeader(req.Header, &scan); err != nil {
		return nil, err
	}
	if p := req.ProviderAuthorization; p != "" {
		if reason := checkHeaderValue(p, maxProviderAuthBytes); reason != "" {
			return nil, invalidRequest("ProviderAuthorization", reason)
		}
		w.header[headerAuthorization] = []string{p}
		scan.add(p)
	}
	if err = req.Controls.apply(w.header, &scan); err != nil {
		return nil, err
	}
	if err = c.attachBody(w, req.Body, &scan); err != nil {
		return nil, err
	}
	if slices.ContainsFunc(scan, c.holdsToken) {
		return nil, invalidRequest("CallRequest", reasonCredentialInRequest)
	}
	if c.holdsTokenBytes(w.body) {
		return nil, invalidRequest("Body", reasonCredentialInRequest)
	}
	return w, nil
}

// buildQuery validates and encodes the query, adding its decoded strings to
// scan.
func buildQuery(q url.Values, raw string, scan *scanList) (string, error) {
	if len(q) > 0 && raw != "" {
		return "", invalidRequest("RawQuery", reasonConflict)
	}
	if raw != "" {
		if len(raw) > maxURLBytes {
			return "", invalidRequest("RawQuery", reasonTooLong)
		}
		if reason := checkEscaped(raw, isRawQueryChar); reason != "" {
			return "", invalidRequest("RawQuery", reason)
		}
		scan.add(raw, percentDecode(strings.ReplaceAll(raw, "+", " ")))
		return raw, nil
	}
	scan.addPairs(q)
	return q.Encode(), nil
}

// copyUpstreamHeader validates the caller's upstream headers and returns a
// fresh header map holding them, plus room for the SDK's own.
func copyUpstreamHeader(src http.Header, scan *scanList) (http.Header, error) {
	h := make(http.Header, len(src)+8)
	// A fixed order, so the first error is deterministic. A plain loop:
	// slices.Sorted(maps.Keys(src)) costs four allocations per call.
	names := make([]string, 0, len(src))
	for k := range src {
		names = append(names, k)
	}
	slices.Sort(names)
	count, total := 0, 0
	for _, k := range names {
		field := "Header[" + printable(k, shortHeaderName, "?") + "]"
		if reason := checkBounded(k, maxHeaderNameBytes, isTokenChar); reason != "" {
			return nil, invalidRequest("Header", reason)
		}
		if reservedHeader(k) {
			return nil, invalidRequest(field, reasonReservedHeader)
		}
		for _, v := range src[k] {
			if reason := checkHeaderValue(v, maxHeaderValueBytes); reason != "" {
				return nil, invalidRequest(field, reason)
			}
			count++
			total += len(k) + len(v)
		}
		if count > maxHeaderCount {
			return nil, invalidRequest("Header", reasonTooMany)
		}
		if total > maxHeaderTotalBytes {
			return nil, invalidRequest("Header", reasonTooLarge)
		}
		if len(src[k]) > 0 {
			h[k] = slices.Clone(src[k])
			scan.add(k)
			scan.add(src[k]...)
		}
	}
	return h, nil
}

// attachBody snapshots b into w and sets Content-Type.
func (c *Client) attachBody(w *wire, b Body, scan *scanList) error {
	if b.kind == bodyNone {
		return nil
	}
	if b.data == nil {
		return invalidRequest("Body", reasonNotInitialized)
	}
	d := b.data()
	ct, err := bodyContentType(b.kind, d.contentType, scan)
	if err != nil {
		return err
	}
	limit := c.cfg().maxRequestBytes
	switch b.kind {
	case bodyJSON:
		if w.body, err = json.Marshal(d.value); err != nil {
			return invalidRequest("Body", reasonJSONMarshal)
		}
	case bodyBytes:
		w.body = d.bytes // copied below, once it is known to fit
	case bodyForm:
		scan.addPairs(d.form)
		w.body = []byte(d.form.Encode())
	case bodyReader:
		if w.reader, err = newReaderBody(d.reader, b.size, limit); err != nil {
			return err
		}
	case bodyNone:
	}
	if int64(len(w.body)) > limit {
		return invalidRequest("Body", reasonTooLarge)
	}
	if b.kind == bodyBytes {
		w.body = bytes.Clone(w.body)
	}
	w.header[headerContentType] = []string{ct}
	return nil
}

// bodyContentType returns the Content-Type a Body of kind is sent with: the
// SDK's own for JSONBody and FormBody, else the caller's ct, validated and
// added to scan.
func bodyContentType(kind bodyKind, ct string, scan *scanList) (string, error) {
	switch kind {
	case bodyJSON:
		return jsonContentType, nil
	case bodyForm:
		return formContentType, nil
	case bodyBytes, bodyReader, bodyNone:
	}
	if reason := headerValueReason(ct, maxContentTypeBytes); reason != "" {
		return "", invalidRequest("Body.ContentType", reason)
	}
	scan.add(ct)
	return ct, nil
}

// validMethod reports whether m is one of the methods /call/ accepts.
func validMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// bodylessMethod reports whether a request without a body uses noReplayBody
// rather than an empty body with Content-Length: 0.
func bodylessMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}
