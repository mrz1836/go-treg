package treg_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// findQuery is the query of every Find a test opens.
const findQuery = "clone a voice"

// ndjsonHeader is the response header of an NDJSON answer.
func ndjsonHeader() http.Header { return http.Header{"Content-Type": {"application/x-ndjson"}} }

// findLines returns the lines of the synthetic Find stream, without their
// newlines.
func findLines(tb testing.TB) []string {
	tb.Helper()
	return strings.Split(strings.TrimSuffix(synthetic(tb, "catalog/find.ndjson"), "\n"), "\n")
}

// drainFind ranges over the stream's events once and returns every event
// and every error it yielded.
func drainFind(s *treg.FindStream) ([]treg.FindEvent, []error) {
	var events []treg.FindEvent
	var errs []error
	for ev, err := range s.Events() {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		events = append(events, ev)
	}
	return events, errs
}

// findOver returns a CatalogClient whose requests are answered in memory with
// status, header and body, of undeclared length.
func findOver(tb testing.TB, status int, header http.Header, body io.ReadCloser, opts ...treg.Option) *treg.CatalogClient {
	tb.Helper()
	return newCatalog(tb, testutil.TransportWith(status, header, -1, body), opts...)
}

// openFind opens a Find answered in memory with a 200 NDJSON body.
func openFind(t *testing.T, body io.ReadCloser) *treg.FindStream {
	t.Helper()
	s, err := findOver(t, http.StatusOK, ndjsonHeader(), body).Find(t.Context(), treg.FindRequest{Query: findQuery})
	require.NoError(t, err)
	require.NotNil(t, s)
	return s
}

// paddedEvent returns a valid event line of exactly size bytes, without a
// newline.
func paddedEvent(size int) string {
	head, tail := `{"event": "candidates", "candidates": [], "pad": "`, `"}`
	return head + strings.Repeat("a", size-len(head)-len(tail)) + tail
}

// assertFindEvents checks the events of the synthetic Find stream.
func assertFindEvents(t *testing.T, lines []string, events []treg.FindEvent) {
	t.Helper()
	require.Len(t, events, len(lines))
	for i, ev := range events {
		assertSameBytes(t, lines[i], ev.Raw, "event %d keeps its line", i)
		assert.True(t, ev.Event.Known())
	}
	first := events[0]
	assert.Equal(t, treg.FindCandidates, first.Event)
	assert.Equal(t, []treg.FindCandidate{
		{ID: cloneID, Platform: "voice-gen", Provider: "acmevoice"},
		{ID: "examplespeech.tts.create", Platform: "voice-gen", Provider: "examplespeech", Units: json.RawMessage(`{"chars": 1000}`)},
	}, first.Candidates)
	assert.Equal(t, treg.Optional[string]{}, first.Verdict, "verdict")
	assert.Equal(t, treg.Optional[treg.Decimal]{}, first.High, "high")
	assert.Nil(t, first.Rows)

	second := events[1]
	assert.Equal(t, treg.FindCandidates, second.Event)
	assert.Equal(t, []treg.FindCandidate{{ID: "sampleseo.url.metrics", Platform: "web-seo", Provider: "sampleseo"}},
		second.Candidates)

	assertJudged(t, events[2])
}

