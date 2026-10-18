// Package testutil holds the test plumbing go-treg's tests share: HTTP fakes
// and scriptable bodies, a recording TLS server for HTTP/1.1 and HTTP/2, a
// scripted transport, servers that drop connections or fault HTTP/2 streams
// for replay tests, a helper for spelling JSON escapes, formatting-leak
// assertions, goroutine-leak checks, and race-aware allocation guards.
//
// It is imported only by tests. It imports testify and the standard library,
// never the treg package, so that the root package's internal tests can use
// it too.
package testutil
