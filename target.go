package treg

import (
	"fmt"
	"io"
	"strings"
)

// TargetKind names the form of a Target.
type TargetKind uint8

// Target kinds. The zero TargetKind is invalid.
const (
	// TargetCatalogEndpoint is a catalog endpoint id, direct or routed.
	TargetCatalogEndpoint TargetKind = iota + 1

	// TargetRegisteredTool is an org tool name plus an upstream path.
	TargetRegisteredTool

	// TargetUpstreamURL is the full upstream URL of a registered tool.
	TargetUpstreamURL
)

// String returns "catalog_endpoint", "registered_tool", "upstream_url" or
// "invalid".
func (k TargetKind) String() string {
	switch k {
	case TargetCatalogEndpoint:
		return "catalog_endpoint"
	case TargetRegisteredTool:
		return "registered_tool"
	case TargetUpstreamURL:
		return "upstream_url"
	}
	return "invalid"
}

// Target bounds.
const (
	maxCatalogIDBytes = 256
	maxTargetBytes    = 8 << 10

	// maxNameBytes bounds a tool name, platform slug or provider service
	// name.
	maxNameBytes = 128
)

// Target is the {rest} of a /call/{rest} request: what Treg resolves and
// relays to. Build one with CatalogEndpoint, RegisteredTool or UpstreamURL;
// they only record their arguments, and Validate (or the call) checks them.
// The zero Target is invalid.
//
// Treg tries a URL passthrough or an org tool first, a catalog id only when
// that misses, and a hub tool "<team>.<name>" after that. So an org tool
// with the same name as a catalog id shadows it; the SDK cannot prevent
// that.
//
// A Target prints its kind only.
type Target struct {
	kind TargetKind

	// parts holds the id, name, path or URL behind a function value, which
	// fmt and reflection-based dumpers print only as an address, even where
	// a Target sits in an unexported field and its Format is out of reach.
	parts func() (string, string)
}

// newTarget returns a Target of kind holding a and b.
func newTarget(kind TargetKind, a, b string) Target {
	return Target{kind: kind, parts: func() (string, string) { return a, b }}
}

// CatalogEndpoint targets a catalog endpoint id, such as
// "moz.web.url.metrics", or a routed id, such as "treg.web.search". The id is
// never looked up in a catalog snapshot: any well-formed id works the day
// Treg adds it. It must be 1 to 256 bytes of letters, digits and "._~-",
// contain a ".", not start with "http" in any case, and not begin or end
// with "." or contain "..".
func CatalogEndpoint(id EndpointID) Target {
	return newTarget(TargetCatalogEndpoint, string(id), "")
}

// RegisteredTool targets an org tool by name, plus the path below the tool's
// base URL, such as RegisteredTool("stripe", "v1/charges"). escapedPath is
// already percent-encoded and is sent byte for byte; a leading "/" is
// optional and an empty path means the base URL itself. The name must be 1
// to 128 bytes of letters, digits and "._~-" (so never a URL scheme), and
// not "." or "..".
//
// The path may use only RFC 3986 path characters and "/", with every "%"
// starting a valid %HH escape. It is refused when, raw or percent-decoded,
// it holds "?", "#", a space, a control character or "\", a "." or ".."
// segment, or "//". Query values belong in CallRequest.Query.
func RegisteredTool(name, escapedPath string) Target {
	return newTarget(TargetRegisteredTool, name, escapedPath)
}

// UpstreamURL targets an already-registered tool by its full upstream URL,
// such as "https://api.stripe.com/v1/charges"; Treg matches the tool by host
// and path prefix. The scheme must be exactly "https://" or "http://"
// (Treg's match is case-sensitive); the host an ASCII host name, IPv4
// address or bracketed IPv6 address with an optional port. Userinfo, a query
// and a fragment are refused: query values belong in CallRequest.Query. The
// path follows RegisteredTool's rules.
func UpstreamURL(rawURL string) Target {
	return newTarget(TargetUpstreamURL, rawURL, "")
}

// Kind returns the target's form, or 0 for the zero Target.
func (t Target) Kind() TargetKind {
	if t.parts == nil {
		return 0
	}
	switch t.kind {
	case TargetCatalogEndpoint, TargetRegisteredTool, TargetUpstreamURL:
		return t.kind
	}
	return 0
}

// Validate runs exactly the checks a call runs on the target. Errors are
// *ValidationError values matching ErrInvalidRequest; they never quote the
// target.
func (t Target) Validate() error {
	_, err := t.rest("Target")
	return err
}

// String returns "treg.Target(<kind>)".
func (t Target) String() string { return "treg.Target(" + t.Kind().String() + ")" }

// GoString returns the same text as String.
func (t Target) GoString() string { return t.String() }

