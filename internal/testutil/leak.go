package testutil

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FormatVerbs returns the fmt verbs a caller might apply to a value. Most of
// them bypass String and GoString and print fields by reflection, which
// dereferences pointers and reaches unexported fields.
func FormatVerbs() []string {
	return []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%t", "%d", "%b", "%o", "%e", "%c", "%U", "%p"}
}

// Shape is one way a caller might hold a value, named for test output.
type Shape struct {
	Name  string
	Value any

	// Unexported marks a holding in an unexported struct field. fmt prints
	// such a field by reflection, bypassing the value's own Format and
	// String, so only a value that keeps its secrets out of reflection's
	// reach, inside function values, can protect itself there.
	Unexported bool
}

type exportedFields[T any] struct {
	V T
	P *T
}

type unexportedFields[T any] struct {
	v T
	p *T
}

// Shapes returns the ways a caller might hold p: the pointer, the value, a
// pointer to the pointer, exported and unexported struct fields and pointers
// to them, and slices and maps of pointers and values.
func Shapes[T any](p *T) []Shape {
	return []Shape{
		{Name: "pointer", Value: p},
		{Name: "value", Value: *p},
		{Name: "pointer to pointer", Value: &p},
		{Name: "exported fields", Value: exportedFields[T]{V: *p, P: p}},
		{Name: "pointer to exported fields", Value: &exportedFields[T]{V: *p, P: p}},
		{Name: "unexported fields", Value: unexportedFields[T]{v: *p, p: p}, Unexported: true},
		{Name: "pointer to unexported fields", Value: &unexportedFields[T]{v: *p, p: p}, Unexported: true},
		{Name: "slice of pointers", Value: []*T{p}},
		{Name: "slice of values", Value: []T{*p}},
		{Name: "map of pointers", Value: map[string]*T{"k": p}},
		{Name: "map of values", Value: map[string]T{"k": *p}},
	}
}

// AssertRedacted fails tb when out contains any secret, in plain text or as hex
// in either case. msgAndArgs annotate a failure.
func AssertRedacted(tb testing.TB, out string, secrets []string, msgAndArgs ...any) bool {
	tb.Helper()
	ok := true
	for _, s := range secrets {
		h := hex.EncodeToString([]byte(s))
		for _, form := range []string{s, h, strings.ToUpper(h)} {
			ok = assert.NotContains(tb, out, form, msgAndArgs...) && ok
		}
	}
	return ok
}

// AssertNeverPrints formats v with every verb of FormatVerbs and fails tb when
// any output holds a secret, as AssertRedacted checks.
func AssertNeverPrints(tb testing.TB, v any, secrets ...string) bool {
	tb.Helper()
	ok := true
	for _, verb := range FormatVerbs() {
		ok = AssertRedacted(tb, fmt.Sprintf(verb, v), secrets, "verb %s on %T", verb, v) && ok
	}
	return ok
}

// AssertShapesNeverPrint formats every Shape of p with every verb of
// FormatVerbs and fails tb when any output holds a secret, as AssertRedacted
// checks.
func AssertShapesNeverPrint[T any](tb testing.TB, p *T, secrets ...string) bool {
	tb.Helper()
	ok := true
	for _, sh := range Shapes(p) {
		for _, verb := range FormatVerbs() {
			ok = AssertRedacted(tb, fmt.Sprintf(verb, sh.Value), secrets, "%s %s", sh.Name, verb) && ok
		}
	}
	return ok
}

// ReflectStrings returns every string and byte slice reachable from v by
// reflection, unexported fields included, as a dumper that walks values would
// find them. Function values are opaque to reflection and yield nothing.
// What a pointer, map or slice refers to is walked once, however many places
// share it and through any cycle, unless a shorter path reaches it later.
func ReflectStrings(v any) []string {
	w := &walker{seen: map[ref]int{}}
	w.walk(reflect.ValueOf(v), 0)
	return w.out
}

// ref identifies what a pointer, map or slice refers to. Its type, and a
// slice's length, are part of it: a pointer to a struct and one to the
// struct's first field share an address, as do a slice and a shorter one cut
// from it, yet each reaches values the other does not.
type ref struct {
	typ  reflect.Type
	addr uintptr
	len  int
}

// walker collects what ReflectStrings returns. seen holds the shallowest
// depth at which each ref has been walked.
type walker struct {
	seen map[ref]int
	out  []string
}

// maxDepth bounds the walk, as a guard against unusually deep values.
const maxDepth = 64

// walk collects the strings reachable from v, which lies depth levels below
// the top.
func (w *walker) walk(v reflect.Value, depth int) {
	if !v.IsValid() || depth > maxDepth {
		return
	}
	switch v.Kind() {
	case reflect.String:
		w.out = append(w.out, v.String())
	case reflect.Pointer:
		w.walkPointer(v, depth)
	case reflect.Interface:
		w.walk(v.Elem(), depth+1)
	case reflect.Struct:
		w.walkStruct(v, depth)
	case reflect.Slice:
		w.walkSlice(v, depth)
	case reflect.Array:
		w.walkElems(v, depth)
	case reflect.Map:
		w.walkMap(v, depth)
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
		// Numbers, booleans, channels and functions hold no strings that
		// reflection can reach.
	}
}

// walkPointer walks what v points to, unless enter declines it.
func (w *walker) walkPointer(v reflect.Value, depth int) {
	if w.enter(v, depth) {
		w.walk(v.Elem(), depth+1)
	}
}

// walkStruct walks each of v's fields, unexported ones included.
func (w *walker) walkStruct(v reflect.Value, depth int) {
	for _, f := range v.Fields() {
		w.walk(f, depth+1)
	}
}

// walkSlice collects a byte slice, nil or not, as one string, and walks the
// elements of any other slice unless enter declines it.
func (w *walker) walkSlice(v reflect.Value, depth int) {
	if v.Type().Elem().Kind() == reflect.Uint8 {
		w.out = append(w.out, string(v.Bytes()))
		return
	}
	if w.enter(v, depth) {
		w.walkElems(v, depth)
	}
}

// walkMap walks each of v's keys and values, unless enter declines it.
func (w *walker) walkMap(v reflect.Value, depth int) {
	if !w.enter(v, depth) {
		return
	}
	iter := v.MapRange()
	for iter.Next() {
		w.walk(iter.Key(), depth+1)
		w.walk(iter.Value(), depth+1)
	}
}

// enter reports whether to walk what v, a pointer, map or slice, refers to,
// met at depth: not when v is nil, nor when a walk has already reached it at
// depth or shallower. Otherwise it records depth as the shallowest so far, so
// that a value first met near maxDepth is walked again when a shorter path
// reaches it, and nothing within maxDepth of the top is missed.
func (w *walker) enter(v reflect.Value, depth int) bool {
	if v.IsNil() {
		return false
	}
	r := ref{typ: v.Type(), addr: v.Pointer()}
	if v.Kind() == reflect.Slice {
		r.len = v.Len()
	}
	if d, ok := w.seen[r]; ok && d <= depth {
		return false
	}
	w.seen[r] = depth
	return true
}

// walkElems walks each element of v, a slice or an array.
func (w *walker) walkElems(v reflect.Value, depth int) {
	for i := range v.Len() {
		w.walk(v.Index(i), depth+1)
	}
}
