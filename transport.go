package treg

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Transport constants.
const (
	// defaultUserAgent is the User-Agent header unless WithUserAgent replaces it.
	defaultUserAgent = "go-treg"

	// maxResponseHeaderBytes bounds the response headers, checked by the
	// redirect guard on every transport.
	maxResponseHeaderBytes = 64 << 10

	// transportHeaderBytes is the SDK transport's own backstop, above the
	// guard's bound so that the guard reports an oversized header first.
	transportHeaderBytes = 2 * maxResponseHeaderBytes

	dialTimeout           = 10 * time.Second
	keepAlivePeriod       = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	idleConnTimeout       = 30 * time.Second
	expectContinueTimeout = time.Second
	maxIdleConnsPerHost   = 16
	maxIdleConns          = 4 * maxIdleConnsPerHost
)

// newTransport returns a fresh transport, never a clone of
// http.DefaultTransport: no proxy (environment variables are never read),
// HTTP/2 when the server offers it, TLS 1.2 or later, bounded dial and
// handshake times, and its own connection pool. It sets no response header
// timeout: the SDK's own deadlines bound every wait.
func newTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlivePeriod}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    tlsHandshakeTimeout,
		IdleConnTimeout:        idleConnTimeout,
		MaxIdleConns:           maxIdleConns,
		MaxIdleConnsPerHost:    maxIdleConnsPerHost,
		ExpectContinueTimeout:  expectContinueTimeout,
		MaxResponseHeaderBytes: transportHeaderBytes,
	}
}

// client returns the HTTP client requests use: a copy of the injected client,
// or a new one on the SDK's transport, that never follows a redirect, keeps
// no cookies and has no Timeout of its own (a caller's Timeout caps the
// SDK's deadlines instead; see WithHTTPClient). A nil injected transport
// means the SDK's own transport.
func (o *options) client() *http.Client {
	var c http.Client
	if o.httpClientSet {
		c = *o.httpClient
	}
	next := c.Transport
	if next == nil {
		next = newTransport()
	}
	c.Transport = redirectGuard{next: next}
	c.CheckRedirect = refuseRedirect
	c.Jar = nil
	c.Timeout = 0
	return &c
}

// refuseRedirect stops the client at the first response, so the token is
// never sent to another URL.
func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// locationKey is the context key of a request's locationHolder.
type locationKey struct{}

// locationHolder receives the Location header the redirect guard removes
// from a 301, 302, 303, 307 or 308, so that the SDK can restore it in the
// sensitive Header clone.
type locationHolder struct {
	mu     sync.Mutex
	values []string
}

// set records the Location values.
func (h *locationHolder) set(v []string) {
	h.mu.Lock()
	h.values = slices.Clone(v)
	h.mu.Unlock()
}

// get returns the recorded Location values.
func (h *locationHolder) get() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.values)
}

// headersTooLargeError reports response headers over maxResponseHeaderBytes.
// It keeps only the status, which decides whether the outcome is known.
type headersTooLargeError struct {
	status int
}

// Error returns a fixed message.
func (headersTooLargeError) Error() string { return errHeadersTooLarge.Error() }

// redirectGuard wraps the transport of every SDK client. It moves the
// Location of the five redirect statuses http.Client acts on into the
// request's locationHolder, so the client neither follows the redirect nor
// fails with an error that quotes the URL, and it refuses response headers
// over 64 KiB on any transport.
type redirectGuard struct {
	next http.RoundTripper
}

// RoundTrip passes r to the wrapped transport and inspects the response.
func (g redirectGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := g.next.RoundTrip(r)
	if err != nil || resp == nil {
		return resp, err
	}
	if headerBytes(resp.Header) > maxResponseHeaderBytes {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, headersTooLargeError{status: resp.StatusCode}
	}
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		var loc []string
		for k, vs := range resp.Header {
			if strings.EqualFold(k, headerLocation) {
				loc = append(loc, vs...)
				delete(resp.Header, k)
			}
		}
		if h, ok := r.Context().Value(locationKey{}).(*locationHolder); ok && len(loc) > 0 {
			h.set(loc)
		}
	}
	return resp, nil
}

