package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxStrictDepth bounds the nesting the strict check walks, as encoding/json
// bounds its own.
const maxStrictDepth = 10000

// strictFailure applies the strict rules for SDK models to b: one valid
// JSON value, no repeated keys outside the members skip names, and nesting
// within encoding/json's bound.
func strictFailure(b []byte, skip func(string) bool) *decodeFailure {
	if df := decodeValue(b, new(json.RawMessage), false); df != nil {
		return df
	}
	if err := checkStrict(b, skip); err != nil {
		if errors.Is(err, errTooDeep) {
			return &decodeFailure{FailureMalformedResponse, reasonTooDeep, err}
		}
		return &decodeFailure{FailureMalformedResponse, reasonDuplicateKey, err}
	}
	return nil
}

// rawIsland reports whether a member holds an open fragment that the strict
// check leaves alone: descriptor inputs, routing, observations, reviews,
// examples, ledger metadata, async task blobs and spend series.
func rawIsland(key string) bool {
	switch key {
	case "input", "test_request", "routing", "observed", "reviews", "example_response", "meta",
		"async_task", "series", "buckets", "ranking", "plan", "dropped", "settle", "usage", "modifiers",
		"table", "output", "raw", "detail", "advice", "verification", "ignored_filters", "health_check", "examples",
		"cli", "version":
		return true
	}
	return false
}

// checkStrict walks b, one JSON value, and refuses an object that repeats a
// key, compared as encoding/json matches keys (ignoring case), or nesting
// deeper than encoding/json allows. Members for which skip reports true are
// parsed but not checked. b must already be valid JSON.
func checkStrict(b []byte, skip func(string) bool) error {
	s := strictScanner{b: b, skip: skip}
	if err := s.value(true, 0); err != nil {
		return err
	}
	s.space()
	if s.i != len(s.b) {
		return errTrailingData
	}
	return nil
}

// strictScanner is checkStrict's recursive-descent walker.
type strictScanner struct {
	b    []byte
	i    int
	skip func(string) bool
}

func (s *strictScanner) space() {
	for s.i < len(s.b) && (s.b[s.i] == ' ' || s.b[s.i] == '\t' || s.b[s.i] == '\n' || s.b[s.i] == '\r') {
		s.i++
	}
}

func (s *strictScanner) value(check bool, depth int) error {
	s.space()
	if s.i >= len(s.b) {
		return errInvalidJSON
	}
	switch c := s.b[s.i]; c {
	case '{':
		return s.object(check, depth+1)
	case '[':
		return s.array(check, depth+1)
	case '"':
		_, err := s.str(false)
		return err
	case 't':
		return s.literal("true")
	case 'f':
		return s.literal("false")
	case 'n':
		return s.literal("null")
	}
	return s.number()
}

func (s *strictScanner) object(check bool, depth int) error {
	if depth > maxStrictDepth {
		return errTooDeep
	}
	s.i++
	var seen map[string]struct{}
	s.space()
	if s.i < len(s.b) && s.b[s.i] == '}' {
		s.i++
		return nil
	}
	for {
		key, err := s.member(check, &seen)
		if err != nil {
			return err
		}
		childCheck := check && (s.skip == nil || !s.skip(key))
		if err := s.value(childCheck, depth); err != nil {
			return err
		}
		s.space()
		if s.i >= len(s.b) {
			return errInvalidJSON
		}
		switch s.b[s.i] {
		case ',':
			s.i++
		case '}':
			s.i++
			return nil
		default:
			return errInvalidJSON
		}
	}
}

// member scans an object key and its colon, recording the folded key in
// seen and refusing a repeat when check is set.
func (s *strictScanner) member(check bool, seen *map[string]struct{}) (string, error) {
	s.space()
	if s.i >= len(s.b) || s.b[s.i] != '"' {
		return "", errInvalidJSON
	}
	key, err := s.str(check)
	if err != nil {
		return "", err
	}
	if check {
		folded := foldKey(key)
		if _, dup := (*seen)[folded]; dup {
			return "", errDuplicateKey
		}
		if *seen == nil {
			*seen = make(map[string]struct{})
		}
		(*seen)[folded] = struct{}{}
	}
	s.space()
	if s.i >= len(s.b) || s.b[s.i] != ':' {
		return "", errInvalidJSON
	}
	s.i++
	return key, nil
}

