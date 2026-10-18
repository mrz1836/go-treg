package treg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Sentinel errors. Match them with errors.Is.
//
// ErrInvalidConfig and ErrInvalidRequest are local: an error matching either
// means no request was sent. Every other sentinel describes a request that
// was attempted, or one that a done context stopped before it was sent.
var (
	// ErrInvalidConfig reports an invalid NewClient or NewCatalogClient
	// argument or option, or a value not built by this package. No request
	// was sent.
	ErrInvalidConfig = errors.New("treg: invalid configuration")

	// ErrInvalidRequest reports a request that fails local validation, a nil
	// context, or an option a method does not accept. No request was sent.
	ErrInvalidRequest = errors.New("treg: invalid request")

	// ErrHTTPStatus reports an HTTP 4xx or 5xx status where a 2xx was
	// required: from CallJSON, every typed read, and the StatusError methods.
	// CallError.StatusCode and CallError.Refusal say more.
	ErrHTTPStatus = errors.New("treg: HTTP error status")

	// ErrGatewayRefusal reports an HTTP 4xx or 5xx that Treg marked as its own
	// refusal (X-Treg-Error: 1), or a 503 whose body says
	// {"treg_saturated": true}. It also matches ErrHTTPStatus. The marker is
	// evidence, not proof: an upstream reached on its own key can send the
	// same header.
	ErrGatewayRefusal = errors.New("treg: gateway refusal")

	// ErrUnexpectedResponse reports a response this SDK cannot interpret: a
	// 1xx or 3xx status (a redirect is never followed), a 2xx with an empty
	// body or a Content-Type or Content-Encoding the decoder refuses, or
	// response headers over 64 KiB.
	ErrUnexpectedResponse = errors.New("treg: unexpected response")

	// ErrMalformedResponse reports a 2xx body that is not one valid JSON
	// value followed only by whitespace, that does not decode into the
	// requested type (a quoted number where an SDK model expects a number
	// included), or that repeats an object key in an SDK model.
	ErrMalformedResponse = errors.New("treg: malformed response")

	// ErrResponseTooLarge reports a body larger than the configured limit
	// (WithMaxResponseBytes, WithMaxStreamBytes) or a fixed SDK bound.
	ErrResponseTooLarge = errors.New("treg: response too large")

	// ErrTransport reports a failure in transit. CallError.Reason names one of
	// a few fixed classes: dns, proxy, dial, tls, reset, closed, stalled,
	// short_body, long_body, body_read or other. The request may or may not
	// have reached Treg.
	ErrTransport = errors.New("treg: transport failure")

	// ErrTimeout reports that one of the SDK's own deadlines expired
	// (WithTimeout, WithStreamTimeout, or an injected http.Client's Timeout).
	// It also matches context.DeadlineExceeded, so check it first to tell it
	// apart from the caller's own deadline.
	ErrTimeout = errors.New("treg: timeout")

	// ErrOutcomeUnknown matches a CallError whose OutcomeUnknown is true: a
	// /call/ request that may have executed, and may have been billed,
	// although no complete answer arrived. Reconcile through Calls().Get or
	// Calls().List before repeating it; the SDK never retries or invents an
	// idempotency key.
	ErrOutcomeUnknown = errors.New("treg: outcome unknown")
)

