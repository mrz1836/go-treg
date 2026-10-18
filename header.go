package treg

import (
	"iter"
	"net/http"
	"strings"
)

// Header names the SDK sets, refuses or reads.
const (
	// tregHeaderPrefix starts every Treg control and metadata header.
	tregHeaderPrefix = "X-Treg-"

	// Request headers the SDK sets itself.
	headerToken         = "X-Treg-Token" //nolint:gosec // a header name, not a credential
	headerOrg           = "X-Treg-Org"
	headerUserAgent     = "User-Agent"
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	headerAccept        = "Accept"

	// headerIdempotencyKey is stored under this exact, non-canonical map
	// key. Header names are case-insensitive on the wire (and HTTP/2 lowers
	// them), so Treg reads it as Idempotency-Key. net/http's isReplayable
	// looks the canonical key up by exact map lookup, so this spelling keeps
	// it from making a bodyless POST, PUT or PATCH replayable over HTTP/1.
	headerIdempotencyKey = "Idempotency-key"

	// Request headers the SDK writes from Controls.
	headerMaxCost       = "X-Treg-Route-Max-Cost"
	headerMeta          = "X-Treg-Meta"
	headerMaxAge        = "X-Treg-Max-Age"
	headerCacheControl  = "Cache-Control"
	headerWaterfall     = "X-Treg-Route-Waterfall"
	headerStrictFilters = "X-Treg-Route-Strict-Filters"
	headerVerify        = "X-Treg-Route-Verify"
	headerPrefer        = "X-Treg-Route-Prefer"
	headerExclude       = "X-Treg-Route-Exclude"

	// Treg request headers Controls.Extra may not set and the SDK never
	// sends: attribution and OAuth grant selection.
	headerClient              = "X-Treg-Client"
	headerAuthorizationMethod = "X-Treg-Authorization-Method"

	// Response headers the SDK reads, canonical.
	headerCallID          = "X-Treg-Call-Id"
	headerCostMicro       = "X-Treg-Cost-Micro"
	headerOriginalCost    = "X-Treg-Original-Cost-Micro"
	headerReplay          = "X-Treg-Idempotent-Replay"
	headerError           = "X-Treg-Error"
	headerServedBy        = "X-Treg-Served-By"
	headerServedVia       = "X-Treg-Served-Via"
	headerUpstreamFailed  = "X-Treg-Upstream-Failed"
	headerCache           = "X-Treg-Cache"
	headerAge             = "X-Treg-Age"
	headerFetchedAt       = "X-Treg-Fetched-At"
	headerAsync           = "X-Treg-Async"
	headerRetryAfter      = "Retry-After"
	headerContentEncoding = "Content-Encoding"
	headerLocation        = "Location"
)

// foldedValues yields every value of the header name in h, matching names
// ignoring case, so that the non-canonical keys a caller or an injected
// transport may set count too.
func foldedValues(h http.Header, name string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for k, vs := range h {
			if !strings.EqualFold(k, name) {
				continue
			}
			for _, v := range vs {
				if !yield(v) {
					return
				}
			}
		}
	}
}

// validHeaderName reports whether k is a non-empty RFC 7230 token of at most
// 256 bytes.
func validHeaderName(k string) bool { return boundedOf(k, maxHeaderNameBytes, isTokenChar) }

// checkHeaderValue validates a string that becomes a header value: bytes
// 0x20 to 0x7E and horizontal tab, no leading or trailing whitespace, at
// most maxBytes. It returns "" when v is valid, else a reason: too_long,
// then, for the first refused byte, control_character for a control byte
// or invalid_character for any other (a byte of 0x80 or above), then
// surrounding_whitespace. The rule is stricter than net/http's, so a value
// is never refused after dispatch.
func checkHeaderValue(v string, maxBytes int) string {
	if len(v) > maxBytes {
		return reasonTooLong
	}
	if reason := checkChars(v, isHeaderValueChar); reason != "" {
		return reason
	}
	if v != "" && (isSpace(v[0]) || isSpace(v[len(v)-1])) {
		return reasonSurroundingSpace
	}
	return ""
}

// headerValueReason validates a header value that may not be empty, such
// as WithOrg's or a body's Content-Type: required when v is empty, else
// checkHeaderValue's reason.
func headerValueReason(v string, maxBytes int) string {
	if v == "" {
		return reasonRequired
	}
	return checkHeaderValue(v, maxBytes)
}

// reservedHeader reports whether name, compared ignoring case, is one the
// caller may not set in CallRequest.Header.
func reservedHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "transfer-encoding", "connection", "keep-alive", "te", "trailer",
		"trailers", "upgrade", "expect", "forwarded", "via", "x-real-ip", "true-client-ip", "cdn-loop",
		"cookie", "cookie2", "authorization", "idempotency-key", "x-idempotency-key", "content-type",
		"content-encoding", "accept-encoding", "user-agent":
		return true
	}
	for _, prefix := range [...]string{"Proxy-", "X-Forwarded-", "Cf-", tregHeaderPrefix} {
		if hasPrefixFold(name, prefix) {
			return true
		}
	}
	return false
}
