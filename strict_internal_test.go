package treg

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// foldPair is two object keys and whether encoding/json matches them to
// the same field.
type foldPair struct {
	a, b string
	same bool
}

// unicodeMajor returns the major version of the toolchain's Unicode tables:
// 15 on Go 1.26, 17 on Go 1.27.
func unicodeMajor(tb testing.TB) int {
	tb.Helper()
	major, _, _ := strings.Cut(unicode.Version, ".")
	n, err := strconv.Atoi(major)
	require.NoError(tb, err)
	return n
}

// foldPairs returns the key pairs whose matching is easy to get wrong:
// ASCII case, the non-ASCII runes that fold to ASCII letters, the dotted
// and dotless i that do not, multi-rune orbits, a fold that depends on the
// Unicode version, non-letters and invalid UTF-8.
func foldPairs(tb testing.TB) []foldPair {
	tb.Helper()
	// Unicode 16 made the ligature ﬅ (U+FB05) simple-fold to ﬆ (U+FB06).
	ligaturesFold := unicodeMajor(tb) >= 16
	return []foldPair{
		{"a", "A", true},
		{"key", "KEY", true},
		{"Charged_Micro", "charged_micro", true},
		{"key", "kez", false},
		{"\u212a", "k", true}, // Kelvin sign
		{"\u212a", "K", true},
		{"\u212aey", "key", true},
		{"\u017f", "s", true}, // long s
		{"\u017f", "S", true},
		{"\u017ferved_by", "served_by", true},
		{"\u0130", "i", false}, // dotted capital I
		{"\u0130", "I", false},
		{"\u0131", "i", false}, // dotless small i
		{"\u0131", "I", false},
		{"\u0130", "\u0131", false},
		{"\u00df", "\u1e9e", true}, // small and capital sharp s
		{"\u00df", "ss", false},
		{"\u1e9e", "SS", false},
		{"\u03c3", "\u03c2", true}, // sigma, final sigma, capital sigma
		{"\u03c2", "\u03a3", true},
		{"\u03c3", "\u03a3", true},
		{"\u2126", "\u03c9", true}, // Ohm sign, small omega, capital omega
		{"\u2126", "\u03a9", true},
		{"\ufb05", "\ufb06", ligaturesFold}, // ligatures long s t and s t
		{"\ufb06", "st", false},
		{"\u00e9", "\u00c9", true},
		{"\u01c5", "\u01c6", true}, // title-case dz with caron, and its small and capital forms
		{"\u01c5", "\u01c4", true},
		{"\u03b8", "\u03d1", true}, // theta and theta symbol
		{"\u00b5", "\u039c", true}, // micro sign and capital mu
		{"\uff21", "\uff41", true}, // full-width A and a
		{"\uff21", "A", false},
		{"", "", true},
		{"1", "1", true},
		{"1", "\u00b9", false},
		{"a_b", "a-b", false},
		{"a_b", "AB", false},
		{"ab", "a b", false},
		{"\u20ac", "\u20ac", true},
		{"\u65e5\u672c", "\u65e5\u672c", true},
		{"\u65e5\u672c", "\u65e5\u672c\u8a9e", false},
		{"\xff", "\ufffd", true}, // invalid UTF-8 is U+FFFD
		{"\xff", "\xfe", true},
		{"a\xff", "A\ufffd", true},
		{"\xe2\x82", "\ufffd", false}, // two invalid bytes, two U+FFFD
	}
}

// escapeKey writes every rune of s as a \u escape, as a JSON key may be
// sent. Invalid UTF-8 becomes �, the value encoding/json decodes it to.
func escapeKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}

func TestFoldKeyPairs(t *testing.T) {
	t.Parallel()
	for _, p := range foldPairs(t) {
		t.Run(fmt.Sprintf("%+q~%+q", p.a, p.b), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, p.same, strings.EqualFold(p.a, p.b), "the pair's expectation is strings.EqualFold's")
			assert.Equal(t, p.same, foldKey(p.a) == foldKey(p.b), "foldKey matches as strings.EqualFold does")
			want := error(nil)
			if p.same {
				want = errDuplicateKey
			}
			assert.Equal(t, want, checkStrict([]byte(`{"`+p.a+`":1,"`+p.b+`":2}`), nil), "as sent")
			assert.Equal(t, want, checkStrict([]byte(`{"`+escapeKey(p.a)+`":1,"`+escapeKey(p.b)+`":2}`), nil),
				"escaped")
		})
	}
}