// Fixed causes kept inside a CallError. They replace standard-library errors,
// whose messages can quote URLs, header bytes, tokens or response content.
var (
	errBuildRequest       = errors.New("treg: cannot build the HTTP request")
	errStatus             = errors.New("treg: HTTP error status")
	errInformational      = errors.New("treg: informational (1xx) status")
	errRedirect           = errors.New("treg: redirect (3xx) status; redirects are never followed")
	errEmptyBody          = errors.New("treg: response has an empty body")
	errContentType        = errors.New("treg: response has a content type the decoder refuses")
	errContentEncoding    = errors.New("treg: response has an undecoded content encoding")
	errHeadersTooLarge    = errors.New("treg: response headers exceed 64 KiB")
	errResponseTooLarge   = errors.New("treg: response body exceeds the size limit")
	errInvalidJSON        = errors.New("treg: response is not valid JSON")
	errTrailingData       = errors.New("treg: response has data after the top-level value")
	errDuplicateKey       = errors.New("treg: response repeats an object key")
	errDecode             = errors.New("treg: response does not decode into the requested type")
	errTooDeep            = errors.New("treg: response nests too deeply")
	errHeaderTimeout      = errors.New("treg: response headers did not arrive in time")
	errStreamClosed       = errors.New("treg: stream closed")
	errTransportDNS       = errors.New("treg: transport: DNS lookup failed")
	errTransportProxy     = errors.New("treg: transport: proxy connection failed")
	errTransportDial      = errors.New("treg: transport: connection failed")
	errTransportTLS       = errors.New("treg: transport: TLS failed")
	errTransportReset     = errors.New("treg: transport: connection reset")
	errTransportClosed    = errors.New("treg: transport: connection closed early")
	errTransportStalled   = errors.New("treg: transport: body read made no progress")
	errTransportShortBody = errors.New("treg: transport: request body ended before its declared size")
	errTransportLongBody  = errors.New("treg: transport: request body ran past its declared size")
	errTransportBodyRead  = errors.New("treg: transport: request body read failed")
	errTransportOther     = errors.New("treg: transport: request failed")
)

// FailureKind classifies a CallError. Each kind matches one target with
// errors.Is (a sentinel of this package, context.Canceled or
// context.DeadlineExceeded), except FailureTimeout, which matches both
// ErrTimeout and context.DeadlineExceeded. ErrGatewayRefusal and
// ErrOutcomeUnknown are not kinds: they match through
// CallError.GatewayMarked, CallError.Refusal and CallError.OutcomeUnknown.
// The values are short and fixed, so they are safe as metric labels.
type FailureKind string

// Failure kinds.
const (
	// FailureHTTPStatus matches ErrHTTPStatus.
	FailureHTTPStatus FailureKind = "http_status"

	// FailureUnexpectedResponse matches ErrUnexpectedResponse.
	FailureUnexpectedResponse FailureKind = "unexpected_response"

	// FailureMalformedResponse matches ErrMalformedResponse.
	FailureMalformedResponse FailureKind = "malformed_response"

	// FailureResponseTooLarge matches ErrResponseTooLarge.
	FailureResponseTooLarge FailureKind = "response_too_large"

	// FailureTransport matches ErrTransport.
	FailureTransport FailureKind = "transport"

	// FailureTimeout matches ErrTimeout, and also context.DeadlineExceeded.
	FailureTimeout FailureKind = "timeout"

	// FailureCanceled means the caller's context was canceled. It matches
	// context.Canceled.
	FailureCanceled FailureKind = "canceled"

	// FailureDeadlineExceeded means the caller's context deadline expired. It
	// matches context.DeadlineExceeded, but not ErrTimeout.
	FailureDeadlineExceeded FailureKind = "deadline_exceeded"
)

// Operation names the request a CallError or Metadata concerns.
type Operation string