// assertJudged checks the judged event of the synthetic Find stream.
func assertJudged(t *testing.T, ev treg.FindEvent) {
	t.Helper()
	assert.Equal(t, treg.FindJudged, ev.Event)
	assert.Nil(t, ev.Candidates)
	assert.Equal(t, treg.Some("strong"), ev.Verdict, "verdict")
	assert.Equal(t, treg.Some(""), ev.Named, "named")
	assert.Equal(t, treg.Some[int64](45), ev.Read, "read")
	assert.Equal(t, treg.Some(treg.Decimal("0.7")), ev.High, "high")
	assert.Equal(t, treg.Some("synthetic-judge"), ev.Engine, "engine")
	assert.Equal(t, treg.Optional[string]{}, ev.Reason, "reason")
	require.Len(t, ev.Rows, 2)

	top := ev.Rows[0]
	assert.Equal(t, treg.EndpointID(cloneID), top.ID)
	assert.Equal(t, "Clone a voice", top.Name)
	assert.Equal(t, "acmevoice", top.Provider)
	assert.Equal(t, "Acme Voice", top.ProviderDisplay)
	assert.Equal(t, "voice.clone", top.Capability)
	assert.Equal(t, "Clone a voice from a short sample.", top.CapabilityDescription)
	assert.Equal(t, "voice-gen", top.Platform)
	assert.Equal(t, "Voice generation", top.PlatformLabel)
	cost, ok := top.Cost.Get()
	require.True(t, ok)
	assertSameBytes(t, `{"type": "per_call", "usd": 0.006667, "per": 1, "settle": {"on": "success"}}`, cost.Raw)
	assert.Equal(t, treg.CostPerCall, cost.Type)
	assert.Equal(t, treg.Some(treg.Decimal("0.006667")), cost.USD, "cost.usd")
	assert.Equal(t, treg.Some(treg.Decimal("0.94")), top.P, "p")
	assert.Equal(t, treg.Some("job"), top.FitFrom, "fit_from")
	assert.Nil(t, top.ChildrenHidden)

	routed := ev.Rows[1]
	assert.Equal(t, treg.EndpointID("acmevoice.voice.any"), routed.ID)
	assert.Equal(t, treg.Null[treg.Cost](), routed.Cost, "cost")
	assert.Equal(t, treg.Some(treg.Decimal("0.51")), routed.P, "p")
	assert.Equal(t, treg.Some("name"), routed.FitFrom, "fit_from")
	assertSameBytes(t, "2", routed.ChildrenHidden)
}

// assertFindWire checks the one GET of a Find.
func assertFindWire(t *testing.T, r testutil.Request, query string) {
	t.Helper()
	assert.Equal(t, http.MethodGet, r.Method)
	assert.Equal(t, "/catalog/find", r.Path)
	assert.Equal(t, query, r.RawQuery)
	assert.Equal(t, "application/x-ndjson", r.Header.Get("Accept"))
	assert.NotContains(t, r.Header, "X-Treg-Token")
	assert.NotContains(t, r.Header, "X-Treg-Org")
	assert.Empty(t, r.Header.Get("Authorization"))
	assert.Empty(t, r.Body)
}

func TestFindOverServers(t *testing.T) {
	t.Parallel()
	content := synthetic(t, "catalog/find.ndjson")
	lines := findLines(t)
	// From a standalone CatalogClient, and from the Catalog of a Client with
	// a token and an org: neither sends them.
	catalogs := []struct {
		name    string
		catalog func(tb testing.TB, srv *testutil.Server) *treg.CatalogClient
	}{
		{"standalone", func(tb testing.TB, srv *testutil.Server) *treg.CatalogClient {
			tb.Helper()
			return newCatalog(tb, srv)
		}},
		{"client_catalog", func(tb testing.TB, srv *testutil.Server) *treg.CatalogClient {
			tb.Helper()
			return newClient(tb, srv, treg.WithOrg(testOrg)).Catalog()
		}},
	}
	for _, proto := range testutil.Protocols() {
		for _, from := range catalogs {
			t.Run(proto.Name()+"_"+from.name, func(t *testing.T) {
				t.Parallel()
				srv := testutil.NewServerProto(t, proto, testutil.Serve(testutil.Step{Header: ndjsonHeader(), Body: content}))
				s, err := from.catalog(t, srv).Find(t.Context(), treg.FindRequest{Query: findQuery, Platform: "voice-gen"})
				require.NoError(t, err)
				assert.Equal(t, treg.OpCatalogFind, s.Meta().Op)
				assert.Equal(t, http.StatusOK, s.Meta().StatusCode)
				events, errs := drainFind(s)
				require.Empty(t, errs)
				assertFindEvents(t, lines, events)
				require.NoError(t, s.Close())
				r := testutil.OnlyRequest(t, srv)
				assertFindWire(t, r, "platform=voice-gen&q=clone+a+voice")
				assert.Equal(t, string(proto), r.Proto)
			})
		}
	}
}

