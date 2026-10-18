package treg

import (
	"cmp"
	"net/http"
	"time"
)

// Option configures NewClient or NewCatalogClient. Options only record their
// argument: the constructor validates the result in a fixed order. A nil
// Option is skipped, the last of repeated options wins, and an explicit zero
// or empty argument is invalid. An option a constructor does not accept is
// ErrInvalidConfig with reason "not_applicable".
type Option func(*options)

// options is the configuration record.
type options struct {
	org              string
	orgSet           bool
	baseURL          string
	baseURLSet       bool
	httpClient       *http.Client
	httpClientSet    bool
	timeout          time.Duration
	streamTimeout    time.Duration
	maxRequestBytes  int64
	maxRequestSet    bool
	maxResponseBytes int64
	maxStreamBytes   int64
	maxStreamSet     bool
	userAgent        string
	capture          bool
	errorBodies      bool
}

// WithOrg sends X-Treg-Org: ref on every authenticated request. ref is a
// team slug. It is needed only with an identity token (from "treg login")
// that is not pinned to a team; a per-org token or dashboard API key has its
// team baked in and Treg ignores the header. The server also accepts a
// team's previous slug or numeric id, which the SDK does not document as a
// contract. NewClient only.
func WithOrg(ref string) Option {
	return func(o *options) {
		o.org = ref
		o.orgSet = true
	}
}

// WithBaseURL sends every request to rawURL instead of DefaultBaseURL: a
// self-hosted Treg, or a local test server from httptest.NewTLSServer
// (together with WithHTTPClient(srv.Client())). It must be an https origin:
// a host name, IPv4 address or bracketed IPv6 address with an optional port,
// and no userinfo, query, fragment or path (a lone trailing "/" is
// allowed). The token goes wherever this points, so set it only from
// trusted configuration. Errors never quote the URL.
func WithBaseURL(rawURL string) Option {
	return func(o *options) {
		o.baseURL = rawURL
		o.baseURLSet = true
	}
}

// WithHTTPClient makes requests through a shallow copy of hc, which is never
// modified. The copy keeps no cookies (Jar is nil) and refuses redirects, and
// its transport is wrapped so that a redirect's Location is never parsed and
// response headers stay within 64 KiB.
//
// A nil hc.Transport means the SDK's own transport, not
// http.DefaultTransport, so proxy environment variables stay ignored. A
// non-zero hc.Timeout is not kept on the copy: it caps every SDK deadline
// instead (WithTimeout, the stream header wait and WithStreamTimeout).
//
// The transport is trusted: it sees the token and every request, and it may
// retry on its own, which the SDK cannot prevent. A nil hc is rejected.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) {
		o.httpClient = hc
		o.httpClientSet = true
	}
}

// WithTimeout bounds each buffered request (CallBytes, CallJSON and the typed
// reads), from dispatch through reading and closing the body, and the wait
// for a stream's response headers. The default is DefaultTimeout (240
// seconds); d must be greater than 0 and at most 30 minutes. The caller's
// context deadline applies as well.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithStreamTimeout bounds the life of a stream (Call and CatalogClient.Find),
// from dispatch to Close, including the time the caller spends between
// reads. The default is DefaultStreamTimeout (30 minutes); d must be greater
// than 0 and at most 12 hours.
func WithStreamTimeout(d time.Duration) Option {
	return func(o *options) { o.streamTimeout = d }
}

// WithMaxRequestBytes limits a request body: the encoded bytes of JSONBody,
// BytesBody and FormBody, and the declared size of ReaderBody. The default
// is DefaultMaxRequestBytes (32 MiB); n must be from 1 byte to 4 GiB. A
// larger body is ErrInvalidRequest, and nothing is sent. NewClient only.
func WithMaxRequestBytes(n int64) Option {
	return func(o *options) {
		o.maxRequestBytes = n
		o.maxRequestSet = true
	}
}

// WithMaxResponseBytes limits a buffered response body (CallBytes, CallJSON),
// counted after the transport decompresses it. The default is
// DefaultMaxResponseBytes (16 MiB); n must be from 1 byte to 256 MiB. Typed
// reads use the lower of n and 8 MiB. A larger body fails with
// ErrResponseTooLarge, possibly after the request executed.
func WithMaxResponseBytes(n int64) Option {
	return func(o *options) { o.maxResponseBytes = n }
}

// WithMaxStreamBytes limits the bytes Response.Read returns. The default is
// DefaultMaxStreamBytes (1 GiB); n must be from 1 byte to 64 GiB. NewClient
// only.
func WithMaxStreamBytes(n int64) Option {
	return func(o *options) {
		o.maxStreamBytes = n
		o.maxStreamSet = true
	}
}