func (s *strictScanner) array(check bool, depth int) error {
	if depth > maxStrictDepth {
		return errTooDeep
	}
	s.i++
	s.space()
	if s.i < len(s.b) && s.b[s.i] == ']' {
		s.i++
		return nil
	}
	for {
		if err := s.value(check, depth); err != nil {
			return err
		}
		s.space()
		if s.i >= len(s.b) {
			return errInvalidJSON
		}
		switch s.b[s.i] {
		case ',':
			s.i++
		case ']':
			s.i++
			return nil
		default:
			return errInvalidJSON
		}
	}
}

// str scans a string and, when decode is set, returns its decoded text. It
// finds the closing quote with bytes.IndexByte, stepping over each escape,
// and stays linear however many escapes the string holds: the search for a
// quote restarts only past an escaped one.
func (s *strictScanner) str(decode bool) (string, error) {
	start := s.i
	s.i++
	escaped := false
	quote := -1 // the first quote at or after s.i, once found
	for {
		if quote < s.i {
			n := bytes.IndexByte(s.b[s.i:], '"')
			if n < 0 {
				return "", errInvalidJSON
			}
			quote = s.i + n
		}
		n := bytes.IndexByte(s.b[s.i:quote], '\\')
		if n < 0 {
			break
		}
		escaped = true
		s.i += n + 2 // the backslash and the byte it escapes
	}
	s.i = quote + 1
	if !decode {
		return "", nil
	}
	raw := s.b[start:s.i]
	if !escaped {
		return string(raw[1 : len(raw)-1]), nil
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", errInvalidJSON
	}
	return out, nil
}

func (s *strictScanner) literal(lit string) error {
	if !bytes.HasPrefix(s.b[s.i:], []byte(lit)) {
		return errInvalidJSON
	}
	s.i += len(lit)
	return nil
}

func (s *strictScanner) number() error {
	start := s.i
	for s.i < len(s.b) && isNumberByte(s.b[s.i]) {
		s.i++
	}
	if s.i == start {
		return errInvalidJSON
	}
	return nil
}

// isNumberByte reports whether c can appear in a JSON number.
func isNumberByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E'
}

// foldKey folds a key the way encoding/json matches a key to a field name,
// which is strings.EqualFold: foldKey(a) == foldKey(b) exactly when
// strings.EqualFold(a, b). An ASCII letter folds to lower case, and every
// other rune to the least rune of its unicode.SimpleFold orbit (an ASCII
// upper-case least rune, as for the Kelvin sign, to lower case too). A byte
// that is not valid UTF-8 folds to U+FFFD, which is what encoding/json
// decodes it to. A key of ASCII without upper-case letters, the usual JSON
// key, is returned as it is, without allocating.
func foldKey(k string) string {
	i := 0
	for i < len(k) && k[i] < utf8.RuneSelf && !isUpperASCII(k[i]) {
		i++
	}
	if i == len(k) {
		return k
	}
	var folded strings.Builder
	folded.Grow(len(k) + utf8.UTFMax)
	folded.WriteString(k[:i])
	for i < len(k) {
		if c := k[i]; c < utf8.RuneSelf {
			folded.WriteByte(lowerASCII(c))
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(k[i:])
		folded.WriteRune(foldRune(r))
		i += n
	}
	return folded.String()
}

// foldRune returns the least rune of r's unicode.SimpleFold orbit, the
// runes strings.EqualFold matches to r, lower-cased when that is an ASCII
// letter.
func foldRune(r rune) rune {
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		least = min(least, f)
	}
	if 'A' <= least && least <= 'Z' {
		return least + 'a' - 'A'
	}
	return least
}

// isUpperASCII reports whether c is an ASCII upper-case letter.
func isUpperASCII(c byte) bool { return 'A' <= c && c <= 'Z' }

// lowerASCII returns c in lower case when it is an ASCII letter.
func lowerASCII(c byte) byte {
	if isUpperASCII(c) {
		return c + 'a' - 'A'
	}
	return c
}
