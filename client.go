package treg

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Defaults and limits.
const (
	// DefaultBaseURL is the hosted Treg gateway.
	DefaultBaseURL = "https://treg.to"

	// DefaultTimeout bounds each buffered request, including reading the
	// whole body, and a stream's wait for response headers. WithTimeout
	// accepts more than 0 and at most 30 minutes.
	DefaultTimeout = 240 * time.Second

	// DefaultStreamTimeout bounds the life of a stream, from dispatch to
	// Close. WithStreamTimeout accepts more than 0 and at most 12 hours.
	DefaultStreamTimeout = 30 * time.Minute

	// DefaultMaxRequestBytes limits an encoded or declared request body.
	// WithMaxRequestBytes accepts 1 byte to 4 GiB.
	DefaultMaxRequestBytes = 32 << 20

	// DefaultMaxResponseBytes limits a buffered response body after
	// decompression. WithMaxResponseBytes accepts 1 byte to 256 MiB.
	DefaultMaxResponseBytes = 16 << 20

	// DefaultMaxStreamBytes limits the body Response.Read returns.
	// WithMaxStreamBytes accepts 1 byte to 64 GiB.
	DefaultMaxStreamBytes = 1 << 30
)

// Internal bounds.
const (
	defaultHost = "treg.to"

	maxTimeout            = 30 * time.Minute
	maxStreamTimeout      = 12 * time.Hour
	maxRequestBytesLimit  = 4 << 30
	maxResponseBytesLimit = 256 << 20
	maxStreamBytesLimit   = 64 << 30
	typedResponseCap      = 8 << 20
	maxTokenBytes         = 4096
	maxOrgBytes           = 256
	maxUserAgentBytes     = 256
	maxCodeBytes          = 64

	// minScanTokenBytes is the shortest token the credential scan looks
	// for: real tokens are far longer, and a short one would match ordinary
	// text.
	minScanTokenBytes = 16
)

// settings is a validated configuration, shared by a Client and the
// CatalogClient it returns. It holds no secret.
type settings struct {
	hc               *http.Client
	host             string // host[:port] of the https origin
	org              string
	userAgent        string
	timeout          time.Duration
	streamTimeout    time.Duration
	maxRequestBytes  int64
	maxResponseBytes int64
	maxStreamBytes   int64
	customBase       bool
	capture          bool
	errorBodies      bool
	injected         bool
}

// Client calls Treg with a token: /call/ execution and the authenticated
// reads. Create it with NewClient; a nil *Client and the zero Client return
// ErrInvalidConfig without any I/O. A Client is immutable and safe for
// concurrent use.
//
// Its fields are unexported and the token is held only inside function
// values, so formatting a Client, or a struct holding one, with any fmt verb
// or a reflection-based dumper never prints it.
type Client struct {
	// settings returns the validated configuration. It sits behind a
	// function value so that no fmt verb or holding can reach the org or the
	// base URL.
	settings sealed[*settings]

	// auth sets the authentication headers on a request.
	auth func(h http.Header)

	// holdsToken reports whether s contains the token, raw or in an encoded
	// form (see minScanTokenBytes); holdsTokenBytes is the same scan of an
	// in-memory request body.
	holdsToken      func(s string) bool
	holdsTokenBytes func(b []byte) bool
}

// CatalogClient reads Treg's public routes: the catalog and /meta. It never
// sends a token, even when it comes from Client.Catalog. Create it with
// NewCatalogClient or Client.Catalog; a nil *CatalogClient and the zero
// CatalogClient return ErrInvalidConfig without any I/O. It is immutable and
// safe for concurrent use.
type CatalogClient struct {
	// settings returns the validated configuration; see Client.settings.
	settings sealed[*settings]

	// holdsToken is the credential scan of the Client this came from, or nil.
	holdsToken func(s string) bool
}

// NewClient returns a Client that authenticates with a Treg token.
//
// Parameters:
//   - token: a per-org token, a dashboard API key or an identity token, sent
//     only as X-Treg-Token, only to the configured origin, and never on the
//     public catalog routes. With an identity token not pinned to a team,
//     add WithOrg
//   - opts: optional settings; see the With functions
//
// The token must be 1 to 4096 bytes of visible ASCII (0x21 to 0x7E). The
// application decides where it comes from: NewClient reads no environment
// variables or files and performs no I/O.
//
// Errors are *ValidationError values matching ErrInvalidConfig. They name the
// argument or option, never its value.
func NewClient(token string, opts ...Option) (*Client, error) {
	if reason := checkBounded(token, maxTokenBytes, isVisible); reason != "" {
		return nil, invalidConfig("token", reason)
	}
	o := defaultOptions()
	o.apply(opts)
	cfg, err := o.validate()
	if err != nil {
		return nil, err
	}
	org := cfg.org
	holdsToken, holdsTokenBytes := tokenScanner(token)
	return &Client{
		settings: seal(cfg),
		auth: func(h http.Header) {
			h[headerToken] = []string{token}
			if org != "" {
				h[headerOrg] = []string{org}
			}
		},
		holdsToken:      holdsToken,
		holdsTokenBytes: holdsTokenBytes,
	}, nil
}

// NewCatalogClient returns an unauthenticated client for the public routes.
// It accepts every option except WithOrg, WithMaxRequestBytes and
// WithMaxStreamBytes; WithStreamTimeout bounds Find. It performs no I/O.
func NewCatalogClient(opts ...Option) (*CatalogClient, error) {
	o := defaultOptions()
	o.apply(opts)
	if err := o.catalogOnly(); err != nil {
		return nil, err
	}
	cfg, err := o.validate()
	if err != nil {
		return nil, err
	}
	return &CatalogClient{settings: seal(cfg)}, nil
}