// Operations. OpCall covers every /call/ request; each typed read has its own.
const (
	// OpCall is /call/{rest}: Call, CallBytes and CallJSON.
	OpCall Operation = "call"

	// OpServerInfo is GET /meta (CatalogClient.ServerInfo).
	OpServerInfo Operation = "server_info"

	// OpCatalogPlatforms is GET /catalog/platforms.
	OpCatalogPlatforms Operation = "catalog_platforms"

	// OpCatalogPlatform is GET /catalog/platforms/{slug}.
	OpCatalogPlatform Operation = "catalog_platform"

	// OpCatalogProvider is GET /catalog/providers/{service}.
	OpCatalogProvider Operation = "catalog_provider"

	// OpCatalogSearch is GET /catalog/search.
	OpCatalogSearch Operation = "catalog_search"

	// OpCatalogFind is GET /catalog/find (NDJSON).
	OpCatalogFind Operation = "catalog_find"

	// OpCatalogEndpoint is GET /catalog/endpoints/{id}.
	OpCatalogEndpoint Operation = "catalog_endpoint"

	// OpCatalogExample is GET /catalog/examples/{id}.
	OpCatalogExample Operation = "catalog_example"

	// OpEndpointAccess is GET /catalog/endpoints/{id}/access.
	OpEndpointAccess Operation = "endpoint_access"

	// OpAuthMe is GET /auth/me (Account().Me).
	OpAuthMe Operation = "auth_me"

	// OpListOrgs is GET /orgs (Account().Orgs).
	OpListOrgs Operation = "list_orgs"

	// OpUsageMe is GET /usage/me (Account().Usage).
	OpUsageMe Operation = "usage_me"

	// OpListCalls is GET /calls (Calls().List).
	OpListCalls Operation = "list_calls"

	// OpGetCall is GET /calls/{call_ref} (Calls().Get).
	OpGetCall Operation = "get_call"

	// OpCallResult is GET /calls/{call_id}/result (Calls().Result).
	OpCallResult Operation = "call_result"

	// OpOrgBalance is GET /orgs/{org_id}/balance (Orgs().Balance).
	OpOrgBalance Operation = "org_balance"

	// OpOrgUsage is GET /orgs/{org_id}/usage (Orgs().Usage).
	OpOrgUsage Operation = "org_usage"

	// OpOrgSpend is GET /orgs/{org_id}/usage/spend (Orgs().Spend).
	OpOrgSpend Operation = "org_spend"

	// OpOrgUsageByTag is GET /orgs/{org_id}/usage/by-tag (Orgs().UsageByTag).
	OpOrgUsageByTag Operation = "org_usage_by_tag"

	// OpOrgTagKeys is GET /orgs/{org_id}/tag-keys (Orgs().TagKeys).
	OpOrgTagKeys Operation = "org_tag_keys"

	// OpOrgProviderResources is GET /orgs/{org_id}/provider-resources
	// (Orgs().ProviderResources).
	OpOrgProviderResources Operation = "org_provider_resources"

	// OpListTools is GET /tools (Tools().List).
	OpListTools Operation = "list_tools"

	// OpToolByName is GET /tools/by-name/{name} (Tools().ByName).
	OpToolByName Operation = "tool_by_name"
)

// ValidationError describes a local validation failure: nothing was sent. Err
// is ErrInvalidConfig or ErrInvalidRequest. Field names the argument, option
// or request field as a Go path, such as "Controls.Tags[2].Value" or
// "Timeout", and Reason is a short fixed code, such as "control_character".
// Neither ever contains the rejected value.
type ValidationError struct {
	Err    error
	Field  string
	Reason string
}

// Error returns the sentinel's message followed by the field and the reason,
// such as "treg: invalid request: Header[Cookie]: reserved_header".
func (e *ValidationError) Error() string {
	if e == nil {
		return "treg: <nil ValidationError>"
	}
	var b strings.Builder
	if e.Err != nil {
		b.WriteString(e.Err.Error())
	} else {
		b.WriteString("treg: validation failed")
	}
	for _, part := range [...]string{e.Field, e.Reason} {
		if part != "" {
			b.WriteString(": ")
			b.WriteString(part)
		}
	}
	return b.String()
}

// Unwrap returns the sentinel, ErrInvalidConfig or ErrInvalidRequest.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// CallError describes a request that was attempted, or one that a done
// context stopped before it was sent.
//
// Its Error text is built only from Op, Kind, StatusCode, GatewayMarked,
// Reason and a Refusal code that is on the compiled allowlist (see
// Refusal.Known): never the token, a URL, a header value, a body or provider
// text, so it is safe to log. GoString and MarshalJSON add only the other
// exported fields, printed by the same rule. The sanitized cause, a retained
// error body and a capture sit behind a function value that fmt and
// reflection-based dumpers print only as an address.
//
// Tests and fakes may build a CallError from its exported fields alone; it
// then matches errors.Is like one the SDK returns.
type CallError struct {
	// Kind classifies the failure.
	Kind FailureKind

	// Op is the operation concerned.
	Op Operation

	// Reason is a short fixed code that refines Kind, such as "empty_body",
	// "content_type", "duplicate_key" or the transport class "dial". It is
	// empty when Kind says everything. It never holds a value.
	Reason string

	// StatusCode is the HTTP status received, or 0 when none was.
	StatusCode int

	// GatewayMarked reports that the response carried X-Treg-Error: 1.
	GatewayMarked bool

	// OutcomeUnknown reports that a /call/ request may have executed, and
	// may have been billed, although no complete answer arrived: a failure
	// after the request headers were written (or on an injected transport,
	// which gives no such evidence), or an HTTP 5xx that is not a proven
	// pre-relay refusal. It is never set for typed reads, for a 2xx (even
	// one that failed to read or decode: that call executed), or for a 1xx,
	// 3xx or 4xx. It drives errors.Is(err, ErrOutcomeUnknown).
	OutcomeUnknown bool

	// Refusal holds the machine-readable fields of a 4xx or 5xx JSON body,
	// when one could be parsed from its first 64 KiB.
	Refusal *Refusal

	// Meta holds the response metadata, when a response arrived.
	Meta Metadata

	// private holds the sanitized cause, a retained error body and a capture.
	private sealed[callDetail]
}

