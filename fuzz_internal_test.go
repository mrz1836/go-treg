package treg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fuzz targets below check properties of the SDK's decoders beyond "does
// not panic": what a successful decode guarantees, that accepted values
// round-trip, and that failures carry only fixed text. Seeds are small
// hand-written inputs plus the synthetic fixtures. Run one with, for example:
//
//	go test -run='^$' -fuzz='^FuzzCheckStrict$' -fuzztime=10s .

// fuzzFixtures returns every file under testdata/synthetic, and every line
// of its NDJSON files, as seeds.
func fuzzFixtures(f *testing.F) [][]byte {
	f.Helper()
	var seeds [][]byte
	err := filepath.WalkDir(filepath.Join("testdata", "synthetic"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) == ".md" {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // a fixture path from WalkDir
		if err != nil {
			return err
		}
		seeds = append(seeds, b)
		if filepath.Ext(path) == ".ndjson" {
			for line := range bytes.Lines(b) {
				seeds = append(seeds, bytes.TrimSpace(line))
			}
		}
		return nil
	})
	require.NoError(f, err)
	require.NotEmpty(f, seeds)
	return seeds
}

// fuzzFindLines returns the lines of the synthetic Find stream.
func fuzzFindLines(f *testing.F) [][]byte {
	f.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "synthetic", "catalog", "find.ndjson"))
	require.NoError(f, err)
	var lines [][]byte
	for line := range bytes.Lines(b) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			lines = append(lines, line)
		}
	}
	require.NotEmpty(f, lines)
	return lines
}

// fuzzNested returns n nested arrays: "[[...]]".
func fuzzNested(n int) []byte {
	return []byte(strings.Repeat("[", n) + strings.Repeat("]", n))
}

// fuzzRouted is a routed envelope with every _treg member set.
const fuzzRouted = `{"output":{"email":"ada@example.com","confidence":0.92},"raw":{"data":{"score":92}},` +
	`"_treg":{"served_by":"vendor.people.email.find","provider":"vendor","tier":"platform","outcome":"hit",` +
	`"tried":[{"endpoint_id":"other.people.email.find","provider":"other","outcome":"miss","status":200,` +
	`"charged_micro":0,"detail":{"x":1},"ignored_filters":null}],"charged_micro":10000,"reserved_micro":null,` +
	`"call_ref":"","async":null,"verification":{"verdict":"valid"},"advice":null,"ignored_filters":[],` +
	`"dropped":null,"capped":false,"merged_from":["a.b.c"]}}`

