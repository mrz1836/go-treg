package treg

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Metadata bounds.
const (
	maxTregHeaderBytes  = 1 << 10
	maxAsyncHeaderBytes = 16 << 10

	// maxSecondsDigits bounds a header of whole seconds.
	maxSecondsDigits = 10

	// A call ref is callRefHexDigits lowercase hex digits, then up to
	// maxCallRefSuffixes suffixes, each ":v" or ":r<n>"/":p<n>" with a body of
	// at most maxCallRefSuffixBytes ("r" or "p" and up to five digits).
	callRefHexDigits      = 32
	maxCallRefSuffixes    = 4
	maxCallRefSuffixBytes = 6
)

// Metadata is what Treg's response headers say about one request. It is
// immutable and request-local, and holds no caller data: no endpoint id,
// URL, query, tag value, body or provider text. Each field is evidence as
// received: on a call that ran on the team's own key, an upstream can send
// X-Treg-* headers too (Treg overwrites only the call id, the async
// descriptor and, on metered calls, the cost), so reconcile money through
// Calls().Get. A header that is repeated, oversized or malformed leaves its
// field unset and is named in Unparsed.
//
// Metadata prints only its fields, by the rules of String.
type Metadata struct {
	// Op is the operation.
	Op Operation

	// StatusCode is the HTTP status.
	StatusCode int

	// CallRef is X-Treg-Call-Id: 32 lowercase hex digits, plus a routed
	// child's ":r<n>", ":p<n>" or ":v" suffixes. On a replay it is the
	// original call's ref.
	CallRef string

	// CostMicro is X-Treg-Cost-Micro as reported. nil means unknown: an
	// own-key call or a refusal before any reserve, never zero. When Async
	// is set it is the reservation, not the charge; the terminal task
	// settles the real charge.
	CostMicro *MicroUSD

	// OriginalCostMicro is X-Treg-Original-Cost-Micro: on a replay, what
	// the original call was charged (or, for a generation task, reserved).
	OriginalCostMicro *MicroUSD

	// Replayed reports X-Treg-Idempotent-Replay: true.
	Replayed bool

	// GatewayMarked reports X-Treg-Error: 1, which Treg stamps on its own
	// refusals. It is evidence, not proof.
	GatewayMarked bool

	// ServedBy is X-Treg-Served-By: the catalog id of the child endpoint
	// that served a routed call.
	ServedBy EndpointID

	// ServedVia is X-Treg-Served-Via, "overflow:<name>": the endpoint was
	// served through a Treg relay account.
	ServedVia string

	// UpstreamFailed reports that X-Treg-Upstream-Failed was present: a 2xx
	// in which the provider reported a failure. Its value is in Header.
	UpstreamFailed bool

	// Cache describes an archive-served answer.
	Cache CacheInfo

	// Async is the parsed X-Treg-Async descriptor of an asynchronous
	// submission, or nil.
	Async *AsyncDescriptor

	// RetryAfter is the Retry-After delay, when HasRetryAfter is set.
	RetryAfter time.Duration

	// HasRetryAfter reports a valid Retry-After in whole seconds.
	HasRetryAfter bool

	// Unparsed lists the canonical names of the headers Metadata reads that
	// were present but repeated, oversized or malformed.
	Unparsed []string
}

// CacheInfo describes an answer Treg served from its archive.
type CacheInfo struct {
	// Hit reports X-Treg-Cache: hit.
	Hit bool

	// Age is X-Treg-Age in whole seconds.
	Age *time.Duration

	// FetchedAt is X-Treg-Fetched-At: when the archived answer was fetched.
	FetchedAt *time.Time
}