func TestFindWithoutPlatform(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Header: ndjsonHeader(), Body: synthetic(t, "catalog/find.ndjson")})
	s, err := newCatalog(t, script).Find(t.Context(), treg.FindRequest{Query: "tts & more"})
	require.NoError(t, err)
	events, errs := drainFind(s)
	require.Empty(t, errs)
	assert.Len(t, events, 3)
	assertFindWire(t, testutil.OnlyRequest(t, script), "q=tts+%26+more")
}

func TestFindLineFraming(t *testing.T) {
	t.Parallel()
	content := synthetic(t, "catalog/find.ndjson")
	lines := findLines(t)
	crlf := strings.ReplaceAll(content, "\n", "\r\n")
	cases := []struct {
		name string
		body string
		step int
	}{
		{"lf_in_7_byte_chunks", content, 7},
		{"one_byte_chunks", content, 1},
		{"crlf_in_5_byte_chunks", crlf, 5},
		{"blank_lines", "\n\r\n" + strings.Join(lines, "\n  \n\n\r\n") + "\n\n", 3},
		{"no_final_newline", strings.TrimSuffix(content, "\n"), 0},
		{"crlf_no_final_newline", strings.TrimSuffix(crlf, "\r\n"), 11},
		{"surrounding_whitespace", " \t" + strings.Join(lines, "  \n\t ") + "  ", 0},
	}
	for _, fc := range cases {
		t.Run(fc.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody(fc.body)
			body.Step = fc.step
			s := openFind(t, body)
			events, errs := drainFind(s)
			require.Empty(t, errs)
			assertFindEvents(t, lines, events)
			assert.Positive(t, body.Closes(), "the stream closed when the loop ended")
		})
	}
}

func TestFindSingleUse(t *testing.T) {
	t.Parallel()
	t.Run("after_full_range", func(t *testing.T) {
		t.Parallel()
		body := testutil.NewBody(synthetic(t, "catalog/find.ndjson"))
		s := openFind(t, body)
		events, errs := drainFind(s)
		require.Empty(t, errs)
		require.Len(t, events, 3)
		for range 2 {
			events, errs = drainFind(s)
			assert.Empty(t, events)
			require.Len(t, errs, 1, "one error, then the loop ends")
			requireValidation(t, errs[0], "FindStream", "stream_consumed")
		}
		require.NoError(t, s.Close())
	})

	t.Run("two_sequences", func(t *testing.T) {
		t.Parallel()
		s := openFind(t, testutil.NewBody(synthetic(t, "catalog/find.ndjson")))
		first, second := s.Events(), s.Events()
		n := 0
		for _, err := range first {
			require.NoError(t, err)
			n++
		}
		assert.Equal(t, 3, n)
		var errs []error
		for ev, err := range second {
			assert.Equal(t, treg.FindEvent{}, ev)
			errs = append(errs, err)
		}
		require.Len(t, errs, 1)
		requireValidation(t, errs[0], "FindStream", "stream_consumed")
	})
}

func TestFindEarlyBreak(t *testing.T) {
	t.Parallel()
	body := testutil.NewBody(synthetic(t, "catalog/find.ndjson"))
	s := openFind(t, body)
	var got []treg.FindEvent
	for ev, err := range s.Events() {
		require.NoError(t, err)
		got = append(got, ev)
		if len(got) == 1 {
			break
		}
	}
	require.Len(t, got, 1)
	assert.Equal(t, treg.FindCandidates, got[0].Event)
	assert.Positive(t, body.Closes(), "an early break closes the stream")
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")
	events, errs := drainFind(s)
	assert.Empty(t, events)
	require.Len(t, errs, 1)
	requireValidation(t, errs[0], "FindStream", "stream_consumed")
}

func TestFindCloseBeforeEvents(t *testing.T) {
	t.Parallel()
	body := testutil.NewBody(synthetic(t, "catalog/find.ndjson"))
	s := openFind(t, body)
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")
	assert.Positive(t, body.Closes())
	assert.Equal(t, treg.OpCatalogFind, s.Meta().Op, "Meta outlives Close")
	events, errs := drainFind(s)
	assert.Empty(t, events)
	require.Len(t, errs, 1)
	requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureTransport, Reason: "closed"})
}

