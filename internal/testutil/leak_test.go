package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg/internal/testutil"
)

// lkMarker is the secret the leak tests plant.
const lkMarker = "lk-marker"

// lkVisible holds the marker where every fmt verb and reflection can see it.
type lkVisible struct{ Name string }

// lkHidden holds the marker only inside a closure, which neither fmt nor
// reflection can see into.
type lkHidden struct{ get func() string }

func TestFormatVerbs(t *testing.T) {
	t.Parallel()
	verbs := testutil.FormatVerbs()
	assert.Len(t, verbs, 15)
	for _, v := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
		assert.Contains(t, verbs, v)
	}
}

func TestShapesHoldTheValue(t *testing.T) {
	t.Parallel()
	shapes := testutil.Shapes(&lkVisible{Name: lkMarker})
	require.Len(t, shapes, 11)
	var unexported []string
	for _, s := range shapes {
		assert.Contains(t, testutil.ReflectStrings(s.Value), lkMarker, s.Name)
		if s.Unexported {
			unexported = append(unexported, s.Name)
		}
	}
	assert.Equal(t, []string{"unexported fields", "pointer to unexported fields"}, unexported)
}

// assertFails runs check under record and asserts that it reported and
// recorded a failure, and let the test go on, as an assert does.
func assertFails(t *testing.T, check func(*recorder) bool, msgAndArgs ...any) {
	t.Helper()
	ok := true
	rec, returned := record(func(r *recorder) { ok = check(r) })
	assert.False(t, ok, msgAndArgs...)
	assert.True(t, rec.failed, msgAndArgs...)
	assert.True(t, returned, msgAndArgs...)
}

func TestAssertRedacted(t *testing.T) {
	t.Parallel()
	const secret = "key" // 6b6579 as hex
	assert.True(t, testutil.AssertRedacted(t, "clean output", []string{secret}))
	for _, out := range []string{"a key", "hex 6b6579", "HEX 6B6579"} {
		assertFails(t, func(r *recorder) bool { return testutil.AssertRedacted(r, out, []string{secret}) }, out)
	}
}

func TestAssertNeverPrints(t *testing.T) {
	t.Parallel()
	assert.True(t, testutil.AssertNeverPrints(t, lkHidden{get: func() string { return lkMarker }}, lkMarker))
	assertFails(t, func(r *recorder) bool { return testutil.AssertNeverPrints(r, lkVisible{Name: lkMarker}, lkMarker) })
}

func TestAssertShapesNeverPrint(t *testing.T) {
	t.Parallel()
	assert.True(t, testutil.AssertShapesNeverPrint(t, &lkHidden{get: func() string { return lkMarker }}, lkMarker))
	assertFails(t, func(r *recorder) bool {
		return testutil.AssertShapesNeverPrint(r, &lkVisible{Name: lkMarker}, lkMarker)
	})
}

func TestReflectStrings(t *testing.T) {
	t.Parallel()
	type node struct {
		s    string
		b    []byte
		arr  [1]string
		list []string
		m    map[string]string
		v    any
		get  func() string
		n    int
		next *node
	}
	n := &node{
		s: "s", b: []byte("b"), arr: [1]string{"arr"}, list: []string{"list"},
		m: map[string]string{"key": "value"}, v: "iface", get: func() string { return lkMarker }, n: 7,
	}
	n.next = n // a cycle is followed once
	assert.ElementsMatch(t, []string{"s", "b", "arr", "list", "key", "value", "iface"}, testutil.ReflectStrings(n))
	assert.Empty(t, testutil.ReflectStrings(nil))
	assert.Empty(t, testutil.ReflectStrings((*node)(nil)))

	// Each node is two levels below the previous one, so the walk stops
	// before the 40th.
	top := &node{s: "top"}
	cur := top
	for range 40 {
		cur.next = &node{s: "middle"}
		cur = cur.next
	}
	cur.s = "bottom"
	got := testutil.ReflectStrings(top)
	assert.Contains(t, got, "top")
	assert.NotContains(t, got, "bottom")
}

// TestReflectStringsWalksSharedValuesOnce shares a map, a slice and a pointer
// between fields and builds a cyclic map and a cyclic slice: each is walked
// once. A slice cut shorter from another and a pointer to a struct's first
// field share an address with what they were taken from, yet are walked too.
func TestReflectStringsWalksSharedValuesOnce(t *testing.T) {
	t.Parallel()
	type pair struct{ a, b string }
	list := []string{"x", "y"}
	m := map[string]string{"k": "v"}
	p := &pair{a: "a", b: "b"}
	shared := struct {
		short, long []string
		m1, m2      map[string]string
		first       *string
		p1, p2      *pair
	}{short: list[:1], long: list, m1: m, m2: m, first: &p.a, p1: p, p2: p}
	assert.ElementsMatch(t, []string{"x", "x", "y", "k", "v", "a", "a", "b"}, testutil.ReflectStrings(shared))

	cycle := map[string]any{"self": nil}
	cycle["self"] = cycle
	assert.Equal(t, []string{"self"}, testutil.ReflectStrings(cycle), "a cyclic map is walked once")
	loop := []any{"elem", nil}
	loop[1] = loop
	assert.Equal(t, []string{"elem"}, testutil.ReflectStrings(loop), "so is a cyclic slice")
}

// TestReflectStringsRewalksByAShorterPath meets a slice first at the end of a
// chain and then right below the top. For one chain length, the first meeting
// is at the depth bound, which cuts off the slice's elements; the shorter path
// must still find them.
func TestReflectStringsRewalksByAShorterPath(t *testing.T) {
	t.Parallel()
	type link struct {
		list []string
		next *link
	}
	shared := []string{"shared"}
	for n := range 40 {
		top := &link{}
		cur := top
		for range n {
			cur.next = &link{}
			cur = cur.next
		}
		cur.list = shared
		got := testutil.ReflectStrings(struct {
			deep any
			list []string
		}{deep: top, list: shared})
		assert.Contains(t, got, "shared", "a chain of %d links", n)
	}
}