// parseMetadata reads the Treg headers of a response.
func parseMetadata(op Operation, status int, h http.Header) Metadata {
	m := Metadata{Op: op, StatusCode: status}
	p := headerParser{h: h}
	m.CallRef = p.shaped(headerCallID, validCallRef)
	m.CostMicro = p.micro(headerCostMicro)
	m.OriginalCostMicro = p.micro(headerOriginalCost)
	m.Replayed = p.flag(headerReplay, "true")
	m.GatewayMarked = p.flag(headerError, "1")
	m.ServedBy = EndpointID(p.shaped(headerServedBy, func(v string) bool { return checkCatalogID(v) == "" }))
	m.ServedVia = p.shaped(headerServedVia, func(v string) bool {
		name, found := strings.CutPrefix(v, "overflow:")
		return found && validProvider(name)
	})
	_, m.UpstreamFailed = p.value(headerUpstreamFailed, maxTregHeaderBytes)
	m.Cache.Hit = p.flag(headerCache, "hit")
	m.Cache.Age = p.seconds(headerAge)
	m.Cache.FetchedAt = p.time(headerFetchedAt)
	if v, ok := p.value(headerAsync, maxAsyncHeaderBytes); ok {
		if d, err := parseAsyncDescriptor([]byte(v)); err == nil {
			m.Async = d
		} else {
			p.bad(headerAsync)
		}
	}
	if d := p.seconds(headerRetryAfter); d != nil {
		m.RetryAfter, m.HasRetryAfter = *d, true
	}
	m.Unparsed = p.unparsed
	return m
}

// headerParser reads single header values, recording the names of bad ones.
type headerParser struct {
	h        http.Header
	unparsed []string
}

// value returns the one value of the header name, matched ignoring case so
// that non-canonical keys an injected transport sets count too. A header
// that is repeated or longer than maxBytes is recorded as bad.
func (p *headerParser) value(name string, maxBytes int) (string, bool) {
	var value string
	n := 0
	for v := range foldedValues(p.h, name) {
		value = v
		n++
	}
	switch {
	case n == 0:
		return "", false
	case n > 1 || len(value) > maxBytes:
		p.bad(name)
		return "", false
	}
	return value, true
}

// bad records name as unparsed.
func (p *headerParser) bad(name string) {
	if !slices.Contains(p.unparsed, name) {
		p.unparsed = append(p.unparsed, name)
	}
}

// shaped returns the header's value when valid accepts it, recording any
// other value as bad.
func (p *headerParser) shaped(name string, valid func(string) bool) string {
	v, ok := p.value(name, maxTregHeaderBytes)
	if !ok {
		return ""
	}
	if !valid(v) {
		p.bad(name)
		return ""
	}
	return v
}

// seconds parses a header of whole non-negative seconds.
func (p *headerParser) seconds(name string) *time.Duration {
	v, ok := p.value(name, maxTregHeaderBytes)
	if !ok {
		return nil
	}
	d, valid := parseSeconds(v)
	if !valid {
		p.bad(name)
		return nil
	}
	return &d
}

// time parses an RFC 3339 timestamp header, returned in UTC.
func (p *headerParser) time(name string) *time.Time {
	v, ok := p.value(name, maxTregHeaderBytes)
	if !ok {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		p.bad(name)
		return nil
	}
	return new(t.UTC())
}

// micro parses an integer micro-USD header: 1 to 19 digits, no sign.
func (p *headerParser) micro(name string) *MicroUSD {
	v, ok := p.value(name, maxTregHeaderBytes)
	if !ok {
		return nil
	}
	if len(v) > maxInt64Digits || !asciiDigits(v) {
		p.bad(name)
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.bad(name)
		return nil
	}
	return new(MicroUSD(n))
}

// flag reports whether the header name has exactly the value want; any
// other value is recorded as bad.
func (p *headerParser) flag(name, want string) bool {
	v, ok := p.value(name, maxTregHeaderBytes)
	if !ok {
		return false
	}
	if v != want {
		p.bad(name)
		return false
	}
	return true
}