func FuzzRoutedUnmarshalJSON(f *testing.F) {
	for _, s := range []string{
		fuzzRouted,
		`{"output":{"email":null},"raw":null,"_treg":{"served_by":null,"provider":null,"tier":"platform","outcome":"miss","tried":[],"charged_micro":0}}`,
		`{"output":null,"_treg":{"outcome":"pending","charged_micro":null,"reserved_micro":24000,` +
			`"call_ref":"0123456789abcdef0123456789abcdef:p1","async":{"task_id":"t-1","poll":{"endpoint":"a.b.c"},"status":{}}}}`,
		`{"_treg":{}}`, `{"_treg":[]}`, `{"output":{}}`, `{"_treg":{"outcome":"hit","Outcome":"miss"}}`,
		`{"_treg":{"charged_micro":1.5}}`, `{"_treg":{"tried":[{"status":"200"}]}}`, `{"_treg":{}} x`, `[]`, `null`, ``,
		`{"_treg":{},"_treg":{"outcome":"weak"}}`, `{"_TREG":{}}`, `{"_treg":{"outcome":"hit"}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		var direct Routed[json.RawMessage]
		directErr := direct.UnmarshalJSON(in) // never panics, whatever the input
		var r Routed[json.RawMessage]
		if err := json.Unmarshal(in, &r); err != nil {
			return
		}
		require.NoError(t, directErr, "what encoding/json accepts, a direct call accepts")
		assert.Equal(t, r, direct)
		out, err := json.Marshal(r)
		require.NoError(t, err, "a decoded envelope re-encodes")

		// The encoding is canonical: decoding it again gives the same value.
		var again Routed[json.RawMessage]
		require.NoError(t, again.UnmarshalJSON(out))
		out2, err := json.Marshal(again)
		require.NoError(t, err)
		assert.Equal(t, string(out), string(out2))
		assert.Equal(t, r.Treg.Outcome, again.Treg.Outcome)
	})
}

// fuzzFindReasons are the fixed reasons a bad Find line can fail with.
func fuzzFindReasons() []string {
	return []string{reasonInvalidJSON, reasonTruncatedJSON, reasonTrailingData, reasonDecode, reasonDuplicateKey, reasonTooDeep}
}

// fuzzFindCauses are the fixed causes a bad Find line can fail with.
func fuzzFindCauses() []error {
	return []error{errInvalidJSON, errTrailingData, errDecode, errDuplicateKey, errTooDeep}
}

func FuzzDecodeFindEvent(f *testing.F) {
	for _, line := range fuzzFindLines(f) {
		f.Add(line)
	}
	for _, s := range []string{
		`{"event":"judged","rows":[]}`, `{"event":"candidates","candidates":[{"id":"a.b.c","units":{"chars":1}}]}`,
		`{"event":"later_kind","extra":{"k":1,"k":2}}`, `{"event":"judged","event":"candidates"}`,
		`{"event":"judged","EVENT":"x"}`, `{"event":"judged","read":1.5}`, `{"event":"judged","high":"0.5"}`,
		`{"input":{"a":1,"a":2},"event":"judged"}`, `{"event":"judged"} x`, `{"event":"judged"`, `[]`, `"x"`, ``,
		`{"event":"judged","rows":[{"id":"a.b.c","cost":{"type":"per_call","usd":0.01,"settle":{"on":"x","on":"y"}},"p":0.5}]}`,
		"{\"event\":\"judged\",\"named\":\"\xff\"}",
	} {
		f.Add([]byte(s))
	}
	f.Add(append(append([]byte(`{"event":"judged","x":`), fuzzNested(maxStrictDepth)...), '}'))
	f.Fuzz(func(t *testing.T, line []byte) {
		ev, df := decodeFindEvent(line)
		if df != nil {
			assert.Equal(t, FindEvent{}, ev)
			assert.Equal(t, FailureMalformedResponse, df.kind)
			assert.Contains(t, fuzzFindReasons(), df.reason)
			assert.Contains(t, fuzzFindCauses(), df.cause, "the cause is a fixed sentinel, never a JSON error")
			// The error a stream yields for the line quotes none of it.
			ce := newCallError(OpCatalogFind, df.kind, df.reason, 200, df.cause)
			assert.Equal(t, "treg: catalog_find: malformed_response (HTTP 200): "+df.reason, ce.Error())
			require.ErrorIs(t, ce, ErrMalformedResponse)
			require.ErrorIs(t, ce, df.cause)
			return
		}
		assert.True(t, json.Valid(line))
		assert.True(t, isJSONObject(line))
		require.NoError(t, checkStrict(line, rawIsland), "an accepted line has no repeated keys")
		assert.Equal(t, string(line), string(ev.Raw), "Raw is the line as received")

		// A decoded event re-encodes, and decodes again to the same encoding.
		out, err := json.Marshal(ev)
		require.NoError(t, err)
		again, df := decodeFindEvent(out)
		require.Nil(t, df)
		assert.Equal(t, ev.Event, again.Event)
		assert.Len(t, again.Candidates, len(ev.Candidates))
		assert.Len(t, again.Rows, len(ev.Rows))
		out2, err := json.Marshal(again)
		require.NoError(t, err)
		assert.Equal(t, string(out), string(out2))
	})
}

func FuzzParseRefusal(f *testing.F) {
	ref := "0123456789abcdef0123456789abcdef"
	for _, s := range []string{
		`{"detail":{"error":"insufficient_balance","balance_micro":0,"estimated_cost_micro":24000}}`,
		`{"error":"route_max_cost","max_cost_micro":20000,"charged_micro":0}`,
		`{"treg_saturated":true}`, `{"treg_saturated":false}`, `{"detail":"Idempotency key reused with a different body"}`,
		`{"detail":{"error":"idempotency_response_lost","call_id":"` + ref + `:r1:v","charged_micro":5}}`,
		`{"detail":{"error":"x","call_id":"` + strings.ToUpper(ref) + `"}}`,
		`{"error":"Not_A_Code"}`, `{"error":"provider_said_no"}`, `{"detail":{"error":"x"},"error":"tag_blocked"}`,
		`{"detail":[],"error":"org_rate_limit","retry_after_s":1.5}`, `{"retry_after_s":-1}`, `{"retry_after_s":1e300}`,
		`{"balance_micro":"5"}`, `{"balance_micro":-9223372036854775808}`, `{"balance_micro":9223372036854775808}`,
		`{"error":"` + strings.Repeat("a", maxCodeBytes) + `"}`, `{"error":"` + strings.Repeat("a", maxCodeBytes+1) + `"}`,
		`{"detail":{"error":"daily_cap_reached","error":"route_failed"}}`, `[]`, `null`, ``, `{`, "\xff",
		`{"error":"insufficient_balance","pad":"` + strings.Repeat("x", maxRefusalBytes) + `"}`,
	} {
		f.Add([]byte(s))
	}
	fieldRE := regexp.MustCompile(`^(code: [a-z0-9_]{1,64}|saturated: true|` +
		`(balanceMicro|estimatedCostMicro|maxCostMicro|chargedMicro): -?[0-9]{1,19}|retryAfterSeconds: [0-9]{1,10}|` +
		`callRef: [0-9a-f]{32}(:(v|[rp][0-9]{1,5})){0,4})$`)
	f.Fuzz(func(t *testing.T, body []byte) {
		r := parseRefusal(body)
		if r == nil {
			return
		}
		assert.NotEqual(t, Refusal{}, *r, "an empty refusal is reported as none")
		assert.True(t, r.Code == "" || validCode(r.Code), "Code is empty or a fixed code")
		assert.True(t, r.CallRef == "" || validCallRef(r.CallRef))
		if r.RetryAfterSeconds != nil {
			assert.GreaterOrEqual(t, *r.RetryAfterSeconds, int64(0))
		}

		fuzzRefusalText(t, fieldRE, r)

		var printed refusalJSON
		b, err := r.MarshalJSON()
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &printed))
		assert.True(t, printed.Code == "" || printed.Code == "refused" || knownRefusalCode(printed.Code))

		// A CallError with this refusal prints the code only when known.
		ce := &CallError{Kind: FailureHTTPStatus, Op: OpCall, StatusCode: 402, Refusal: r}
		want := "treg: call: http_status (HTTP 402)"
		switch {
		case r.Code == "":
		case r.Known():
			want += ": " + r.Code
		default:
			want += ": refused"
		}
		assert.Equal(t, want, ce.Error())
	})
}

// fuzzRefusalText checks that r prints only fixed words, known codes,
// integers and a call ref, every field matching fieldRE: no input text.
func fuzzRefusalText(t *testing.T, fieldRE *regexp.Regexp, r *Refusal) {
	t.Helper()
	text := r.String()
	require.True(t, strings.HasPrefix(text, "treg.Refusal{") && strings.HasSuffix(text, "}"), text)
	if inner := strings.TrimSuffix(strings.TrimPrefix(text, "treg.Refusal{"), "}"); inner != "" {
		for field := range strings.SplitSeq(inner, ", ") {
			require.Regexp(t, fieldRE, field)
			if code, ok := strings.CutPrefix(field, "code: "); ok {
				assert.True(t, code == "refused" || knownRefusalCode(code), "only a known code prints")
			}
		}
	}
	assert.Equal(t, text, fmt.Sprintf("%+v", *r))
	assert.Equal(t, text, fmt.Sprintf("%#v", r))
}

// fuzzOptional checks that decoding in into an Optional[T] keeps the
// absent, null and value states consistent with the input.
func fuzzOptional[T any](t *testing.T, in []byte) {
	t.Helper()
	isNull := bytes.Equal(bytes.TrimSpace(in), []byte("null"))
	var plain T
	plainErr := json.Unmarshal(in, &plain)

	// Called directly, as encoding/json calls it for a present member.
	var o Optional[T]
	err := o.UnmarshalJSON(in)
	v, ok := o.Get()
	switch {
	case isNull:
		require.NoError(t, err)
		assert.True(t, o.IsNull())
		assert.True(t, o.IsPresent())
		assert.False(t, ok)
	case plainErr == nil:
		require.NoError(t, err)
		assert.True(t, ok)
		assert.False(t, o.IsNull())
		assert.Equal(t, plain, v)
		out, merr := json.Marshal(o)
		require.NoError(t, merr)
		var back T
		require.NoError(t, json.Unmarshal(out, &back), "a value re-encodes as T")
		assert.Equal(t, plain, back)
	default:
		require.Error(t, err)
		assert.True(t, o.IsZero(), "a failed decode leaves the Optional absent")
	}

	// As an object member, through encoding/json.
	if !json.Valid(in) {
		return
	}
	var w struct {
		V Optional[T] `json:"v"`
	}
	werr := json.Unmarshal(append(append([]byte(`{"v":`), in...), '}'), &w)
	switch {
	case isNull:
		require.NoError(t, werr)
		assert.True(t, w.V.IsNull())
	case plainErr == nil:
		require.NoError(t, werr)
		got, present := w.V.Get()
		assert.True(t, present)
		assert.Equal(t, plain, got)
	default:
		require.Error(t, werr)
	}
	var absent struct {
		V Optional[T] `json:"v"`
	}
	require.NoError(t, json.Unmarshal(append(append([]byte(`{"w":`), in...), '}'), &absent))
	assert.True(t, absent.V.IsZero(), "a missing member stays absent")
}

func FuzzOptionalUnmarshalJSON(f *testing.F) {
	for _, s := range []string{
		`null`, ` null `, "\tnull\n", `nul`, `NULL`, `"text"`, `""`, `"null"`, "\"\xff\"", `"é"`, `0`, `-12`,
		`9223372036854775807`, `9223372036854775808`, `1.5`, `1e3`, `-0`, `true`, `{}`, `[]`, ``, ` `, `"a" "b"`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzOptional[string](t, in)
		fuzzOptional[int64](t, in)
		fuzzOptional[MicroUSD](t, in)
		fuzzOptional[Decimal](t, in)
	})
}

func FuzzMicroUSDUnmarshalJSON(f *testing.F) {
	for _, s := range []string{
		`0`, `-0`, `20000`, `-1`, `9223372036854775807`, `-9223372036854775808`, `9223372036854775808`,
		`-9223372036854775809`, `00`, `01`, `1.0`, `1e3`, `"5"`, `null`, ` 7 `, `+1`, `-`, ``, " 12", `１`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		var direct MicroUSD
		derr := direct.UnmarshalJSON(in)
		if derr != nil {
			require.ErrorIs(t, derr, errNotInteger)
			assert.Zero(t, direct, "a refused value leaves the target unchanged")
		}
		var m MicroUSD
		if err := json.Unmarshal(in, &m); err != nil {
			return
		}
		require.NoError(t, derr, "what encoding/json accepts, a direct call accepts")
		assert.Equal(t, m, direct)

		// Only a JSON integer is accepted, and it encodes back as itself.
		lit := string(bytes.Trim(in, " \t\r\n"))
		n, err := strconv.ParseInt(lit, 10, 64)
		require.NoError(t, err)
		assert.Equal(t, int64(m), n)
		out, err := json.Marshal(m)
		require.NoError(t, err)
		if lit == "-0" {
			assert.Equal(t, "0", string(out))
		} else {
			assert.Equal(t, lit, string(out))
		}
		var back MicroUSD
		require.NoError(t, json.Unmarshal(out, &back))
		assert.Equal(t, m, back)

		// The decimal form round-trips too.
		usd, err := ParseDecimalUSD(m.DecimalUSD())
		require.NoError(t, err)
		assert.Equal(t, m, usd)
	})
}

func FuzzDecimalUnmarshalJSON(f *testing.F) {
	for _, s := range []string{
		`0.006667`, `12.5`, `0`, `-0`, `1e-7`, `1E+400`, `-1.5e10`, `"0.02"`, `null`, `.5`, `01`, ` 3 `, `NaN`,
		`-`, `0x10`, `1.`, `true`, `[1]`, ``, " 1",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		var direct Decimal
		if err := direct.UnmarshalJSON(in); err != nil {
			require.ErrorIs(t, err, errNotNumber)
			assert.Empty(t, direct, "a refused value leaves the target unchanged")
		} else {
			assert.True(t, isJSONNumber([]byte(direct)))
			assert.Equal(t, string(bytes.TrimSpace(in)), string(direct), "the number is kept as sent")
			out, err := json.Marshal(direct)
			require.NoError(t, err)
			assert.Equal(t, string(direct), string(out), "and encodes as the same text")
			var back Decimal
			require.NoError(t, json.Unmarshal(out, &back))
			assert.Equal(t, direct, back)
		}
		var d Decimal
		if err := json.Unmarshal(in, &d); err == nil {
			assert.Equal(t, direct, d, "encoding/json and a direct call agree")
		}
	})
}

func FuzzObjectMembers(f *testing.F) {
	for _, s := range []string{
		`{}`, ` { } `, `{"a":1}`, `{"a" : 1 , "b" : [1 ,2 ] }`, `{"a":1,"a":{"b":2}}`, `{"a":{"x":1},"a":2,"A":3}`,
		`{"\u0061":1,"a":2}`, `{"a":1,"\u0061":2}`, `{"\"":1,"\\":2,"\/":3}`, `{"\ud800":1,"\udc00x":2}`,
		"{\"\xff\":1,\"\xef\xbf\xbd\":2}", "{\"\xe2\x82\":1}", `{"é":1,"\u00e9":2}`, `{"":1,"":2}`,
		`{"poll":{"endpoint":"a.b.c","param":{"in":"pathParams","name":"id"}},"status":{"success":["ok",1]}}`,
		`{"detail":{"error":"x","balance_micro":5},"error":"y"}`, `{"a":"}","b":"{\"c\":1}"}`, `{"a":[{"a":1}],"b":{"a":2}}`,
		`[]`, `null`, `"x"`, `1`, ``, `{`, `{"a":1`, `{"a":1}x`, `{"a":1,}`, `{"a" 1}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		o, err := objectMembers(in)
		trimmed := bytes.TrimSpace(in)
		var want map[string]json.RawMessage
		if !isJSONObject(trimmed) || json.Unmarshal(trimmed, &want) != nil {
			require.ErrorIs(t, err, errNotObject)
			assert.Equal(t, jsonObject{}, o)
			return
		}
		require.NoError(t, err, "a JSON object has members")
		assert.Equal(t, string(trimmed), string(o.b))
		for _, m := range o.members {
			assert.True(t, json.Valid(m.key) && json.Valid(m.value), "a member is a key and a value")
		}
		for key, raw := range want {
			got, ok := o.member(key)
			require.True(t, ok, "every key of the decoded map is a member")
			assert.Equal(t, string(raw), string(got), "the value is the map's: the last for a repeated key")
			for _, other := range []string{strings.ToUpper(key), strings.ToLower(key), key + "x", "x" + key} {
				_, inMap := want[other]
				_, isMember := o.member(other)
				assert.Equal(t, inMap, isMember, "a key matches exactly, as a map lookup does")
			}
		}
		_, ok := o.member("\x00absent")
		assert.False(t, ok)
	})
}

// fuzzFrame is one open object or array of fuzzStrictModel's walk.
type fuzzFrame struct {
	object, check, wantKey bool
	key                    string
	keys                   []string
}

// fuzzStrictModel is checkStrict's model for valid JSON, built on
// encoding/json's tokenizer, which decodes every key as encoding/json does.
// dup is errDuplicateKey once an object, outside the members skip names,
// repeats a key as encoding/json matches keys to fields: by
// strings.EqualFold. mismatch is the first pair of keys of one object, in
// any member, that foldKey folds alike although strings.EqualFold tells
// them apart, or the reverse.
type fuzzStrictModel struct {
	skip     func(string) bool
	stack    []*fuzzFrame
	dup      error
	mismatch []string
}

// fuzzStrictReference walks b, which must be valid JSON, with the model.
func fuzzStrictReference(b []byte, skip func(string) bool) (mismatch []string, dup error) {
	m := &fuzzStrictModel{skip: skip}
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return m.mismatch, m.dup
		}
		m.token(tok)
	}
}

// top returns the innermost open frame, or nil.
func (m *fuzzStrictModel) top() *fuzzFrame {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}

// token advances the walk by one token.
func (m *fuzzStrictModel) token(tok json.Token) {
	top := m.top()
	if key, isKey := tok.(string); isKey && top != nil && top.object && top.wantKey {
		m.key(top, key)
		return
	}
	switch tok {
	case json.Delim('{'), json.Delim('['):
		check := top == nil || (top.check && (!top.object || m.skip == nil || !m.skip(top.key)))
		m.stack = append(m.stack, &fuzzFrame{object: tok == json.Delim('{'), check: check, wantKey: true})
		return
	case json.Delim('}'), json.Delim(']'):
		m.stack = m.stack[:len(m.stack)-1]
	}
	if top = m.top(); top != nil && top.object {
		top.wantKey = true // a member's value ended
	}
}

// key records an object key and compares it with the object's earlier keys.
func (m *fuzzStrictModel) key(top *fuzzFrame, key string) {
	for _, prev := range top.keys {
		same := strings.EqualFold(prev, key)
		if top.check && m.dup == nil && same {
			m.dup = errDuplicateKey
		}
		if (foldKey(prev) == foldKey(key)) != same && m.mismatch == nil {
			m.mismatch = []string{prev, key}
		}
	}
	top.keys = append(top.keys, key)
	top.key, top.wantKey = key, false
}

func FuzzCheckStrict(f *testing.F) {
	for _, seed := range fuzzFixtures(f) {
		f.Add(seed)
	}
	for _, s := range []string{
		`{}`, `[]`, `{"a":1,"A":2}`, `{"a":{"b":1,"b":2}}`, `{"input":{"x":1,"x":2}}`, `{"a":[{"input":{"x":1,"x":2}}]}`,
		`{"a":1,"a":2}`, `{"k":1,"\u212a":2}`, `{"s":1,"\u017F":2}`, `{"S":1,"ſ":2}`, `{"ﬅ":1,"ﬆ":2}`, `{"İ":1,"i":2}`,
		`[1,{"k":true,"K":false}]`, `{"a":"\"}"}`, `{"ı":1,"I":2}`, `{"ß":1,"ẞ":2}`, `{"σ":1,"ς":2,"Σ":3}`,
		`{"\u2126":1,"ω":2}`, `{"ǅ":1,"ǆ":2}`, `{"µ":1,"Μ":2}`, "{\"\xff\":1,\"\ufffd\":2}", `{"input":{"ſ":1,"S":2}}`,
		`1 2`, `{"a":1}x`, `{"a":}`, `--`, "\"\x01\"", `"\u00zz"`, `{"a" 1}`, `[1,]`, `tru`, ``, ` `,
	} {
		f.Add([]byte(s))
	}
	f.Add(fuzzNested(maxStrictDepth))
	f.Add(fuzzNested(maxStrictDepth + 1))
	f.Fuzz(func(t *testing.T, in []byte) {
		err := checkStrict(in, rawIsland) // never panics, whatever the input
		_ = checkStrict(in, nil)

		// The guarded entry point validates first: a pass is valid JSON.
		df := strictFailure(in, rawIsland)
		if df == nil {
			assert.True(t, json.Valid(in), "a strict pass is valid JSON")
			require.NoError(t, err)
		} else {
			assert.Equal(t, FailureMalformedResponse, df.kind)
			assert.Contains(t, fuzzFindReasons(), df.reason)
			assert.Contains(t, fuzzFindCauses(), df.cause)
		}
		if !json.Valid(in) {
			return // outside checkStrict's precondition
		}
		mismatch, want := fuzzStrictReference(in, rawIsland)
		assert.Equal(t, want, err, "checkStrict agrees with the reference walk")
		assert.Nil(t, mismatch, "foldKey folds two keys alike exactly when strings.EqualFold matches them")
		if errors.Is(err, errDuplicateKey) {
			assert.Equal(t, reasonDuplicateKey, df.reason)
		}
	})
}