func TestFindCloseUnblocksLoop(t *testing.T) {
	t.Parallel()
	body := testutil.NewBlockingBody(findLines(t)[0] + "\n")
	s := openFind(t, body)
	first := make(chan struct{})
	done := make(chan []error, 1)
	go func() {
		var errs []error
		n := 0
		for _, err := range s.Events() {
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if n++; n == 1 {
				close(first)
			}
		}
		done <- errs
	}()
	select {
	case <-first:
	case errs := <-done:
		require.Failf(t, "the loop ended before its first event", "%d errors", len(errs))
	}
	require.NoError(t, s.Close())
	errs := <-done
	require.Len(t, errs, 1)
	requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureTransport, Reason: "closed"})
	assert.Positive(t, body.Closes())
}

func TestFindStreamUninitialized(t *testing.T) {
	t.Parallel()
	for _, sc := range []struct {
		name string
		s    *treg.FindStream
	}{
		{"nil", nil},
		{"zero", &treg.FindStream{}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			requireConfigValidation(t, sc.s.Close(), "stream", "not_initialized")
			events, errs := drainFind(sc.s)
			assert.Empty(t, events)
			require.Len(t, errs, 1)
			requireConfigValidation(t, errs[0], "stream", "not_initialized")
			assert.Equal(t, treg.Metadata{}, sc.s.Meta())
		})
	}
}

func TestFindLineBound(t *testing.T) {
	t.Parallel()
	lines := findLines(t)

	t.Run("endless_line", func(t *testing.T) {
		t.Parallel()
		body := &testutil.EndlessBody{}
		s := openFind(t, body)
		events, errs := drainFind(s)
		assert.Empty(t, events)
		require.Len(t, errs, 1)
		requireCallError(t, errs[0], callErrorWant{
			Op: treg.OpCatalogFind, Kind: treg.FailureResponseTooLarge, Reason: "line_too_long", Status: http.StatusOK,
		})
		assert.Positive(t, body.Closes())
		assert.Less(t, body.BytesRead(), int64(2<<20), "reading stops soon after 1 MiB")
	})

	t.Run("line_over_1_MiB", func(t *testing.T) {
		t.Parallel()
		body := testutil.NewBody(lines[0] + "\n" + paddedEvent(1<<20+1) + "\n" + lines[2] + "\n")
		s := openFind(t, body)
		events, errs := drainFind(s)
		require.Len(t, events, 1)
		assertSameBytes(t, lines[0], events[0].Raw)
		require.Len(t, errs, 1)
		requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureResponseTooLarge, Reason: "line_too_long"})
		assert.Positive(t, body.Closes())
	})

	t.Run("line_of_exactly_1_MiB", func(t *testing.T) {
		t.Parallel()
		long := paddedEvent(1 << 20)
		s := openFind(t, testutil.NewBody(long+"\n"+lines[2]+"\n"))
		events, errs := drainFind(s)
		require.Empty(t, errs)
		require.Len(t, events, 2)
		assert.Len(t, events[0].Raw, 1<<20)
		assert.Equal(t, treg.FindCandidates, events[0].Event)
		assert.Equal(t, treg.FindJudged, events[1].Event)
	})
}

func TestFindTotalBound(t *testing.T) {
	t.Parallel()
	const total = 16 << 20
	line := `{"event": "candidates", "candidates": []}`
	line += strings.Repeat(" ", 4095-len(line)) + "\n"
	require.Len(t, line, 4096)

	t.Run("exactly_16_MiB", func(t *testing.T) {
		t.Parallel()
		rb := testutil.NewRepeatBody(line)
		s := openFind(t, testutil.ReadCloser{Reader: io.LimitReader(rb, total), Closer: rb})
		events, errs := drainFind(s)
		require.Empty(t, errs)
		assert.Len(t, events, total/len(line))
		assert.Positive(t, rb.Closes())
	})

	t.Run("over_16_MiB", func(t *testing.T) {
		t.Parallel()
		rb := testutil.NewRepeatBody(line)
		s := openFind(t, rb)
		events, errs := drainFind(s)
		assert.Len(t, events, total/len(line), "every line within 16 MiB is yielded")
		require.Len(t, errs, 1)
		requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureResponseTooLarge})
		assert.Positive(t, rb.Closes())
	})
}