// parseSeconds parses whole non-negative seconds, at most 10 digits.
func parseSeconds(v string) (time.Duration, bool) {
	if len(v) > maxSecondsDigits || !asciiDigits(v) {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

// validCallRef reports whether s is 32 lowercase hex digits, followed by up
// to four ":r<n>", ":p<n>" or ":v" suffixes.
func validCallRef(s string) bool {
	if len(s) < callRefHexDigits || !allBytes(s[:callRefHexDigits], isLowerHex) {
		return false
	}
	suffixes, found := strings.CutPrefix(s[callRefHexDigits:], ":")
	if !found {
		return len(s) == callRefHexDigits
	}
	n := 0
	for seg := range strings.SplitSeq(suffixes, ":") {
		if n++; n > maxCallRefSuffixes || !validCallRefSuffix(seg) {
			return false
		}
	}
	return true
}

// validCallRefSuffix reports whether seg, a call ref suffix without its
// ":", is "v", or "r" or "p" followed by 1 to 5 digits.
func validCallRefSuffix(seg string) bool {
	return seg == "v" ||
		(seg != "" && (seg[0] == 'r' || seg[0] == 'p') && boundedOf(seg[1:], maxCallRefSuffixBytes-1, isDigit))
}

// String prints only the op, status, call ref, the micro amounts, the
// booleans, the cache hit and age, whether an async descriptor is present,
// the retry delay and the unparsed header names.
func (m Metadata) String() string {
	fields := []string{"op: " + printable(string(m.Op), validCode, ""), "status: " + strconv.Itoa(m.StatusCode)}
	if validCallRef(m.CallRef) {
		fields = append(fields, "callRef: "+m.CallRef)
	}
	if m.CostMicro != nil {
		fields = append(fields, "costMicro: "+strconv.FormatInt(int64(*m.CostMicro), 10))
	}
	if m.OriginalCostMicro != nil {
		fields = append(fields, "originalCostMicro: "+strconv.FormatInt(int64(*m.OriginalCostMicro), 10))
	}
	for _, f := range [...]struct {
		name string
		on   bool
	}{
		{"replayed", m.Replayed},
		{"gatewayMarked", m.GatewayMarked},
		{"upstreamFailed", m.UpstreamFailed},
		{"servedBy", m.ServedBy != ""},
		{"servedVia", m.ServedVia != ""},
		{"cacheHit", m.Cache.Hit},
		{"async", m.Async != nil},
	} {
		if f.on {
			fields = append(fields, f.name+": true")
		}
	}
	if m.Cache.Age != nil {
		fields = append(fields, "cacheAge: "+m.Cache.Age.String())
	}
	if m.HasRetryAfter {
		fields = append(fields, "retryAfter: "+m.RetryAfter.String())
	}
	if names := printableHeaderNames(m.Unparsed); len(names) > 0 {
		fields = append(fields, "unparsed: "+strings.Join(names, " "))
	}
	return "treg.Metadata{" + strings.Join(fields, ", ") + "}"
}

// GoString returns the same text as String.
func (m Metadata) GoString() string { return m.String() }

// Format prints String for every verb.
func (m Metadata) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, m.String()) }

// MarshalJSON encodes the fields String prints.
func (m Metadata) MarshalJSON() ([]byte, error) {
	out := metadataJSON{
		Op:                printable(string(m.Op), validCode, ""),
		StatusCode:        m.StatusCode,
		CostMicro:         m.CostMicro,
		OriginalCostMicro: m.OriginalCostMicro,
		Replayed:          m.Replayed,
		GatewayMarked:     m.GatewayMarked,
		UpstreamFailed:    m.UpstreamFailed,
		CacheHit:          m.Cache.Hit,
		Async:             m.Async != nil,
		Unparsed:          printableHeaderNames(m.Unparsed),
	}
	if validCallRef(m.CallRef) {
		out.CallRef = m.CallRef
	}
	if m.Cache.Age != nil {
		out.CacheAgeSeconds = new(int64(*m.Cache.Age / time.Second))
	}
	if m.HasRetryAfter {
		out.RetryAfterSeconds = new(int64(m.RetryAfter / time.Second))
	}
	return json.Marshal(out)
}

// metadataJSON is the JSON form of Metadata.
type metadataJSON struct {
	Op                string    `json:"op,omitempty"`
	StatusCode        int       `json:"status_code,omitempty"`
	CallRef           string    `json:"call_ref,omitempty"`
	CostMicro         *MicroUSD `json:"cost_micro,omitempty"`
	OriginalCostMicro *MicroUSD `json:"original_cost_micro,omitempty"`
	Replayed          bool      `json:"replayed,omitempty"`
	GatewayMarked     bool      `json:"gateway_marked,omitempty"`
	UpstreamFailed    bool      `json:"upstream_failed,omitempty"`
	CacheHit          bool      `json:"cache_hit,omitempty"`
	CacheAgeSeconds   *int64    `json:"cache_age_seconds,omitempty"`
	Async             bool      `json:"async,omitempty"`
	RetryAfterSeconds *int64    `json:"retry_after_seconds,omitempty"`
	Unparsed          []string  `json:"unparsed,omitempty"`
}

// printableHeaderNames returns the names that are valid header tokens.
func printableHeaderNames(names []string) []string {
	var out []string
	for _, n := range names {
		if shortHeaderName(n) {
			out = append(out, n)
		}
	}
	return out
}
