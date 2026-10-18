package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg/internal/testutil"
)

// allocBodyBytes is the size of the large request bodies: 1 MiB.
const allocBodyBytes = 1 << 20

// allocToken is a synthetic token, long enough for the credential scan.
const allocToken = "trg_alloc_aaaabbbbaaaabbbbSYNTH" //nolint:gosec // a synthetic token, not a credential

// allocBody returns a JSON document of exactly n bytes:
// {"blob":"aaa...a"}.
func allocBody(n int) []byte {
	prefix, suffix := `{"blob":"`, `"}`
	return []byte(prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix)
}

// allocLargeCalls returns the large-body requests the guards build: the
// same 1 MiB JSON document as a BytesBody and as a JSONBody.
func allocLargeCalls(t *testing.T) (asBytes, asJSON CallRequest) {
	t.Helper()
	body := allocBody(allocBodyBytes)
	require.True(t, json.Valid(body))
	base := CallRequest{
		Target: CatalogEndpoint("vendor.bulk.records.import"),
		Method: http.MethodPost,
		Header: http.Header{"Model": {"bulk-1"}},
		Controls: Controls{
			MaxCost:        new(MicroUSD(20_000)),
			IdempotencyKey: "alloc-job-0001",
			Tags:           []Tag{{Key: "customer", Value: "cust_example"}},
		},
	}
	asBytes, asJSON = base, base
	asBytes.Body = BytesBody(jsonContentType, body)
	asJSON.Body = JSONBody(json.RawMessage(body))
	return asBytes, asJSON
}

// allocResponseHeader is a realistic header set of a charged, replayed,
// archive-served async submission.
func allocResponseHeader() http.Header {
	return http.Header{
		"Content-Type":               {"application/json"},
		"Date":                       {"Sat, 17 Oct 2026 12:00:00 GMT"},
		"X-Treg-Call-Id":             {strings.Repeat("a1", 16) + ":r0"},
		"X-Treg-Cost-Micro":          {"20000"},
		"X-Treg-Original-Cost-Micro": {"24000"},
		"X-Treg-Idempotent-Replay":   {"true"},
		"X-Treg-Served-By":           {"vendor.people.email.find"},
		"X-Treg-Served-Via":          {"overflow:relay-1"},
		"X-Treg-Cache":               {"hit"},
		"X-Treg-Age":                 {"3600"},
		"X-Treg-Fetched-At":          {"2026-10-01T12:00:00Z"},
		"Retry-After":                {"2"},
		"X-Treg-Async": {`{"id_from":"id","poll":{"endpoint":"vendor.jobs.get","param":{"in":"pathParams","name":"id"}},` +
			`"status":{"path":"status","success":["succeeded"],"failure":["failed"]},"result":{"path":"output"},"interval":2}`},
	}
}

// allocCeiling returns go126 when version, a runtime.Version, is Go 1.26,
// and newer for Go 1.27 or any later or unrecognized version.
func allocCeiling(version string, go126, newer float64) float64 {
	if version == "go1.26" || strings.HasPrefix(version, "go1.26.") || strings.HasPrefix(version, "go1.26rc") {
		return go126
	}
	return newer
}

func TestAllocCeiling(t *testing.T) {
	t.Parallel()
	for version, want := range map[string]float64{
		"go1.26": 36, "go1.26.0": 36, "go1.26.9": 36, "go1.26rc1": 36,
		"go1.27": 18, "go1.27.2": 18, "go1.260": 18, "go1.28rc2": 18, "devel go1.27-abcdef": 18, "": 18,
	} {
		assert.InDelta(t, want, allocCeiling(version, 36, 18), 0, version)
	}
}

// allocMinGrowth returns the least heap growth of one run of f over a few
// measurements, so that a garbage collection emptying a pool mid-run does
// not count.
func allocMinGrowth(f func()) uint64 {
	growth := make([]uint64, 0, 5)
	for range cap(growth) {
		growth = append(growth, testutil.HeapGrowth(f))
	}
	return slices.Min(growth)
}