func TestFindMalformedLine(t *testing.T) {
	t.Parallel()
	lines := findLines(t)
	cases := []struct {
		name, line, reason string
	}{
		{"not_json", "not json", "invalid_json"},
		{"array", `["event", "judged"]`, "invalid_json"},
		{"string", `"judged"`, "invalid_json"},
		{"broken_object", `{"event": judged}`, "invalid_json"},
		{"truncated", `{"event": "judged"`, "truncated_json"},
		{"two_values", `{"event": "judged"} {"event": "judged"}`, "trailing_data"},
		{"duplicate_key", `{"event": "judged", "event": "candidates"}`, "duplicate_key"},
		{"case_folded_duplicate", `{"event": "judged", "Event": "candidates"}`, "duplicate_key"},
		{"nested_duplicate", `{"event": "judged", "rows": [{"id": "a.b", "id": "c.d"}]}`, "duplicate_key"},
		// One level past the strict check's bound, which equals encoding/json's,
		// so the JSON check refuses it first.
		{"too_deep", `{"event": "judged", "x": ` + strings.Repeat("[", 10000) + strings.Repeat("]", 10000) + `}`, "invalid_json"},
		{"quoted_number", `{"event": "judged", "read": "45"}`, "decode"},
		{"quoted_decimal", `{"event": "judged", "high": "0.7"}`, "decode"},
		{"wrong_type", `{"event": "candidates", "candidates": {"id": "a.b"}}`, "decode"},
	}
	for _, mc := range cases {
		t.Run(mc.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody(lines[0] + "\n" + mc.line + "\n" + lines[2] + "\n")
			s := openFind(t, body)
			events, errs := drainFind(s)
			require.Len(t, events, 1, "the events before the bad line arrive, none after")
			require.Len(t, errs, 1)
			requireCallError(t, errs[0], callErrorWant{
				Op: treg.OpCatalogFind, Kind: treg.FailureMalformedResponse, Reason: mc.reason, Status: http.StatusOK,
			})
			assert.Positive(t, body.Closes())
		})
	}
}

func TestFindRawIslandDuplicatesAccepted(t *testing.T) {
	t.Parallel()
	line := `{"event": "candidates", "candidates": [], "raw": {"k": 1, "k": 2}, "detail": {"x": 1, "X": 2}}`
	s := openFind(t, testutil.NewBody(line+"\n"))
	events, errs := drainFind(s)
	require.Empty(t, errs)
	require.Len(t, events, 1)
	assertSameBytes(t, line, events[0].Raw)
}

func TestFindUnknownEvent(t *testing.T) {
	t.Parallel()
	lines := findLines(t)
	progress := `{"event": "progress", "done": 3, "of": 9}`
	empty := `{"event": "judged", "verdict": "none", "reason": "no_match", "rows": []}`
	s := openFind(t, testutil.NewBody(lines[0]+"\n"+progress+"\n"+empty+"\n"))
	events, errs := drainFind(s)
	require.Empty(t, errs)
	require.Len(t, events, 3)
	assert.Equal(t, treg.FindEventType("progress"), events[1].Event)
	assert.False(t, events[1].Event.Known())
	assertSameBytes(t, progress, events[1].Raw, "unmodeled members stay in Raw")
	assert.Nil(t, events[1].Candidates)
	assert.True(t, events[2].Event.Known())
	assert.Equal(t, treg.Some("none"), events[2].Verdict, "verdict")
	assert.Equal(t, treg.Some("no_match"), events[2].Reason, "reason")
	assert.Equal(t, treg.Optional[string]{}, events[2].Named, "named")
	assert.NotNil(t, events[2].Rows)
	assert.Empty(t, events[2].Rows)
}

