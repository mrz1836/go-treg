package treg

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMetadataValid(t *testing.T) {
	t.Parallel()
	ref := strings.Repeat("a1", 16)
	const async = `{"id_from":"id","poll":{"endpoint":"replicate.predictions.get","param":{"in":"pathParams","name":"id"}},` +
		`"status":{"path":"status","success":["succeeded"],"failure":["failed"]},"result":{"path":"output"},"interval":2,` +
		`"max_age":300,"terminal_on_submission":true}`
	h := http.Header{
		"X-Treg-Call-Id":             {ref + ":r0:v"},
		"X-Treg-Cost-Micro":          {"20000"},
		"X-Treg-Original-Cost-Micro": {"24000"},
		"X-Treg-Idempotent-Replay":   {"true"},
		"X-Treg-Error":               {"1"},
		"X-Treg-Served-By":           {"hunter.people.email.find"},
		"X-Treg-Served-Via":          {"overflow:relay-1"},
		"X-Treg-Upstream-Failed":     {"status_code=40501"},
		"X-Treg-Cache":               {"hit"},
		"X-Treg-Age":                 {"3600"},
		"X-Treg-Fetched-At":          {"2026-10-01T12:00:00Z"},
		"Retry-After":                {"2"},
		"x-treg-async":               {async}, // an injected transport may set non-canonical keys
	}
	m := parseMetadata(OpCall, 200, h)
	assert.Equal(t, OpCall, m.Op)
	assert.Equal(t, 200, m.StatusCode)
	assert.Equal(t, ref+":r0:v", m.CallRef)
	require.NotNil(t, m.CostMicro)
	assert.Equal(t, MicroUSD(20000), *m.CostMicro)
	require.NotNil(t, m.OriginalCostMicro)
	assert.Equal(t, MicroUSD(24000), *m.OriginalCostMicro)
	assert.True(t, m.Replayed)
	assert.True(t, m.GatewayMarked)
	assert.Equal(t, EndpointID("hunter.people.email.find"), m.ServedBy)
	assert.Equal(t, "overflow:relay-1", m.ServedVia)
	assert.True(t, m.UpstreamFailed)
	assert.True(t, m.Cache.Hit)
	require.NotNil(t, m.Cache.Age)
	assert.Equal(t, time.Hour, *m.Cache.Age)
	require.NotNil(t, m.Cache.FetchedAt)
	assert.Equal(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), *m.Cache.FetchedAt)
	assert.True(t, m.HasRetryAfter)
	assert.Equal(t, 2*time.Second, m.RetryAfter)
	require.NotNil(t, m.Async)
	assert.Equal(t, "id", m.Async.IDFrom)
	assert.Equal(t, EndpointID("replicate.predictions.get"), m.Async.Poll.Endpoint)
	require.NotNil(t, m.Async.Poll.Param)
	assert.Equal(t, AsyncParam{In: "pathParams", Name: "id"}, *m.Async.Poll.Param)
	assert.Equal(t, "status", m.Async.Status.Path)
	assert.Equal(t, []json.RawMessage{json.RawMessage(`"succeeded"`)}, m.Async.Status.Success)
	assert.Equal(t, "output", m.Async.Result.Path)
	assert.Equal(t, Decimal("2"), m.Async.Interval)
	assert.Equal(t, Some(int64(300)), m.Async.MaxAge)
	assert.True(t, m.Async.TerminalOnSubmission)
	assert.JSONEq(t, async, string(m.Async.Raw))
	assert.Empty(t, m.Unparsed)
}