// String describes the Client's settings, such as
// "treg.Client{baseURL: default, org: unset, timeout: 4m0s, ...}". It never
// includes the token, the org or the base URL.
func (c *Client) String() string {
	if c == nil {
		return "treg.Client(nil)"
	}
	if c.settings == nil || c.settings() == nil {
		return "treg.Client(uninitialized)"
	}
	return c.settings().describe("treg.Client", true)
}

// GoString returns the same text as String, so that %#v prints settings only.
func (c *Client) GoString() string { return c.String() }

// String describes the CatalogClient's settings. It never includes the base
// URL.
func (c *CatalogClient) String() string {
	if c == nil {
		return "treg.CatalogClient(nil)"
	}
	if c.settings == nil || c.settings() == nil {
		return "treg.CatalogClient(uninitialized)"
	}
	return c.settings().describe("treg.CatalogClient", false)
}

// GoString returns the same text as String.
func (c *CatalogClient) GoString() string { return c.String() }

// CloseIdleConnections closes idle connections of the SDK's transport, or of
// the injected client's transport when it has the method. It does nothing on
// an uninitialized Client.
func (c *Client) CloseIdleConnections() {
	if c.ready() == nil {
		c.cfg().hc.CloseIdleConnections()
	}
}

// Catalog returns a CatalogClient with c's settings, for the public routes.
// Its requests never carry the token or the org. It returns nil for an
// uninitialized Client; the nil CatalogClient's methods report
// ErrInvalidConfig.
func (c *Client) Catalog() *CatalogClient {
	if c.ready() != nil {
		return nil
	}
	return &CatalogClient{settings: c.settings, holdsToken: c.holdsToken}
}

// ready reports ErrInvalidConfig for a nil Client or one not built by
// NewClient.
func (c *Client) ready() error {
	if c == nil || c.settings == nil || c.settings() == nil || c.auth == nil || c.holdsToken == nil ||
		c.holdsTokenBytes == nil {
		return notInitialized("client")
	}
	return nil
}

// cfg returns the settings of an initialized Client.
func (c *Client) cfg() *settings { return c.settings() }

// ready reports ErrInvalidConfig for a nil CatalogClient or one not built by
// this package.
func (c *CatalogClient) ready() error {
	if c == nil || c.settings == nil || c.settings() == nil {
		return notInitialized("client")
	}
	return nil
}

// cfg returns the settings of an initialized CatalogClient.
func (c *CatalogClient) cfg() *settings { return c.settings() }

// describe prints the settings as "name{key: value, ...}".
func (s *settings) describe(name string, authenticated bool) string {
	base := "default"
	if s.customBase {
		base = "custom"
	}
	hc := "sdk"
	if s.injected {
		hc = "injected"
	}
	fields := []string{
		"baseURL: " + base,
		"httpClient: " + hc,
		"timeout: " + s.timeout.String(),
		"streamTimeout: " + s.streamTimeout.String(),
		"maxResponseBytes: " + strconv.FormatInt(s.maxResponseBytes, 10),
		"capture: " + strconv.FormatBool(s.capture),
		"errorBodies: " + strconv.FormatBool(s.errorBodies),
	}
	if authenticated {
		org := "unset"
		if s.org != "" {
			org = "set"
		}
		fields = append(fields,
			"org: "+org,
			"maxRequestBytes: "+strconv.FormatInt(s.maxRequestBytes, 10),
			"maxStreamBytes: "+strconv.FormatInt(s.maxStreamBytes, 10),
		)
	}
	return name + "{" + strings.Join(fields, ", ") + "}"
}

// responseLimit returns the body limit of a buffered request: the configured
// limit for CallBytes and CallJSON, and the lower of it and 8 MiB for typed
// reads.
func (s *settings) responseLimit(op Operation) int64 {
	if op == OpCall {
		return s.maxResponseBytes
	}
	return min(s.maxResponseBytes, typedResponseCap)
}

// tokenScanner returns the credential scan for token, of a string and of a
// body: each reports whether its input holds the token raw, JSON-escaped
// (with or without HTML escaping) or query-escaped. Tokens shorter than
// minScanTokenBytes are not scanned. Only the two closures hold the token.
func tokenScanner(token string) (func(string) bool, func([]byte) bool) {
	if len(token) < minScanTokenBytes {
		return func(string) bool { return false }, func([]byte) bool { return false }
	}
	forms := []string{token}
	for _, f := range []string{jsonEscape(token, true), jsonEscape(token, false), url.QueryEscape(token), url.PathEscape(token)} {
		if !slices.Contains(forms, f) {
			forms = append(forms, f)
		}
	}
	byteForms := make([][]byte, len(forms))
	for i, f := range forms {
		byteForms[i] = []byte(f)
	}
	// Every form is at least as long as the raw token.
	holds := func(s string) bool {
		return len(s) >= len(token) && slices.ContainsFunc(forms, func(f string) bool { return strings.Contains(s, f) })
	}
	holdsBytes := func(b []byte) bool {
		return len(b) >= len(token) && slices.ContainsFunc(byteForms, func(f []byte) bool { return bytes.Contains(b, f) })
	}
	return holds, holdsBytes
}

// jsonEscape returns s as encoding/json writes it inside a string literal,
// without the quotes; html selects json.Marshal's HTML escaping.
func jsonEscape(s string, html bool) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(html)
	_ = enc.Encode(s) //nolint:errchkjson // a string always encodes, and a bytes.Buffer never fails a write
	out := strings.TrimSuffix(b.String(), "\n")
	return strings.TrimSuffix(strings.TrimPrefix(out, `"`), `"`)
}
