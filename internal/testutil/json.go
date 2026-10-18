package testutil

import "strings"

// Esc returns s with every ~ replaced by a backslash, so that JSON escapes read
// naturally in raw string literals: Esc(`"~u0061"`) is the JSON string "a"
// spelled with an escape.
func Esc(s string) string { return strings.ReplaceAll(s, "~", `\`) }
