package treg

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Host bounds.
const (
	// maxBaseURLBytes bounds the WithBaseURL origin.
	maxBaseURLBytes = 2048

	// maxHostNameBytes and maxHostLabelBytes are the DNS limits on a name
	// (without its trailing dot) and on each of its labels.
	maxHostNameBytes  = 253
	maxHostLabelBytes = 63
)

// originHost validates an https origin and returns its "host[:port]".
// Refusing "?", "#" and "@" up front leaves url.Parse no query, fragment or
// userinfo to find, and isVisible refuses spaces and tabs.
func originHost(raw string) (string, bool) {
	if raw == "" || len(raw) > maxBaseURLBytes || strings.ContainsAny(raw, "?#@\\") || !allBytes(raw, isVisible) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.RawPath != "" ||
		(u.Path != "" && u.Path != "/") || !strings.HasPrefix(raw, "https://") {
		return "", false
	}
	if !validHostPort(u.Host) {
		return "", false
	}
	return u.Host, true
}

// validHostPort accepts "host[:port]": an ASCII host name, a canonical IPv4
// address, or a bracketed IPv6 address, with an optional canonical port
// from 1 to 65535.
func validHostPort(hostport string) bool {
	host, port := splitHostPort(hostport)
	if v6, ok := strings.CutPrefix(host, "["); ok {
		addr, closed := strings.CutSuffix(v6, "]")
		ip, err := netip.ParseAddr(addr)
		if !closed || err != nil || !ip.Is6() || ip.Zone() != "" {
			return false
		}
	} else if !validHostName(host) {
		return false
	}
	if port == "" {
		return true
	}
	digits, ok := strings.CutPrefix(port, ":")
	if !ok {
		return false
	}
	n, err := strconv.ParseUint(digits, 10, 16)
	return err == nil && n != 0 && strconv.FormatUint(n, 10) == digits
}

// splitHostPort splits "host[:port]" or "[v6][:port]" without validating
// either part; port keeps its leading ":".
func splitHostPort(hostport string) (string, string) {
	if strings.HasPrefix(hostport, "[") {
		end := strings.IndexByte(hostport, ']')
		if end < 0 {
			return hostport, ""
		}
		return hostport[:end+1], hostport[end+1:]
	}
	if i := strings.LastIndexByte(hostport, ':'); i >= 0 {
		return hostport[:i], hostport[i:]
	}
	return hostport, ""
}

// validHostName accepts a DNS name of ASCII letters, digits and hyphens in
// dot-separated labels of 1 to 63 bytes (an IPv4 address included), at most
// 253 bytes, with an optional trailing dot.
func validHostName(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > maxHostNameBytes {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if !boundedOf(label, maxHostLabelBytes, isLDH) || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4() && ip.String() == host
	}
	// A numeric last label is never a top-level domain: such a host is a
	// malformed IPv4 address.
	last := host[strings.LastIndexByte(host, '.')+1:]
	return !asciiDigits(last)
}