func TestFindStatusErrors(t *testing.T) {
	t.Parallel()
	gateway := gatewayHeader()
	cases := []struct {
		name    string
		status  int
		header  http.Header
		body    string
		is      error
		kind    treg.FailureKind
		reason  string
		refusal string
	}{
		{
			"rate_limited", http.StatusTooManyRequests, gateway, `{"detail":{"error":"public_demo_rate_limited"}}`,
			treg.ErrGatewayRefusal, treg.FailureHTTPStatus, "", "public_demo_rate_limited",
		},
		{
			"rate_limited_unmarked", http.StatusTooManyRequests, nil, `{"detail":{"error":"public_demo_rate_limited"}}`,
			treg.ErrHTTPStatus, treg.FailureHTTPStatus, "", "public_demo_rate_limited",
		},
		{
			"unknown_platform", http.StatusNotFound, nil, `{"detail":"unknown platform 'nope'"}`,
			treg.ErrHTTPStatus, treg.FailureHTTPStatus, "", "",
		},
		{
			"provider_paused", http.StatusServiceUnavailable, nil,
			`{"detail":{"error":"provider_paused","message":"Paused for synthetic maintenance."}}`,
			treg.ErrHTTPStatus, treg.FailureHTTPStatus, "", "provider_paused",
		},
		{
			"redirect", http.StatusFound,
			http.Header{"Location": {"https://example.com/elsewhere"}},
			"",
			treg.ErrUnexpectedResponse, treg.FailureUnexpectedResponse, "redirect", "",
		},
		{
			"html", http.StatusOK,
			http.Header{"Content-Type": {"text/html"}},
			"<html></html>",
			treg.ErrUnexpectedResponse, treg.FailureUnexpectedResponse, "content_type", "",
		},
		{
			"repeated_content_type", http.StatusOK,
			http.Header{"Content-Type": {"application/x-ndjson", "text/plain"}},
			"{}",
			treg.ErrUnexpectedResponse, treg.FailureUnexpectedResponse, "content_type", "",
		},
		{
			"content_encoding", http.StatusOK,
			http.Header{"Content-Type": {"application/x-ndjson"}, "Content-Encoding": {"gzip"}},
			"\x1f\x8b", treg.ErrUnexpectedResponse, treg.FailureUnexpectedResponse, "content_encoding", "",
		},
	}
	for _, sc := range cases {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody(sc.body)
			s, err := findOver(t, sc.status, sc.header, body).Find(t.Context(), treg.FindRequest{Query: findQuery})
			assert.Nil(t, s)
			ce := requireCallError(t, err, callErrorWant{
				Is: sc.is, Op: treg.OpCatalogFind, Kind: sc.kind, Reason: sc.reason, Status: sc.status,
			})
			if sc.refusal == "" {
				assert.True(t, ce.Refusal == nil || ce.Refusal.Code == "", "no refusal code")
			} else {
				require.NotNil(t, ce.Refusal)
				assert.Equal(t, sc.refusal, ce.Refusal.Code)
				assert.True(t, ce.Refusal.Known())
			}
			assert.NotContains(t, err.Error(), "nope")
			assert.Positive(t, body.Closes(), "Find closes a response it refuses")
		})
	}
}

func TestFindAcceptedContentTypes(t *testing.T) {
	t.Parallel()
	content := synthetic(t, "catalog/find.ndjson")
	for _, ct := range []struct {
		name   string
		header http.Header
	}{
		{"x_ndjson_with_charset", http.Header{"Content-Type": {"application/x-ndjson; charset=utf-8"}}},
		{"ndjson", http.Header{"Content-Type": {"application/ndjson"}}},
		{"jsonl", http.Header{"Content-Type": {"application/jsonl"}}},
		{"x_jsonlines", http.Header{"Content-Type": {"application/x-jsonlines"}}},
		{"json", http.Header{"Content-Type": {"application/json"}}},
		{"upper_case", http.Header{"Content-Type": {"APPLICATION/X-NDJSON"}}},
		{"absent", http.Header{}},
	} {
		t.Run(ct.name, func(t *testing.T) {
			t.Parallel()
			s, err := findOver(t, http.StatusOK, ct.header, testutil.NewBody(content)).Find(t.Context(), treg.FindRequest{Query: findQuery})
			require.NoError(t, err, "Content-Type %q", ct.header.Get("Content-Type"))
			events, errs := drainFind(s)
			require.Empty(t, errs)
			assert.Len(t, events, 3)
		})
	}
}

func TestFindValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		req    treg.FindRequest
		field  string
		reason string
	}{
		{"empty_query", treg.FindRequest{Platform: "voice-gen"}, "Query", "required"},
		{"query_too_long", treg.FindRequest{Query: strings.Repeat("q", 501)}, "Query", "too_long"},
		{"query_control", treg.FindRequest{Query: "clone\x00voice"}, "Query", "control_character"},
		{"query_newline", treg.FindRequest{Query: "clone\nvoice"}, "Query", "control_character"},
		{"query_tab", treg.FindRequest{Query: "clone\tvoice"}, "Query", "control_character"},
		{"query_del", treg.FindRequest{Query: "clone\x7fvoice"}, "Query", "control_character"},
		{"query_c1", treg.FindRequest{Query: "clone\u009bvoice"}, "Query", "control_character"},
		{"query_invalid_utf8", treg.FindRequest{Query: "clone\xc3voice"}, "Query", "invalid_character"},
		{"platform_slash", treg.FindRequest{Query: findQuery, Platform: "a/b"}, "Platform", "invalid_character"},
		{"platform_escaped", treg.FindRequest{Query: findQuery, Platform: "%2F"}, "Platform", "invalid_character"},
		{"platform_space", treg.FindRequest{Query: findQuery, Platform: "a b"}, "Platform", "invalid_character"},
		{"platform_newline", treg.FindRequest{Query: findQuery, Platform: "a\nb"}, "Platform", "control_character"},
		{"platform_dot_dot", treg.FindRequest{Query: findQuery, Platform: ".."}, "Platform", "dot_segment"},
		{"platform_too_long", treg.FindRequest{Query: findQuery, Platform: strings.Repeat("p", 129)}, "Platform", "too_long"},
	}
	for _, vc := range cases {
		t.Run(vc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			s, err := newCatalog(t, script).Find(t.Context(), vc.req)
			assert.Nil(t, s)
			requireValidation(t, err, vc.field, vc.reason)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestFindRefusedLocally(t *testing.T) {
	t.Parallel()
	// The catalogs a case may call Find on: one sending to its script, and
	// the unusable ones, which send nowhere.
	standalone := func(tb testing.TB, script *testutil.Script) *treg.CatalogClient {
		tb.Helper()
		return newCatalog(tb, script)
	}
	ofClient := func(tb testing.TB, script *testutil.Script) *treg.CatalogClient {
		tb.Helper()
		return newClient(tb, script).Catalog()
	}
	unusable := func(cat *treg.CatalogClient) func(testing.TB, *testutil.Script) *treg.CatalogClient {
		return func(testing.TB, *testutil.Script) *treg.CatalogClient { return cat }
	}
	// The contexts a case may call Find with.
	own := (*testing.T).Context
	nilCtx := func(*testing.T) context.Context { return nil }
	canceled := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}
	invalid := func(field, reason string) func(*testing.T, error) {
		return func(t *testing.T, err error) {
			t.Helper()
			requireValidation(t, err, field, reason)
		}
	}
	notInitialized := func(t *testing.T, err error) {
		t.Helper()
		requireConfigValidation(t, err, "client", "not_initialized")
	}
	cases := []struct {
		name    string
		catalog func(testing.TB, *testutil.Script) *treg.CatalogClient
		ctx     func(*testing.T) context.Context
		req     treg.FindRequest
		check   func(*testing.T, error)
	}{
		{"nil_ctx", standalone, nilCtx, treg.FindRequest{Query: findQuery}, invalid("ctx", "nil")},
		{"nil_catalog", unusable(nil), own, treg.FindRequest{Query: findQuery}, notInitialized},
		{"zero_catalog", unusable(&treg.CatalogClient{}), own, treg.FindRequest{Query: findQuery}, notInitialized},
		{"nil_client_catalog", unusable((*treg.Client)(nil).Catalog()), own, treg.FindRequest{Query: findQuery}, notInitialized},
		{
			"token_in_query", ofClient, own,
			treg.FindRequest{Query: "find " + testToken},
			invalid("Request", "credential_in_request"),
		},
		{
			"token_in_platform", ofClient, own,
			treg.FindRequest{Query: findQuery, Platform: testToken},
			invalid("Request", "credential_in_request"),
		},
		{"canceled_ctx", standalone, canceled, treg.FindRequest{Query: findQuery}, func(t *testing.T, err error) {
			t.Helper()
			require.ErrorIs(t, err, context.Canceled)
		}},
	}
	for _, rc := range cases {
		t.Run(rc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			s, err := rc.catalog(t, script).Find(rc.ctx(t), rc.req)
			assert.Nil(t, s)
			rc.check(t, err)
			assert.Zero(t, script.Calls(), "nothing was sent")
		})
	}
}

