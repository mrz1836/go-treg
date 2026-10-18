package treg

// shapeJSON is the JSON form of a model that prints its shape only.
type shapeJSON struct {
	Type       string `json:"type"`
	ID         *int64 `json:"id,omitempty"`
	StatusCode int64  `json:"status_code,omitempty"`
	Stored     *bool  `json:"stored,omitempty"`
	CallRef    string `json:"call_ref,omitempty"`
	Count      *int   `json:"count,omitempty"`
}

// sealed holds a value that must never print, such as the client settings,
// a stream, a body's data or a CallError's cause. fmt and reflection-based
// dumpers print a function value only as an address, so neither a verb nor
// a struct holding a sealed value can reach the value itself.
type sealed[T any] func() T

// seal returns v sealed.
func seal[T any](v T) sealed[T] { return func() T { return v } }

// safeRef returns ", callRef: <ref>" for a valid call reference, else "".
func safeRef(ref string) string {
	if !validCallRef(ref) {
		return ""
	}
	return ", callRef: " + ref
}

// printable returns s when ok accepts it, else fallback, so that a string
// a caller set or a server sent prints only in a known, safe shape.
func printable(s string, ok func(string) bool, fallback string) string {
	if ok(s) {
		return s
	}
	return fallback
}

// shortHeaderName reports whether k is a valid header name of at most 64
// bytes: a name, never a value, short enough to print.
func shortHeaderName(k string) bool { return len(k) <= 64 && validHeaderName(k) }