// CloseIdleConnections closes the wrapped transport's idle connections, if
// it has any, so that (*http.Client).CloseIdleConnections still reaches it.
func (g redirectGuard) CloseIdleConnections() {
	if c, ok := g.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// headerBytes estimates the wire size of h: each value as "name: value\r\n".
func headerBytes(h http.Header) int {
	n := 0
	for k, vs := range h {
		for _, v := range vs {
			n += len(k) + len(v) + 4
		}
	}
	return n
}

// noReplayBody is the body of every request without one whose method is
// GET, HEAD, DELETE or OPTIONS: always empty, and neither nil nor
// http.NoBody, which the standard library treats as safe to resend. Its
// request also has TransferEncoding "identity", so HTTP/1 sends no
// Content-Length, Transfer-Encoding or body bytes and never probes the body
// against a timer. It holds no state, so one value serves every request.
type noReplayBody struct{}

// Read reports the end of the empty body.
func (noReplayBody) Read([]byte) (int, error) { return 0, io.EOF }

// Close does nothing.
func (noReplayBody) Close() error { return nil }

// classify maps a failed exchange onto a CallError: the caller's context
// first, then an SDK deadline or network timeout, then the request body's
// own failure, then oversized headers, else a transport failure of a fixed
// class. The original error is never kept.
func classify(caller, sdk context.Context, op Operation, err error, status int, rb *readerBody) *CallError {
	if cerr := caller.Err(); cerr != nil {
		return contextFailure(op, cerr, status)
	}
	if sdk.Err() != nil || errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		ce := timeoutFailure(op, status)
		if errors.Is(context.Cause(sdk), errHeaderTimeout) {
			ce.Reason = reasonHeaderTimeout
		}
		return ce
	}
	if f := rb.failure(); f != nil {
		return transportFailure(op, status, f)
	}
	if he, ok := errors.AsType[headersTooLargeError](err); ok {
		return newCallError(op, FailureUnexpectedResponse, reasonHeadersTooLarge, he.status, errHeadersTooLarge)
	}
	return transportFailure(op, status, transportClass(err))
}

// isNetTimeout reports whether any error in err's tree reports a timeout,
// such as a dial or TLS handshake timeout. It walks the whole tree, because
// the *url.Error that http.Client adds reports a timeout only when its direct
// cause does, and a transport may wrap a timeout further down.
func isNetTimeout(err error) bool {
	if err == nil {
		return false
	}
	if t, ok := err.(interface{ Timeout() bool }); ok && t.Timeout() {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // walking the tree by hand
	case interface{ Unwrap() error }:
		return isNetTimeout(u.Unwrap())
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(u.Unwrap(), isNetTimeout)
	}
	return false
}

// transportClass reduces a transport error to one of a few fixed classes.
func transportClass(err error) error {
	if errors.Is(err, errTransportStalled) {
		return errTransportStalled
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return errTransportDNS
	}
	var op string
	if oe, ok := errors.AsType[*net.OpError](err); ok {
		op = oe.Op
	}
	if op == "proxyconnect" {
		return errTransportProxy
	}
	if isTLSError(err) {
		return errTransportTLS
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) {
		return errTransportReset
	}
	if op == "dial" {
		return errTransportDial
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return errTransportClosed
	}
	return errTransportOther
}

// transportFailure returns the CallError of a transport class, with the
// class as its cause and the Reason of its row.
func transportFailure(op Operation, status int, class error) *CallError {
	return newCallError(op, FailureTransport, transportClassOf(class).reason, status, class)
}

// transportClassRow is one transport class: its fixed cause, its Reason, and
// whether it proves the request was never sent (name resolution, proxy,
// connection or TLS setup failed).
type transportClassRow struct {
	class       error
	reason      string
	preDispatch bool
}

// transportClassOf returns the row of class. Any other error, such as
// errBuildRequest or errTransportOther, is "other", which proves nothing.
func transportClassOf(class error) transportClassRow {
	for _, row := range [...]transportClassRow{
		{errTransportDNS, reasonDNS, true},
		{errTransportProxy, reasonProxy, true},
		{errTransportDial, reasonDial, true},
		{errTransportTLS, reasonTLS, true},
		{errTransportReset, reasonReset, false},
		{errTransportClosed, reasonClosed, false},
		{errStreamClosed, reasonClosed, false},
		{errTransportStalled, reasonStalled, false},
		{errTransportShortBody, reasonShortBody, false},
		{errTransportLongBody, reasonLongBody, false},
		{errTransportBodyRead, reasonBodyRead, false},
	} {
		if errors.Is(class, row.class) {
			return row
		}
	}
	return transportClassRow{reason: reasonOther}
}

// isTLSError reports whether err comes from the TLS handshake or record layer.
func isTLSError(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}
	_, ok := errors.AsType[tls.AlertError](err)
	return ok
}