func TestFindQueryAccepted(t *testing.T) {
	t.Parallel()
	for _, q := range []struct{ name, query, raw string }{
		{"query_max", strings.Repeat("q", 500), "q=" + strings.Repeat("q", 500)},
		{"query_utf8", "voix française", "q=voix+fran%C3%A7aise"},
		{"query_utf8_max", strings.Repeat("é", 250), "q=" + strings.Repeat("%C3%A9", 250)},
	} {
		t.Run(q.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{Header: ndjsonHeader(), Body: synthetic(t, "catalog/find.ndjson")})
			s, err := newCatalog(t, script).Find(t.Context(), treg.FindRequest{Query: q.query})
			require.NoError(t, err)
			require.NoError(t, s.Close())
			assert.Equal(t, q.raw, testutil.OnlyRequest(t, script).RawQuery)
		})
	}
}

// The Find tests below that wait on deadlines, or that once counted
// goroutines, run in testing/synctest bubbles over in-memory transports: the
// deadlines elapse on the bubble's fake clock, exactly, and every goroutine a
// call starts belongs to the bubble, which synctest.Test requires to have
// ended with the test. Their contexts derive from context.WithoutCancel, so
// that a goroutine left waiting on the caller's context stays blocked, and
// fails the test, rather than ending when the test's context does.

func TestFindCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody(findLines(t)[0] + "\n")
		cat := findOver(t, http.StatusOK, ndjsonHeader(), body)
		ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
		defer cancel()
		s, err := cat.Find(ctx, treg.FindRequest{Query: findQuery})
		require.NoError(t, err)
		var events []treg.FindEvent
		var errs []error
		for ev, evErr := range s.Events() {
			if evErr != nil {
				errs = append(errs, evErr)
				continue
			}
			events = append(events, ev)
			cancel() // the next read waits on the blocking body
		}
		require.Len(t, events, 1)
		require.Len(t, errs, 1)
		requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureCanceled, Status: http.StatusOK})
		require.NotErrorIs(t, errs[0], treg.ErrTimeout)
		assert.Positive(t, body.Closes(), "cancellation closes the body")
	})
}

func TestFindStreamTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody(findLines(t)[0] + "\n")
		cat := findOver(t, http.StatusOK, ndjsonHeader(), body, treg.WithStreamTimeout(100*time.Millisecond))
		start := time.Now()
		s, err := cat.Find(context.WithoutCancel(t.Context()), treg.FindRequest{Query: findQuery})
		require.NoError(t, err)
		events, errs := drainFind(s)
		assert.Equal(t, 100*time.Millisecond, time.Since(start), "the stream ends exactly at its timeout")
		require.Len(t, events, 1)
		require.Len(t, errs, 1)
		requireCallError(t, errs[0], callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureTimeout})
		require.ErrorIs(t, errs[0], context.DeadlineExceeded)
		assert.Positive(t, body.Closes(), "the stream timeout closes the body")
	})
}

func TestFindHeaderTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cat := newCatalog(t, testutil.RoundTripFunc(testutil.BlockUntilDone), treg.WithTimeout(50*time.Millisecond))
		start := time.Now()
		s, err := cat.Find(context.WithoutCancel(t.Context()), treg.FindRequest{Query: findQuery})
		assert.Equal(t, 50*time.Millisecond, time.Since(start), "the header wait ends exactly at the timeout")
		assert.Nil(t, s)
		requireCallError(t, err, callErrorWant{Op: treg.OpCatalogFind, Kind: treg.FailureTimeout, Reason: "header_timeout"})
	})
}