// TestAllocationCeilings locks in the allocation counts of request building
// and metadata parsing. Each ceiling is the count measured with Go 1.27 on
// darwin/arm64 plus small headroom; raise one only with a reason. The
// measurements, per run, on this tree / the after-A snapshot / the baseline:
//
//   - buildCall, 1 MiB BytesBody: 22 / 27 / 23
//   - buildCall, 1 MiB JSONBody: 23 / 28 / 24
//   - parseMetadata, 13 headers with an async descriptor: 21 / 56 / 58
//   - parseMetadata, the same without the async descriptor: 5 / 5 / 5
//   - foldKey, a lower-case ASCII key / one to fold: 0 / 1 (new on this tree)
//
// buildCall is one below the baseline because the credential scan no longer
// copies the body into a string. The four after-A added came from sorting
// the header names with slices.Sorted(maps.Keys(src)), whose iterator
// closures and collected slice reach the heap where a small make does not.
// The async descriptor fell from 56 to 21 when its five objects came to be
// read in place (objectMembers indexes each one's members in one pass, one
// allocation per object) rather than decoded into a
// map[string]json.RawMessage, which allocated the map, every key and a copy
// of every value.
//
// Go 1.26, which CI runs, measures the same except for the async
// descriptor: 38 on this tree (106 before the in-place reading), against
// 21. The difference is encoding/json's own: on Go 1.26 each json.Unmarshal
// of a member allocates its decode state and each string it decodes, while
// Go 1.27's encoding/json, built on encoding/json/v2, reuses its state and
// interns short strings. That ceiling alone is set per toolchain
// (allocCeiling), so that it stays tight on Go 1.27 rather than letting a
// doubling through.
//
// The count cannot tell one 1 MiB copy from two, so
// TestBuildCallLargeBodyHeapGrowth guards the bytes.
func TestAllocationCeilings(t *testing.T) { //nolint:paralleltest // measures allocations
	testutil.SkipUnderRace(t)
	c, err := NewClient(allocToken)
	require.NoError(t, err)
	asBytes, asJSON := allocLargeCalls(t)
	h := allocResponseHeader()
	m := parseMetadata(OpCall, http.StatusOK, h)
	require.Empty(t, m.Unparsed, "every header of the set parses")
	require.NotNil(t, m.Async)
	plain := h.Clone()
	plain.Del("X-Treg-Async")

	var sinkWire *wire
	var sinkMeta Metadata
	var sinkKey string
	var buildErr error
	build := func(req CallRequest) func() {
		return func() {
			w, berr := c.buildCall(req)
			sinkWire, buildErr = w, errors.Join(buildErr, berr)
		}
	}
	testutil.CheckCeilings(t, []testutil.Ceiling{
		{Name: "buildCall BytesBody 1 MiB", Max: 24, Run: build(asBytes)},
		{Name: "buildCall JSONBody 1 MiB", Max: 25, Run: build(asJSON)},
		{Name: "parseMetadata realistic headers", Max: allocCeiling(runtime.Version(), 41, 23), Run: func() { sinkMeta = parseMetadata(OpCall, http.StatusOK, h) }},
		{Name: "parseMetadata without async", Max: 6, Run: func() { sinkMeta = parseMetadata(OpCall, http.StatusOK, plain) }},
		{Name: "foldKey folded key", Max: 0, Run: func() { sinkKey = foldKey("charged_micro") }},
		{Name: "foldKey key to fold", Max: 1, Run: func() { sinkKey = foldKey("Charged_\u212aey_\u03a3") }},
	})
	require.NoError(t, buildErr)
	assert.NotNil(t, sinkWire)
	assert.Equal(t, m.CallRef, sinkMeta.CallRef)
	assert.Equal(t, "charged_key_\u03a3", sinkKey, "Σ, σ and ς fold to Σ, the least")
}

// allocMaxGrowth is the most a 1 MiB request may allocate: its one copy
// plus a quarter. Measured per run, on this tree / the after-A snapshot /
// the baseline: BytesBody 1,049,912 / 2,098,576 / 2,098,488 bytes and
// JSONBody 1,049,936 / 2,098,600 / 2,098,512 bytes. The second copy the
// older trees make is the token scan's string(body).
const allocMaxGrowth = allocBodyBytes + allocBodyBytes/4

// TestBuildCallLargeBodyHeapGrowth locks in that the credential scan reads a
// large body in place: building a request with a 1 MiB body allocates one
// copy of it, not a second one for the scan, and the scan still finds a
// token at the body's end.
func TestBuildCallLargeBodyHeapGrowth(t *testing.T) { //nolint:paralleltest // measures allocations
	testutil.SkipUnderRace(t)
	c, err := NewClient(allocToken)
	require.NoError(t, err)
	asBytes, asJSON := allocLargeCalls(t)
	leak := allocBody(allocBodyBytes)
	copy(leak[len(leak)-len(allocToken)-2:], allocToken)
	leaky := asBytes
	leaky.Body = BytesBody(jsonContentType, leak)
	_, err = c.buildCall(leaky)
	require.ErrorIs(t, err, ErrInvalidRequest, "the scan still reads the whole body")

	for _, tt := range []struct { //nolint:paralleltest // measures allocations
		name    string
		req     CallRequest
		refused bool
	}{
		{name: "BytesBody", req: asBytes},
		{name: "JSONBody", req: asJSON},
		{name: "BytesBody holding the token", req: leaky, refused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var sink *wire
			var buildErr error
			grown := allocMinGrowth(func() { sink, buildErr = c.buildCall(tt.req) })
			t.Logf("%d bytes per run (%.3f body copies)", grown, float64(grown)/allocBodyBytes)
			assert.LessOrEqual(t, grown, uint64(allocMaxGrowth), "one copy of the body, not two")
			if tt.refused {
				require.ErrorIs(t, buildErr, ErrInvalidRequest)
				assert.Nil(t, sink)
				return
			}
			require.NoError(t, buildErr)
			require.NotNil(t, sink)
			assert.True(t, bytes.HasPrefix(sink.body, []byte(`{"blob":"`)))
		})
	}
}