// callDetail is the part of a CallError that is never printed.
type callDetail struct {
	cause    error
	body     []byte
	captured Capture
}

// newCallError returns a CallError whose cause is reachable only through
// Unwrap.
func newCallError(op Operation, kind FailureKind, reason string, status int, cause error) *CallError {
	return &CallError{Kind: kind, Op: op, Reason: reason, StatusCode: status, private: seal(callDetail{cause: cause})}
}

// Error returns "treg: <op>: <kind>", then " (HTTP <status>[, gateway])"
// when a status is known, then ": <detail>": a known refusal code, the word
// "refused" for an unknown one, or Reason. For example:
// "treg: call: http_status (HTTP 402, gateway): insufficient_balance".
func (e *CallError) Error() string {
	if e == nil {
		return "treg: <nil CallError>"
	}
	var b strings.Builder
	b.WriteString("treg: ")
	if e.Op != "" {
		b.WriteString(string(e.Op))
		b.WriteString(": ")
	}
	if e.Kind != "" {
		b.WriteString(string(e.Kind))
	} else {
		b.WriteString("call_failed")
	}
	if e.StatusCode != 0 {
		b.WriteString(" (HTTP ")
		b.WriteString(strconv.Itoa(e.StatusCode))
		if e.GatewayMarked {
			b.WriteString(", gateway")
		}
		b.WriteString(")")
	}
	if detail := e.detailText(); detail != "" {
		b.WriteString(": ")
		b.WriteString(detail)
	}
	return b.String()
}

// Unwrap returns the sanitized cause: a context error or a fixed error of
// this package. It is never a *url.Error, a net error, a JSON error or a
// writer error, which could quote a URL, a header, a token or response
// content. It is nil for a CallError built from its exported fields.
func (e *CallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.detail().cause
}

// Is reports whether target is the sentinel for e.Kind; ErrGatewayRefusal
// for an HTTP status failure that Treg marked or a saturation refusal;
// ErrOutcomeUnknown when e.OutcomeUnknown is set; context.Canceled for a
// cancellation; or context.DeadlineExceeded for a timeout or an expired
// caller deadline.
func (e *CallError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrOutcomeUnknown:
		return e.OutcomeUnknown
	case ErrGatewayRefusal:
		return e.Kind == FailureHTTPStatus && (e.GatewayMarked || (e.Refusal != nil && e.Refusal.Saturated))
	case context.Canceled:
		return e.Kind == FailureCanceled
	case context.DeadlineExceeded:
		return e.Kind == FailureDeadlineExceeded || e.Kind == FailureTimeout
	}
	sentinel := kindSentinel(e.Kind)
	return sentinel != nil && target == sentinel
}

// Timeout reports whether the failure is a timeout: the SDK's own deadline
// or the caller's.
func (e *CallError) Timeout() bool {
	return e != nil && (e.Kind == FailureTimeout || e.Kind == FailureDeadlineExceeded)
}

// GoString prints the exported fields only, with the refusal code masked as
// Error masks it, so that %#v cannot reach the cause, a retained body or a
// capture.
func (e *CallError) GoString() string {
	if e == nil {
		return "(*treg.CallError)(nil)"
	}
	refusal := "nil"
	if e.Refusal != nil {
		refusal = "&" + e.Refusal.GoString()
	}
	return "&treg.CallError{Kind:" + strconv.Quote(string(e.Kind)) +
		", Op:" + strconv.Quote(string(e.Op)) +
		", Reason:" + strconv.Quote(printable(e.Reason, validCode, "")) +
		", StatusCode:" + strconv.Itoa(e.StatusCode) +
		", GatewayMarked:" + strconv.FormatBool(e.GatewayMarked) +
		", OutcomeUnknown:" + strconv.FormatBool(e.OutcomeUnknown) +
		", Refusal:" + refusal +
		", Meta:" + e.Meta.GoString() + "}"
}