// WithUserAgent replaces the User-Agent header, "go-treg" by default. ua must
// be a valid header value: 1 to 256 bytes of visible ASCII and spaces, with
// no leading or trailing space.
func WithUserAgent(ua string) Option {
	return func(o *options) { o.userAgent = ua }
}

// WithResponseCapture makes CallJSON and the typed reads keep a copy of the
// exact 2xx bytes they decode, in Result.Captured, unless a call overrides
// it with CaptureResponse(false). Captured bytes may hold personal or
// provider data. It is off by default.
func WithResponseCapture() Option {
	return func(o *options) { o.capture = true }
}

// WithErrorBodies keeps the first 64 KiB of every 4xx and 5xx body on the
// CallError, reachable only through CallError.Body. It is off by default,
// because errors are routinely logged and passed on, and an error body may
// echo request data or provider text.
func WithErrorBodies() Option {
	return func(o *options) { o.errorBodies = true }
}

// defaultOptions returns the options before any Option applies.
func defaultOptions() options {
	return options{
		timeout:          DefaultTimeout,
		streamTimeout:    DefaultStreamTimeout,
		maxRequestBytes:  DefaultMaxRequestBytes,
		maxResponseBytes: DefaultMaxResponseBytes,
		maxStreamBytes:   DefaultMaxStreamBytes,
		userAgent:        defaultUserAgent,
	}
}

// apply runs each non-nil option in order.
func (o *options) apply(opts []Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
}

// catalogOnly reports the first option NewCatalogClient does not accept.
func (o *options) catalogOnly() error {
	switch {
	case o.orgSet:
		return invalidConfig("Org", reasonNotApplicable)
	case o.maxRequestSet:
		return invalidConfig("MaxRequestBytes", reasonNotApplicable)
	case o.maxStreamSet:
		return invalidConfig("MaxStreamBytes", reasonNotApplicable)
	}
	return nil
}

// validate checks the options in a fixed order and returns the settings.
func (o *options) validate() (*settings, error) {
	for _, c := range o.checks() {
		if c.reason != "" {
			return nil, invalidConfig(c.field, c.reason)
		}
	}
	host := defaultHost
	if o.baseURLSet {
		h, ok := originHost(o.baseURL)
		if !ok {
			return nil, invalidConfig("BaseURL", reasonInvalidURL)
		}
		host = h
	}
	return o.settings(host), nil
}

// optionCheck is one option's field and the reason it is invalid, "" when
// it is valid.
type optionCheck struct {
	field, reason string
}

// checks returns the option checks, in the order validate reports them.
func (o *options) checks() []optionCheck {
	var hc, org string
	if o.httpClientSet && o.httpClient == nil {
		hc = reasonNil
	}
	if o.orgSet {
		org = headerValueReason(o.org, maxOrgBytes)
	}
	return []optionCheck{
		{"HTTPClient", hc},
		{"Org", org},
		{"UserAgent", headerValueReason(o.userAgent, maxUserAgentBytes)},
		{"Timeout", rangeReason(o.timeout, 1, maxTimeout)},
		{"StreamTimeout", rangeReason(o.streamTimeout, 1, maxStreamTimeout)},
		{"MaxRequestBytes", rangeReason(o.maxRequestBytes, 1, maxRequestBytesLimit)},
		{"MaxResponseBytes", rangeReason(o.maxResponseBytes, 1, maxResponseBytesLimit)},
		{"MaxStreamBytes", rangeReason(o.maxStreamBytes, 1, maxStreamBytesLimit)},
	}
}

// settings returns the settings of validated options, sent to host.
func (o *options) settings(host string) *settings {
	s := &settings{
		host:             host,
		customBase:       o.baseURLSet,
		org:              o.org,
		userAgent:        o.userAgent,
		timeout:          o.timeout,
		streamTimeout:    o.streamTimeout,
		maxRequestBytes:  o.maxRequestBytes,
		maxResponseBytes: o.maxResponseBytes,
		maxStreamBytes:   o.maxStreamBytes,
		capture:          o.capture,
		errorBodies:      o.errorBodies,
		injected:         o.httpClientSet,
	}
	s.hc = o.client()
	if o.httpClientSet && o.httpClient.Timeout > 0 {
		s.timeout = min(s.timeout, o.httpClient.Timeout)
		s.streamTimeout = min(s.streamTimeout, o.httpClient.Timeout)
	}
	return s
}

// rangeReason returns out_of_range when v is outside [lo, hi], else "".
func rangeReason[T cmp.Ordered](v, lo, hi T) string {
	if v < lo || v > hi {
		return reasonOutOfRange
	}
	return ""
}