// Format prints String for every verb, so that the id, name, path or URL
// never prints.
func (t Target) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, t.String()) }

// MarshalJSON encodes the kind only.
func (t Target) MarshalJSON() ([]byte, error) {
	return []byte(`{"kind":"` + t.Kind().String() + `"}`), nil
}

// rest validates t and returns the raw (escaped) {rest} of the call path.
// field names the target in errors.
func (t Target) rest(field string) (string, error) {
	kind := t.Kind()
	if kind == 0 {
		return "", invalidRequest(field, reasonInvalidTarget)
	}
	a, b := t.parts()
	switch kind {
	case TargetCatalogEndpoint:
		if reason := checkCatalogID(a); reason != "" {
			return "", invalidRequest(field+".ID", reason)
		}
		return a, nil
	case TargetRegisteredTool:
		if reason := checkName(a); reason != "" {
			return "", invalidRequest(field+".Name", reason)
		}
		p := strings.TrimPrefix(b, "/")
		if p == "" {
			return a, nil
		}
		if reason := checkUpstreamPath(p); reason != "" {
			return "", invalidRequest(field+".Path", reason)
		}
		return a + "/" + p, nil
	case TargetUpstreamURL:
		rest, reason := checkUpstreamURL(a)
		if reason != "" {
			return "", invalidRequest(field+".URL", reason)
		}
		return rest, nil
	}
	return "", invalidRequest(field, reasonInvalidTarget)
}

// checkCatalogID returns "" for a well-formed catalog id, else a reason.
func checkCatalogID(id string) string {
	if reason := checkBounded(id, maxCatalogIDBytes, isUnreserved); reason != "" {
		return reason
	}
	if !strings.Contains(id, ".") || strings.HasPrefix(id, ".") || strings.HasSuffix(id, ".") ||
		strings.Contains(id, "..") || hasPrefixFold(id, "http") {
		return reasonInvalidFormat
	}
	return ""
}

// checkName returns "" for a well-formed tool name, platform slug or
// provider service name, each one path segment: 1 to 128 bytes of letters,
// digits and "._~-", and not "." or "..". Else it returns a reason.
func checkName(name string) string {
	return checkPathSegment(name, maxNameBytes, isUnreserved)
}

// checkPathSegment returns "" for a caller string that becomes one path
// segment: 1 to maxBytes bytes that chars all accepts, and not "." or "..",
// which servers would read as a dot segment. Else it returns a reason, in
// the order required, too_long, control_character or invalid_character,
// dot_segment.
func checkPathSegment(s string, maxBytes int, chars func(byte) bool) string {
	if reason := checkBounded(s, maxBytes, chars); reason != "" {
		return reason
	}
	if s == "." || s == ".." {
		return reasonDotSegment
	}
	return ""
}

// checkUpstreamPath returns "" for an acceptable escaped upstream path
// (without a leading "/"), else a reason. It checks the raw form and the
// percent-decoded form.
func checkUpstreamPath(p string) string {
	if len(p) > maxTargetBytes {
		return reasonTooLong
	}
	if reason := checkEscaped(p, isPathChar); reason != "" {
		return reason
	}
	decoded := percentDecode(p)
	if reason := checkChars(decoded, isDecodedPathChar); reason != "" {
		return reason
	}
	for _, form := range [...]string{p, decoded} {
		if reason := checkSegments(form); reason != "" {
			return reason
		}
	}
	return ""
}

// checkSegments refuses "." and ".." segments and empty segments ("//") in
// a path without its leading "/". A trailing "/" is allowed.
func checkSegments(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		switch {
		case seg == "." || seg == "..":
			return reasonDotSegment
		case seg == "" && i != len(segs)-1:
			return reasonEmptySegment
		}
	}
	return ""
}

// checkUpstreamURL validates an upstream URL and returns the {rest} it is
// sent as, which is raw itself, else a reason.
func checkUpstreamURL(raw string) (string, string) {
	if raw == "" {
		return "", reasonRequired
	}
	if len(raw) > maxTargetBytes {
		return "", reasonTooLong
	}
	scheme := ""
	for _, s := range [...]string{"https://", "http://"} {
		if strings.HasPrefix(raw, s) {
			scheme = s
		}
	}
	if scheme == "" {
		return "", reasonInvalidURL
	}
	hostport, path, _ := strings.Cut(raw[len(scheme):], "/")
	if strings.ContainsAny(hostport, "@?#") || !validHostPort(hostport) {
		return "", reasonInvalidURL
	}
	if path == "" {
		return raw, ""
	}
	if strings.ContainsAny(path, "?#") {
		return "", reasonInvalidURL
	}
	if reason := checkUpstreamPath(path); reason != "" {
		return "", reason
	}
	return raw, ""
}