// Format prints Error for every verb, and GoString for %#v, so that no verb
// walks the fields. A CallError held in an unexported struct field is out of
// fmt's reach for methods: there, as for any exported data, its fields
// (Refusal.Code included) print as they are.
func (e CallError) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('#') {
		_, _ = io.WriteString(f, e.GoString())
		return
	}
	_, _ = io.WriteString(f, e.Error())
}

// MarshalJSON encodes the exported fields by the same rule as GoString.
func (e *CallError) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	return json.Marshal(callErrorJSON{
		Kind:           string(e.Kind),
		Op:             string(e.Op),
		Reason:         printable(e.Reason, validCode, ""),
		StatusCode:     e.StatusCode,
		GatewayMarked:  e.GatewayMarked,
		OutcomeUnknown: e.OutcomeUnknown,
		Refusal:        e.Refusal,
		Meta:           e.Meta,
	})
}

// callErrorJSON is the JSON form of a CallError.
type callErrorJSON struct {
	Kind           string   `json:"kind"`
	Op             string   `json:"op"`
	Reason         string   `json:"reason,omitempty"`
	StatusCode     int      `json:"status_code,omitempty"`
	GatewayMarked  bool     `json:"gateway_marked"`
	OutcomeUnknown bool     `json:"outcome_unknown"`
	Refusal        *Refusal `json:"refusal,omitempty"`
	Meta           Metadata `json:"meta"`
}

// Body returns the first 64 KiB of the 4xx or 5xx body, decompressed, when
// the Client was created with WithErrorBodies, and nil otherwise. It may
// echo request data or provider text: inspect it, but think before logging
// it. The caller owns the returned slice.
func (e *CallError) Body() []byte {
	if e == nil {
		return nil
	}
	return e.detail().body
}

// Captured returns the capture of a 2xx body that failed to decode, when
// capture was requested (see CaptureResponse). It is CaptureDisabled
// otherwise.
func (e *CallError) Captured() Capture {
	if e == nil {
		return Capture{}
	}
	return e.detail().captured
}

// withDetail returns e with its hidden detail replaced by d.
func (e *CallError) withDetail(d callDetail) *CallError {
	e.private = seal(d)
	return e
}

// withResponse returns e with what a received response says attached: its
// metadata, its gateway mark, the Refusal r read from its body (nil when
// none was), and, for /call/, whether its status leaves the outcome open.
// Every CallError built after a response arrived goes through it, so
// GatewayMarked is set whenever the response carried the mark.
func (e *CallError) withResponse(meta Metadata, r *Refusal) *CallError {
	e.Meta = meta
	e.GatewayMarked = meta.GatewayMarked
	e.Refusal = r
	e.OutcomeUnknown = e.Op == OpCall && statusOutcome(e.StatusCode, r)
	return e
}

// detailText is the last part of Error: a printable refusal code, else
// Reason.
func (e *CallError) detailText() string {
	if e.Refusal != nil && e.Refusal.Code != "" {
		return e.Refusal.printableCode()
	}
	if e.Reason != "" && validCode(e.Reason) {
		return e.Reason
	}
	return ""
}

func (e *CallError) detail() callDetail {
	if e.private == nil {
		return callDetail{}
	}
	return e.private()
}

// kindSentinel returns the sentinel a kind matches, or nil for the context
// kinds and any kind this package does not define.
func kindSentinel(kind FailureKind) error {
	switch kind {
	case FailureHTTPStatus:
		return ErrHTTPStatus
	case FailureUnexpectedResponse:
		return ErrUnexpectedResponse
	case FailureMalformedResponse:
		return ErrMalformedResponse
	case FailureResponseTooLarge:
		return ErrResponseTooLarge
	case FailureTransport:
		return ErrTransport
	case FailureTimeout:
		return ErrTimeout
	case FailureCanceled, FailureDeadlineExceeded:
		return nil
	}
	return nil
}