func TestParseMetadataBad(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header http.Header
		check  func(t *testing.T, m Metadata)
	}{
		{"call id short", http.Header{"X-Treg-Call-Id": {"abc"}}, func(t *testing.T, m Metadata) { t.Helper(); assert.Empty(t, m.CallRef) }},
		{"call id upper", http.Header{"X-Treg-Call-Id": {strings.Repeat("A", 32)}}, func(t *testing.T, m Metadata) { t.Helper(); assert.Empty(t, m.CallRef) }},
		{"call id bad suffix", http.Header{"X-Treg-Call-Id": {strings.Repeat("a", 32) + ":x1"}}, nil},
		{"call id trailing colon", http.Header{"X-Treg-Call-Id": {strings.Repeat("a", 32) + ":"}}, nil},
		{"call id too many suffixes", http.Header{"X-Treg-Call-Id": {strings.Repeat("a", 32) + ":r0:r1:r2:r3:v"}}, nil},
		{"call id repeated", http.Header{"X-Treg-Call-Id": {strings.Repeat("a", 32), strings.Repeat("b", 32)}}, nil},
		{"call id split keys", http.Header{"X-Treg-Call-Id": {strings.Repeat("a", 32)}, "x-treg-call-id": {strings.Repeat("b", 32)}}, nil},
		{"cost negative", http.Header{"X-Treg-Cost-Micro": {"-1"}}, func(t *testing.T, m Metadata) { t.Helper(); assert.Nil(t, m.CostMicro) }},
		{"cost decimal", http.Header{"X-Treg-Cost-Micro": {"1.5"}}, nil},
		{"cost overflow", http.Header{"X-Treg-Cost-Micro": {"9223372036854775808"}}, nil},
		{"cost empty", http.Header{"X-Treg-Cost-Micro": {""}}, nil},
		{"original cost text", http.Header{"X-Treg-Original-Cost-Micro": {"free"}}, nil},
		{"replay other", http.Header{"X-Treg-Idempotent-Replay": {"yes"}}, func(t *testing.T, m Metadata) { t.Helper(); assert.False(t, m.Replayed) }},
		{"error other", http.Header{"X-Treg-Error": {"true"}}, func(t *testing.T, m Metadata) { t.Helper(); assert.False(t, m.GatewayMarked) }},
		{"served by url", http.Header{"X-Treg-Served-By": {"https://evil.example/x"}}, nil},
		{"served via other", http.Header{"X-Treg-Served-Via": {"direct"}}, nil},
		{"cache other", http.Header{"X-Treg-Cache": {"miss"}}, nil},
		{"age text", http.Header{"X-Treg-Age": {"1h"}}, nil},
		{"fetched at other", http.Header{"X-Treg-Fetched-At": {"yesterday"}}, nil},
		{"retry after date", http.Header{"Retry-After": {"Wed, 21 Oct 2015 07:28:00 GMT"}}, func(t *testing.T, m Metadata) { t.Helper(); assert.False(t, m.HasRetryAfter) }},
		{"async not object", http.Header{"X-Treg-Async": {`[1]`}}, func(t *testing.T, m Metadata) { t.Helper(); assert.Nil(t, m.Async) }},
		{"async invalid", http.Header{"X-Treg-Async": {`{"id_from":`}}, nil},
		{"async oversized", http.Header{"X-Treg-Async": {`{"x":"` + strings.Repeat("a", 16<<10) + `"}`}}, nil},
		{"upstream failed oversized", http.Header{"X-Treg-Upstream-Failed": {strings.Repeat("a", 1025)}}, func(t *testing.T, m Metadata) { t.Helper(); assert.False(t, m.UpstreamFailed) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := parseMetadata(OpCall, 200, tt.header)
			require.Len(t, m.Unparsed, 1)
			for k := range tt.header {
				assert.True(t, strings.EqualFold(k, m.Unparsed[0]))
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func TestParseMetadataLenientAsync(t *testing.T) {
	t.Parallel()
	m := parseMetadata(OpCall, 202, http.Header{"X-Treg-Async": {
		`{"task_id":"t-1","poll":{"endpoint":7,"url_from":"urls.get","url_hosts":["api.example.com"]},"status":"weird","interval":"2"}`,
	}})
	require.NotNil(t, m.Async)
	assert.Equal(t, "t-1", m.Async.TaskID)
	assert.Empty(t, m.Async.Poll.Endpoint, "a member of another type is left unset")
	assert.Equal(t, "urls.get", m.Async.Poll.URLFrom)
	assert.Equal(t, []string{"api.example.com"}, m.Async.Poll.URLHosts)
	assert.Empty(t, m.Async.Status.Path)
	assert.Empty(t, m.Async.Interval, "a quoted interval is not a number")
	assert.Empty(t, m.Unparsed)
}

func TestMetadataPrintsSafeFieldsOnly(t *testing.T) {
	t.Parallel()
	m := Metadata{
		Op: OpCall, StatusCode: 402, CallRef: strings.Repeat("c", 32), CostMicro: new(MicroUSD(5)),
		ServedBy: "secret.vendor.MARKER", ServedVia: "overflow:MARKER2",
		Async:    &AsyncDescriptor{Poll: AsyncPoll{URLFrom: "MARKER3"}, Raw: json.RawMessage(`{"MARKER4":1}`)},
		Unparsed: []string{"X-Treg-Age", "bad name\r\nwith MARKER5"},
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%x", "%q"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			out := fmt.Sprintf(verb, m)
			for _, marker := range []string{"MARKER", "MARKER2", "MARKER3", "MARKER4", "MARKER5"} {
				assert.NotContains(t, out, marker, verb)
			}
		})
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "MARKER")
	assert.Contains(t, string(out), `"cost_micro":5`)
	assert.Contains(t, m.String(), "servedBy: true")
	assert.Contains(t, m.String(), "unparsed: X-Treg-Age")
	assert.Equal(t, "treg.Metadata{op: , status: 0}", Metadata{Op: "bad op!"}.String())
}

func FuzzMetadata(f *testing.F) {
	f.Add("X-Treg-Call-Id", strings.Repeat("a", 32)+":r1")
	f.Add("X-Treg-Async", `{"id_from":"id","poll":{"endpoint":"a.b"}}`)
	f.Add("X-Treg-Cost-Micro", "123")
	f.Add("Retry-After", "99999999999")
	f.Add("X-Treg-Fetched-At", "2026-01-01T00:00:00.123456789+02:00")
	f.Fuzz(func(t *testing.T, name, value string) {
		m := parseMetadata(OpCall, 200, http.Header{name: {value}})
		if m.CallRef != "" {
			assert.True(t, validCallRef(m.CallRef))
		}
		if m.ServedBy != "" {
			assert.Empty(t, checkCatalogID(string(m.ServedBy)))
		}
		if m.CostMicro != nil {
			assert.GreaterOrEqual(t, int64(*m.CostMicro), int64(0))
		}
		assert.LessOrEqual(t, len(m.Unparsed), 1)
		_ = m.String()
		_, err := json.Marshal(m)
		require.NoError(t, err)
	})
}