// TestFoldKeyEveryRune checks foldKey against strings.EqualFold for every
// rune: each folds to one rune of its own unicode.SimpleFold orbit, the
// same one for the whole orbit. So foldKey(a) == foldKey(b) exactly when
// every rune of a shares an orbit with the rune of b at the same place,
// which is strings.EqualFold.
func TestFoldKeyEveryRune(t *testing.T) {
	t.Parallel()
	var bad []rune
	for r := rune(0); r <= unicode.MaxRune && len(bad) < 10; r++ {
		if !utf8.ValidRune(r) {
			continue // a surrogate: string(r) is U+FFFD
		}
		s := string(r)
		folded := foldKey(s)
		if utf8.RuneCountInString(folded) != 1 || !strings.EqualFold(s, folded) {
			bad = append(bad, r)
			continue
		}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if foldKey(string(f)) != folded {
				bad = append(bad, r)
				break
			}
		}
	}
	assert.Empty(t, bad, "runes that fold outside their orbit or apart from it")
}

func TestFoldKeyKeepsFoldedKeys(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"", "served_by", "charged_micro", "a-b.c 1"} {
		assert.Equal(t, k, foldKey(k), "an ASCII key without upper case is already folded")
	}
	assert.Equal(t, "served_by", foldKey("Served_BY"))
	assert.Equal(t, "kelvin", foldKey("Kelvin"))
	assert.Equal(t, "st", foldKey("ſT"))
	assert.Equal(t, "��", foldKey("\xe2\x82"), "each invalid byte is U+FFFD, as encoding/json decodes it")
}

// jsonTagged returns a struct type with one int field tagged json:"name",
// or nil when encoding/json does not accept name as a tag name.
func jsonTagged(t *testing.T, name string) reflect.Type {
	t.Helper()
	typ := reflect.StructOf([]reflect.StructField{{
		Name: "F",
		Type: reflect.TypeFor[int](),
		Tag:  reflect.StructTag(`json:` + strconv.Quote(name)),
	}})
	out, err := json.Marshal(reflect.New(typ).Interface())
	require.NoError(t, err)
	var members map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &members))
	if _, ok := members[name]; !ok {
		return nil
	}
	return typ
}

// jsonMatches reports whether encoding/json decodes {"key":1} into the
// field of typ, whose tag names it.
func jsonMatches(t *testing.T, typ reflect.Type, key string) bool {
	t.Helper()
	member := `"` + key + `"`
	if strings.ContainsAny(key, "\"\\") || strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 }) {
		quoted, err := json.Marshal(key)
		require.NoError(t, err)
		member = string(quoted)
	}
	v := reflect.New(typ)
	require.NoError(t, json.Unmarshal([]byte(`{`+member+`:1}`), v.Interface()))
	return v.Elem().Field(0).Int() == 1
}

// TestFoldKeyMatchesEncodingJSON checks foldKey against encoding/json
// itself: for every pair of keys, a field tagged with the first matches the
// second exactly when foldKey folds them alike. Only keys encoding/json
// accepts as tag names can be the first; any key can be the second.
func TestFoldKeyMatchesEncodingJSON(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	var keys []string
	for _, p := range foldPairs(t) {
		for _, k := range []string{p.a, p.b} {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	keys = append(keys, "a\"b", `a\b`, "a\tb", "k-K", "ID", "id", "İd", "ıd")
	tagged := 0
	for _, a := range keys {
		typ := jsonTagged(t, a)
		for _, b := range keys {
			folds := foldKey(a) == foldKey(b)
			assert.Equal(t, strings.EqualFold(a, b), folds, "%+q %+q", a, b)
			if typ != nil {
				assert.Equal(t, jsonMatches(t, typ, b), folds, "field %+q, key %+q", a, b)
			}
		}
		if typ != nil {
			tagged++
		}
	}
	assert.Greater(t, tagged, 40, "most keys are tag names")
}

// TestIndexObjectOnBrokenInput covers indexObject, which walks with
// strictScanner, on input objectMembers never lets through: a broken
// object yields no members rather than a part of them.
func TestIndexObjectOnBrokenInput(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`{"a`, `{"a":`, `{"a":1,"b":[`} {
		o, ok := indexObject([]byte(in))
		assert.False(t, ok, in)
		assert.Equal(t, jsonObject{}, o, in)
	}
	o, ok := indexObject([]byte(`{"a":1`))
	require.True(t, ok, "a member already complete is found")
	raw, found := o.member("a")
	assert.True(t, found)
	assert.Equal(t, "1", string(raw))
}
