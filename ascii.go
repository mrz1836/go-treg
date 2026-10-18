package treg

import "strings"

// allBytes reports whether ok accepts every byte of s. It is true for "".
func allBytes(s string, ok func(byte) bool) bool {
	for i := range len(s) {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

// boundedOf reports whether s is 1 to maxBytes bytes that ok all accepts.
func boundedOf(s string, maxBytes int, ok func(byte) bool) bool {
	return s != "" && len(s) <= maxBytes && allBytes(s, ok)
}

// checkBounded is boundedOf with a reason: "" when s is 1 to maxBytes bytes
// that ok all accepts, else required, too_long, or the reason for the first
// byte ok refuses (see charReason).
func checkBounded(s string, maxBytes int, ok func(byte) bool) string {
	switch {
	case s == "":
		return reasonRequired
	case len(s) > maxBytes:
		return reasonTooLong
	}
	return checkChars(s, ok)
}

// checkChars returns "" when allowed accepts every byte of s, else the
// reason for the first byte it refuses (see charReason).
func checkChars(s string, allowed func(byte) bool) string {
	for i := range len(s) {
		if !allowed(s[i]) {
			return charReason(s[i])
		}
	}
	return ""
}

// checkEscaped is checkChars for an escaped string: every "%" must start a
// valid %HH escape (else invalid_escape), and an escape's digits are not
// checked against allowed.
func checkEscaped(s string, allowed func(byte) bool) string {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '%':
			if !validEscapeAt(s, i) {
				return reasonInvalidEscape
			}
			i += 2
		case !allowed(s[i]):
			return charReason(s[i])
		}
	}
	return ""
}

// charReason returns the reason for a refused byte: control_character for
// a control byte, else invalid_character.
func charReason(c byte) string {
	if isCTL(c) {
		return reasonControlCharacter
	}
	return reasonInvalidCharacter
}

// asciiDigits reports whether s is non-empty and only ASCII digits.
func asciiDigits(s string) bool { return s != "" && allBytes(s, isDigit) }

// validCode reports whether s is a fixed code: 1 to 64 bytes of [a-z0-9_].
func validCode(s string) bool { return boundedOf(s, maxCodeBytes, isCodeChar) }

// hasPrefixFold reports whether s starts with prefix, ignoring ASCII case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// validEscapeAt reports whether s holds a valid %HH escape at i.
func validEscapeAt(s string, i int) bool {
	return i+2 < len(s) && s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2])
}

// unhex returns the value of the hex digit c.
func unhex(c byte) byte {
	switch {
	case isDigit(c):
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// percentDecode decodes every valid %HH escape in s, leaving anything else
// as it is. It never treats "+" as a space.
func percentDecode(s string) string {
	if strings.IndexByte(s, '%') < 0 {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if validEscapeAt(s, i) {
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		b = append(b, s[i])
	}
	return string(b)
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isLowerHex reports whether c is a digit or a lowercase hex letter.
func isLowerHex(c byte) bool { return isDigit(c) || (c >= 'a' && c <= 'f') }

// isHex reports whether c is an ASCII hex digit of either case.
func isHex(c byte) bool { return isLowerHex(c) || (c >= 'A' && c <= 'F') }

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || isDigit(c) }

// isLDH reports whether c is an ASCII letter, digit or hyphen: a host
// label byte.
func isLDH(c byte) bool { return isAlnum(c) || c == '-' }

// isCodeChar reports whether c is in [a-z0-9_]: a reason, refusal code,
// operation or tag key byte.
func isCodeChar(c byte) bool { return (c >= 'a' && c <= 'z') || isDigit(c) || c == '_' }

// isProviderChar reports whether c is in [A-Za-z0-9._-]: a provider service
// name byte.
func isProviderChar(c byte) bool { return isAlnum(c) || c == '.' || c == '_' || c == '-' }

// isTagValueChar reports whether c is in [A-Za-z0-9._:-]: a tag value byte.
func isTagValueChar(c byte) bool { return isProviderChar(c) || c == ':' }

// isUnreserved reports whether c is an RFC 3986 unreserved character:
// a letter, a digit or "-._~".
func isUnreserved(c byte) bool { return isAlnum(c) || c == '-' || c == '.' || c == '_' || c == '~' }

// isPathChar reports whether c may appear unescaped in a path: an RFC 3986
// pchar other than "%", or "/".
func isPathChar(c byte) bool {
	if isUnreserved(c) {
		return true
	}
	switch c {
	case '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=', ':', '@', '/':
		return true
	}
	return false
}

// isDecodedPathChar reports whether c may appear in a percent-decoded
// path: any byte but a control byte, a space, "?", "#" or "\".
func isDecodedPathChar(c byte) bool {
	return !isCTL(c) && c != ' ' && c != '?' && c != '#' && c != '\\'
}

// isRawQueryChar reports whether c may appear unescaped in RawQuery:
// visible ASCII other than "#".
func isRawQueryChar(c byte) bool { return isVisible(c) && c != '#' }

// isTokenChar reports whether c is an RFC 7230 tchar.
func isTokenChar(c byte) bool {
	if isAlnum(c) {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isVisible reports whether c is visible ASCII: 0x21 to 0x7E.
func isVisible(c byte) bool { return c >= 0x21 && c <= 0x7e }

// isHeaderValueChar reports whether c may appear in a header value: visible
// ASCII, a space or a horizontal tab.
func isHeaderValueChar(c byte) bool { return isVisible(c) || isSpace(c) }

// isSpace reports whether c is a space or a horizontal tab.
func isSpace(c byte) bool { return c == ' ' || c == '\t' }

// isCTL reports whether c is an ASCII control byte: below 0x20, or DEL.
func isCTL(c byte) bool { return c < 0x20 || c == 0x7f }