// contextFailure returns the CallError for a caller context that is done,
// with the context's error as its cause.
func contextFailure(op Operation, err error, status int) *CallError {
	if errors.Is(err, context.DeadlineExceeded) {
		return newCallError(op, FailureDeadlineExceeded, "", status, context.DeadlineExceeded)
	}
	return newCallError(op, FailureCanceled, "", status, context.Canceled)
}

// timeoutFailure returns the CallError for an expired SDK deadline.
func timeoutFailure(op Operation, status int) *CallError {
	return newCallError(op, FailureTimeout, "", status, context.DeadlineExceeded)
}

// CallError reasons, reported in CallError.Reason. Each refines the kind
// noted above its group.
const (
	// FailureUnexpectedResponse.
	reasonInformational   = "informational"
	reasonRedirect        = "redirect"
	reasonContentType     = "content_type"
	reasonContentEncoding = "content_encoding"
	reasonEmptyBody       = "empty_body"
	reasonHeadersTooLarge = "headers_too_large"

	// FailureMalformedResponse.
	reasonInvalidJSON   = "invalid_json"
	reasonTruncatedJSON = "truncated_json"
	reasonTrailingData  = "trailing_data"
	reasonDecode        = "decode"
	reasonDuplicateKey  = "duplicate_key"
	reasonTooDeep       = "too_deep"

	// FailureResponseTooLarge: a Find line over 1 MiB.
	reasonLineTooLong = "line_too_long"

	// FailureTimeout: the header wait of a stream.
	reasonHeaderTimeout = "header_timeout"

	// FailureTransport: the transport classes listed on ErrTransport.
	reasonDNS       = "dns"
	reasonProxy     = "proxy"
	reasonDial      = "dial"
	reasonTLS       = "tls"
	reasonReset     = "reset"
	reasonClosed    = "closed"
	reasonStalled   = "stalled"
	reasonShortBody = "short_body"
	reasonLongBody  = "long_body"
	reasonBodyRead  = "body_read"
	reasonOther     = "other"
)

// Validation reasons, reported in ValidationError.Reason.
const (
	reasonRequired            = "required"
	reasonNil                 = "nil"
	reasonNotInitialized      = "not_initialized"
	reasonNotApplicable       = "not_applicable"
	reasonOutOfRange          = "out_of_range"
	reasonInvalidURL          = "invalid_url"
	reasonInvalidFormat       = "invalid_format"
	reasonInvalidMethod       = "invalid_method"
	reasonControlCharacter    = "control_character"
	reasonSurroundingSpace    = "surrounding_whitespace"
	reasonInvalidCharacter    = "invalid_character"
	reasonInvalidEscape       = "invalid_escape"
	reasonDotSegment          = "dot_segment"
	reasonEmptySegment        = "empty_segment"
	reasonTooLong             = "too_long"
	reasonTooMany             = "too_many"
	reasonTooLarge            = "too_large"
	reasonDuplicate           = "duplicate"
	reasonReservedHeader      = "reserved_header"
	reasonCredentialInRequest = "credential_in_request" //nolint:gosec // a reason code, not a credential
	reasonConflict            = "conflict"
	reasonNotWholeSeconds     = "not_whole_seconds"
	reasonJSONMarshal         = "json_marshal"
	reasonNegative            = "negative"
	reasonInvalidTarget       = "invalid_target"
	reasonStreamConsumed      = "stream_consumed"
)

// invalidConfig returns a *ValidationError matching ErrInvalidConfig.
func invalidConfig(field, reason string) error {
	return &ValidationError{Err: ErrInvalidConfig, Field: field, Reason: reason}
}

// invalidRequest returns a *ValidationError matching ErrInvalidRequest.
func invalidRequest(field, reason string) error {
	return &ValidationError{Err: ErrInvalidRequest, Field: field, Reason: reason}
}

// notInitialized returns the error for a receiver not built by this package.
func notInitialized(field string) error {
	return invalidConfig(field, reasonNotInitialized)
}

// nilContext returns the error for a nil context.
func nilContext() error {
	return invalidRequest("ctx", reasonNil)
}
