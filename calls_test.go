package treg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// callsReads returns every read of CallsService, each with a valid request.
func callsReads() []namedRead[treg.CallsService] {
	return []namedRead[treg.CallsService]{
		{"List", bind(treg.CallsService.List, treg.ListCallsRequest{Limit: 10}).erased()},
		{"Get", bind(treg.CallsService.Get, treg.GetCallRequest{Ref: authReadCallRef}).erased()},
		{"Result", bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9003"}).erased()},
	}
}

func TestCallsListDecodesFixture(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, proto, synthetic(t, "calls/list.json"),
				bind(treg.CallsService.List, treg.ListCallsRequest{Limit: 3}))
			assert.Equal(t, string(proto), r.Proto)
			assertAuthReadWire(t, r, "/calls?limit=3", "")
			assert.Equal(t, treg.OpListCalls, res.Meta.Op)
			assert.Equal(t, http.StatusOK, res.Meta.StatusCode)
			assert.Equal(t, treg.CaptureDisabled, res.Captured.State())
			require.Len(t, res.Value, 3)
			assertCallsRowFull(t, res.Value[0])
			assertCallsRowPending(t, res.Value[1])
			assertCallsRowRefused(t, res.Value[2])
		})
	}
}

// assertCallsRowFull checks the fixture's settled row, whose fields all hold
// values.
func assertCallsRowFull(t *testing.T, row treg.CallRecord) {
	t.Helper()
	assert.Equal(t, int64(9003), row.ID)
	assert.Equal(t, authReadCallRef, row.CallRef)
	assert.Equal(t, authReadEmailMarker, row.UserEmail)
	assert.Equal(t, "example-search", row.ToolName)
	assert.Equal(t, http.MethodPost, row.Method)
	assert.Equal(t, "/call/example.web.search", row.Path)
	assert.Equal(t, int64(200), row.StatusCode)
	assert.Equal(t, "call", row.Kind)
	assert.Equal(t, "sdk", row.Client)
	assert.Equal(t, treg.Some[int64](12), row.APIKeyID)
	assert.Equal(t, treg.Some("ci-runner"), row.APIKeyName)
	assert.Equal(t, treg.Some("trg_ci_"), row.APIKeyPrefix)
	assert.Equal(t, treg.Some[treg.EndpointID]("example.web.search"), row.EndpointID)
	assert.Equal(t, treg.Some("examplecorp"), row.Provider)
	assert.Equal(t, treg.Some("platform"), row.CredentialTier)
	assert.Equal(t, treg.Some(false), row.Cached, "false is a value, not absent")
	assert.Equal(t, treg.Some(true), row.HasResult)
	assert.Equal(t, treg.Some(treg.MicroUSD(2000)), row.CostEstimatedMicro)
	assert.Equal(t, treg.Some(treg.MicroUSD(1500)), row.CostObservedMicro)
	assert.Equal(t, treg.Some(treg.MicroUSD(1500)), row.CostChargedMicro)
	assert.Equal(t, treg.Some[int64](412), row.DurationMS)
	assert.Equal(t, treg.Some[int64](2048), row.ResponseBytes)
	assert.Equal(t, treg.Some("a1b2c3d4e5f60718"), row.ParamsHash)
	assert.Equal(t, treg.Null[string](), row.RefusedBy)
	assert.Equal(t, treg.Some("customer"), row.BudgetDim)
	assert.Equal(t, treg.Some("c-001"), row.BudgetVal)
	assert.Equal(t, treg.Some(map[string]string{"customer": "c-001", "env": "staging"}), row.Tags)
	assert.Equal(t, "2026-10-16T12:34:56Z", row.CreatedAt)
	assert.Equal(t, "null", string(row.AsyncTask), "an explicit null stays raw")
}

// assertCallsRowPending checks the fixture's async row: nulls while the task
// is pending, and the task kept raw.
func assertCallsRowPending(t *testing.T, row treg.CallRecord) {
	t.Helper()
	assert.Equal(t, int64(9002), row.ID)
	assert.Equal(t, authReadChildRef, row.CallRef)
	assert.Equal(t, int64(202), row.StatusCode)
	assert.Equal(t, treg.Null[string](), row.APIKeyName)
	assert.Equal(t, treg.Null[string](), row.APIKeyPrefix)
	assert.Equal(t, treg.Null[string](), row.ParamsHash)
	assert.Equal(t, treg.Null[string](), row.RefusedBy)
	assert.Equal(t, treg.Null[int64](), row.APIKeyID)
	assert.Equal(t, treg.Null[treg.MicroUSD](), row.CostObservedMicro)
	assert.Equal(t, treg.Null[treg.MicroUSD](), row.CostChargedMicro, "null while the async task is pending")
	assert.Equal(t, treg.Some(treg.MicroUSD(3000)), row.CostEstimatedMicro)
	assert.Equal(t, treg.Null[int64](), row.DurationMS)
	assert.Equal(t, treg.Null[int64](), row.ResponseBytes)
	assert.Equal(t, treg.Some(true), row.Cached)
	assert.Equal(t, treg.Some(false), row.HasResult)
	assert.Equal(t, treg.Some(""), row.BudgetDim, "an empty string is a value, not null")
	assert.Equal(t, treg.Null[map[string]string](), row.Tags)
	assertExactJSON(t, `{"task_id": "tsk_example_01", "state": "pending", "poll_after_s": 5}`, row.AsyncTask)
}

// assertCallsRowRefused checks the fixture's refused row: no call ref, no
// cost, and the refusing control.
func assertCallsRowRefused(t *testing.T, row treg.CallRecord) {
	t.Helper()
	assert.Equal(t, int64(9001), row.ID)
	assert.Empty(t, row.CallRef)
	assert.Equal(t, int64(402), row.StatusCode)
	assert.Equal(t, treg.Some("tag_budget"), row.RefusedBy)
	assert.Equal(t, treg.Null[treg.EndpointID](), row.EndpointID)
	assert.Equal(t, treg.Null[string](), row.Provider)
	assert.Equal(t, treg.Null[string](), row.CredentialTier)
	assert.Equal(t, treg.Null[treg.MicroUSD](), row.CostEstimatedMicro)
	assert.Equal(t, treg.Null[treg.MicroUSD](), row.CostChargedMicro)
	assert.Equal(t, treg.Some[int64](0), row.ResponseBytes, "zero is a value, not absent")
	assert.Equal(t, treg.Some(map[string]string{}), row.Tags)
	assert.Equal(t, "null", string(row.AsyncTask))
}

func TestCallsListWire(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  treg.ListCallsRequest
		want string
	}{
		{"defaults omit every field", treg.ListCallsRequest{}, "/calls"},
		{"max limit", treg.ListCallsRequest{Limit: 500}, "/calls?limit=500"},
		{"min limit and days", treg.ListCallsRequest{Limit: 1, Days: 1}, "/calls?days=1&limit=1"},
		{"max days", treg.ListCallsRequest{Days: 365}, "/calls?days=365"},
		{"before id", treg.ListCallsRequest{BeforeID: 9001}, "/calls?before_id=9001"},
		{"api key id", treg.ListCallsRequest{APIKeyID: 12}, "/calls?api_key_id=12"},
		{"zero ids are omitted", treg.ListCallsRequest{Limit: 5, BeforeID: 0, APIKeyID: 0}, "/calls?limit=5"},
		{
			"every field",
			treg.ListCallsRequest{Limit: 25, Days: 7, BeforeID: 9001, APIKeyID: 12},
			"/calls?api_key_id=12&before_id=9001&days=7&limit=25",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, testutil.HTTP1, "[]", bind(treg.CallsService.List, tc.req))
			assertAuthReadWire(t, r, tc.want, "")
			assert.Empty(t, res.Value)
			assert.Equal(t, "go-treg", r.Header.Get("User-Agent"))
		})
	}
}

func TestCallsWireOrgHeader(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body, want string
		read             anyRead[treg.CallsService]
	}{
		{"List", "[]", "/calls", bind(treg.CallsService.List, treg.ListCallsRequest{}).erased()},
		{
			"Get", synthetic(t, "calls/detail.json"), "/calls/" + authReadChildRef,
			bind(treg.CallsService.Get, treg.GetCallRequest{Ref: authReadChildRef}).erased(),
		},
		{
			"Result", synthetic(t, "calls/result_stored.json"), "/calls/9003/result",
			bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9003"}).erased(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAuthReadWire(t, tc.body, tc.read, tc.want)
		})
	}
}

func TestCallPageNextRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		page treg.CallPage
	}{
		{"nil page", nil},
		{"empty page", treg.CallPage{}},
		// A last row without a positive id gives no cursor: BeforeID 0 is
		// omitted, so the next List would return the newest rows again.
		{"last id zero", treg.CallPage{{ID: 9003}, {}}},
		{"last id negative", treg.CallPage{{ID: 9003}, {ID: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prev := treg.ListCallsRequest{Limit: 50, Days: 7, BeforeID: 10}
			next, more := tc.page.NextRequest(prev)
			assert.False(t, more)
			assert.Equal(t, prev, next)
		})
	}
	t.Run("non-empty page", func(t *testing.T) {
		t.Parallel()
		prev := treg.ListCallsRequest{Limit: 3, Days: 30, BeforeID: 9100, APIKeyID: 12}
		page := treg.CallPage{{ID: 9003}, {ID: 9002}, {ID: 9001}}
		next, more := page.NextRequest(prev)
		require.True(t, more)
		assert.Equal(t, int64(9001), next.BeforeID, "the last row's id")
		assert.Equal(t, int64(3), next.Limit)
		assert.Equal(t, int64(30), next.Days)
		assert.Equal(t, prev.APIKeyID, next.APIKeyID)
		assert.Equal(t, int64(9100), prev.BeforeID, "prev is not mutated")
	})
	t.Run("next request is sent", func(t *testing.T) {
		t.Parallel()
		first, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "calls/list.json"),
			bind(treg.CallsService.List, treg.ListCallsRequest{Limit: 3}))
		next, more := first.Value.NextRequest(treg.ListCallsRequest{Limit: 3})
		require.True(t, more)
		second, r := authReadBody(t, testutil.HTTP1, "[]", bind(treg.CallsService.List, next))
		assertAuthReadWire(t, r, "/calls?before_id=9001&limit=3", "")
		last, more := second.Value.NextRequest(next)
		assert.False(t, more, "an empty page ends the walk")
		assert.Equal(t, next, last)
	})
}

func TestCallsGetDecodesFixture(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, proto, synthetic(t, "calls/detail.json"),
				bind(treg.CallsService.Get, treg.GetCallRequest{Ref: authReadChildRef}))
			assert.Equal(t, string(proto), r.Proto)
			assertAuthReadWire(t, r, "/calls/"+authReadChildRef, "")
			assert.Equal(t, treg.OpGetCall, res.Meta.Op)
			d := res.Value
			assert.Equal(t, int64(9002), d.Call.ID)
			assert.Equal(t, authReadChildRef, d.Call.CallRef)
			assert.Equal(t, treg.Some(treg.MicroUSD(1500)), d.Call.CostChargedMicro)
			assert.Equal(t, treg.Some(map[string]string{"team": "audio"}), d.Call.Tags)
			assert.Equal(t, treg.Optional[bool]{}, d.Call.HasResult, "absent from the detail row")
			assert.Equal(t, treg.Optional[string]{}, d.Call.ParamsHash)
			assert.Equal(t, treg.Optional[bool]{}, d.Call.Cached)
			assert.Nil(t, d.Call.AsyncTask, "an absent raw island stays nil")
			assertExactJSON(t, `{"task_id": "tsk_example_01", "state": "succeeded", "result": {"seconds": 12}}`,
				d.AsyncTask)
			assert.Equal(t, treg.Some(treg.MicroUSD(1500)), d.ChargedMicro)
			require.Len(t, d.Ledger, 3)
			assert.Equal(t, treg.LedgerEntry{
				Kind: "hold", AmountMicro: -3000, EndpointID: treg.Some[treg.EndpointID]("example.audio.tts"), CreatedAt: "2026-10-16T12:30:00Z",
			}, d.Ledger[0])
			assert.Equal(t, treg.MicroUSD(3000), d.Ledger[1].AmountMicro)
			assert.Equal(t, "charge", d.Ledger[2].Kind)
			assert.Equal(t, treg.MicroUSD(-1500), d.Ledger[2].AmountMicro)
			assert.Equal(t, treg.Null[treg.EndpointID](), d.Ledger[2].EndpointID)
		})
	}
}

func TestCallsResultDecodesFixture(t *testing.T) {
	t.Parallel()
	t.Run("stored", func(t *testing.T) {
		t.Parallel()
		res, r := authReadBody(t, testutil.HTTP2, synthetic(t, "calls/result_stored.json"),
			bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9003"}))
		assertAuthReadWire(t, r, "/calls/9003/result", "")
		assert.Equal(t, treg.OpCallResult, res.Meta.Op)
		sr := res.Value
		assert.Equal(t, int64(9003), sr.ID)
		assert.Equal(t, authReadCallRef, sr.CallRef)
		assert.Equal(t, treg.Some[treg.EndpointID]("example.web.search"), sr.EndpointID)
		assert.Equal(t, treg.Some("examplecorp"), sr.Provider)
		assert.Equal(t, treg.Some("platform"), sr.CredentialTier)
		assert.Equal(t, http.MethodPost, sr.Method)
		assert.Equal(t, int64(200), sr.StatusCode)
		assert.Equal(t, treg.Some(false), sr.Cached)
		assert.True(t, sr.Stored)
		assert.Equal(t, treg.Null[string](), sr.Note)
		req, ok := sr.Request.Get()
		require.True(t, ok)
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "https://example.com/MARKERPATH/v1/search", req.URL)
		assert.Equal(t, treg.Some(`{"q":"BODYMARKER request"}`), req.BodyText)
		resp, ok := sr.Response.Get()
		require.True(t, ok)
		assert.Equal(t, int64(200), resp.StatusCode)
		assert.Equal(t, treg.Some("application/json"), resp.MediaType)
		assert.Equal(t, treg.Some[int64](38), resp.SizeBytes)
		assert.Equal(t, treg.Some("2026-10-16T12:34:57Z"), resp.FetchedAt)
		assert.Equal(t, treg.Some("upstream"), resp.Origin)
		assert.Equal(t, "2", string(resp.Version))
		assert.Equal(t, treg.Some(`{"results":["BODYMARKER response"]}`), resp.BodyText)
	})
	t.Run("not stored", func(t *testing.T) {
		t.Parallel()
		res, r := authReadBody(t, testutil.HTTP1, synthetic(t, "calls/result_not_stored.json"),
			bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9001"}))
		assertAuthReadWire(t, r, "/calls/9001/result", "")
		sr := res.Value
		assert.Equal(t, int64(9001), sr.ID)
		assert.Empty(t, sr.CallRef)
		assert.False(t, sr.Stored)
		assert.Equal(t, treg.Some("results are stored only for metered platform 2xx calls"), sr.Note)
		assert.Equal(t, treg.Null[treg.StoredRequest](), sr.Request)
		assert.Equal(t, treg.Null[treg.StoredResponse](), sr.Response)
		assert.Equal(t, treg.Null[bool](), sr.Cached)
		assert.Equal(t, treg.Null[treg.EndpointID](), sr.EndpointID)
		assert.Equal(t, "treg.StoredResult{id: 9001, status: 402, stored: false}", sr.String())
	})
}

func TestCallsRefPathEscaping(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 256)
	tests := []struct {
		name, ref, want string
	}{
		{"hex ref", authReadCallRef, authReadCallRef},
		{"child suffix colon kept", authReadChildRef, authReadChildRef},
		{"numeric id", "9003", "9003"},
		{"slash escaped", "a/b", "a%2Fb"},
		{"query and fragment escaped", "a?b#c", "a%3Fb%23c"},
		{"percent escaped", "50%2Foff", "50%252Foff"},
		{"semicolon and comma escaped", "a;b,c", "a%3Bb%2Cc"},
		{"three dots are not a dot segment", "...", "..."},
		{"dot then slash escaped", "./x", ".%2Fx"},
		{"max length", long, long},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, r := authReadBody(t, testutil.HTTP1, synthetic(t, "calls/detail.json"),
				bind(treg.CallsService.Get, treg.GetCallRequest{Ref: tc.ref}))
			assertAuthReadWire(t, r, "/calls/"+tc.want, "")
			_, r = authReadBody(t, testutil.HTTP2, synthetic(t, "calls/result_stored.json"),
				bind(treg.CallsService.Result, treg.CallResultRequest{Ref: tc.ref}))
			assertAuthReadWire(t, r, "/calls/"+tc.want+"/result", "")
		})
	}
}

func TestCallsValidation(t *testing.T) {
	t.Parallel()
	list := func(req treg.ListCallsRequest) anyRead[treg.CallsService] {
		return bind(treg.CallsService.List, req).erased()
	}
	get := func(ref string) anyRead[treg.CallsService] {
		return bind(treg.CallsService.Get, treg.GetCallRequest{Ref: ref}).erased()
	}
	result := func(ref string) anyRead[treg.CallsService] {
		return bind(treg.CallsService.Result, treg.CallResultRequest{Ref: ref}).erased()
	}
	long := strings.Repeat("a", 257)
	refCases := []struct{ name, ref, reason string }{
		{"empty", "", "required"},
		{"too long", long, "too_long"},
		{"non-ASCII", "café", "invalid_character"},
		{"space", "a b", "invalid_character"},
		{"NUL", "a\x00b", "control_character"},
		{"newline", "a\nb", "control_character"},
		{"DEL", "a\x7fb", "control_character"},
		// A server or proxy resolves a dot segment: GET /calls/.. would be
		// GET /, and GET /calls/../result GET /result.
		{"dot", ".", "dot_segment"},
		{"dot dot", "..", "dot_segment"},
	}
	type callsCase struct {
		name          string
		read          anyRead[treg.CallsService]
		field, reason string
	}
	tests := make([]callsCase, 0, 8+2*len(refCases))
	tests = append(tests, []callsCase{
		{"List limit over", list(treg.ListCallsRequest{Limit: 501}), "Limit", "out_of_range"},
		{"List limit negative", list(treg.ListCallsRequest{Limit: -1}), "Limit", "out_of_range"},
		{"List days over", list(treg.ListCallsRequest{Days: 366}), "Days", "out_of_range"},
		{"List days negative", list(treg.ListCallsRequest{Days: -5}), "Days", "out_of_range"},
		{"List before id negative", list(treg.ListCallsRequest{BeforeID: -1}), "BeforeID", "out_of_range"},
		{"List api key id negative", list(treg.ListCallsRequest{APIKeyID: -3}), "APIKeyID", "out_of_range"},
		{"Get token in ref", get(testToken), "Request", "credential_in_request"},
		{"Result token in ref", result(testToken), "Request", "credential_in_request"},
	}...)
	for _, rc := range refCases {
		tests = append(tests,
			callsCase{"Get ref " + rc.name, get(rc.ref), "Ref", rc.reason},
			callsCase{"Result ref " + rc.name, result(rc.ref), "Ref", rc.reason},
		)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			requireValidation(t, tc.read.on(t.Context(), newClient(t, script)), tc.field, tc.reason)
			assert.Zero(t, script.Calls(), "nothing is sent")
		})
	}
}

func TestCallsZeroValue(t *testing.T) {
	t.Parallel()
	requireAuthReadsNotInitialized(t.Context(), t, callsReads())
}

func TestCallsOptionsAndContext(t *testing.T) {
	t.Parallel()
	requireAuthReadsRefuseOptions(t.Context(), t, callsReads())
}

func TestCallsStatusErrors(t *testing.T) {
	t.Parallel()
	list := bind(treg.CallsService.List, treg.ListCallsRequest{})
	t.Run("Get 404", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{Status: http.StatusNotFound, Body: `{"detail":"call not found"}`},
			bind(treg.CallsService.Get, treg.GetCallRequest{Ref: authReadCallRef}), treg.WithErrorBodies())
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpGetCall, Kind: treg.FailureHTTPStatus, Status: http.StatusNotFound, Marked: new(false),
		})
		assert.Nil(t, ce.Refusal)
		assertExactJSON(t, `{"detail":"call not found"}`, ce.Body())
		assert.Equal(t, "treg: get_call: http_status (HTTP 404)", err.Error())
	})
	t.Run("List 401", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{Status: http.StatusUnauthorized, Body: `{"detail":"not authenticated"}`}, list)
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpListCalls, Kind: treg.FailureHTTPStatus, Status: http.StatusUnauthorized, Marked: new(false),
		})
		assert.Nil(t, ce.Body(), "error bodies are off by default")
		assert.NotContains(t, err.Error(), "not authenticated")
	})
	t.Run("Result 403 marked", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{Status: http.StatusForbidden, Header: gatewayHeader(), Body: authReadAdminOnly},
			bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9003"}))
		requireCallError(t, err, callErrorWant{
			Op: treg.OpCallResult, Kind: treg.FailureHTTPStatus, Status: http.StatusForbidden, Marked: new(true),
		})
		assert.Equal(t, "treg: call_result: http_status (HTTP 403, gateway)", err.Error())
	})
	t.Run("List 502 marked", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{
			Status: http.StatusBadGateway, Header: gatewayHeader(), Body: `{"detail":{"error":"connect_failed"}}`,
		}, list)
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpListCalls, Kind: treg.FailureHTTPStatus, Status: http.StatusBadGateway, Marked: new(true),
		})
		require.NotNil(t, ce.Refusal)
		assert.Equal(t, "connect_failed", ce.Refusal.Code)
	})
}

func TestCallsMalformedResponses(t *testing.T) {
	t.Parallel()
	row := func(extra string) string {
		return `{"id":9001,"call_ref":"","user_email":"a@example.com","tool_name":"t","method":"GET","path":"/p",` +
			`"status_code":200,"kind":"call","client":"sdk","created_at":"2026-10-16T12:00:00Z"` + extra + `}`
	}
	list := bind(treg.CallsService.List, treg.ListCallsRequest{})
	get := bind(treg.CallsService.Get, treg.GetCallRequest{Ref: authReadCallRef})
	for _, tc := range []struct {
		name   string
		step   testutil.Step
		kind   treg.FailureKind
		reason string
	}{
		{"duplicate key in a row", testutil.Step{Body: "[" + row(`,"id":9002`) + "]"}, treg.FailureMalformedResponse, "duplicate_key"},
		{
			"duplicate key differing in case",
			testutil.Step{Body: "[" + row(`,"ID":9002`) + "]"},
			treg.FailureMalformedResponse, "duplicate_key",
		},
		{
			"duplicate key in tags",
			testutil.Step{Body: "[" + row(`,"tags":{"env":"a","env":"b"}`) + "]"},
			treg.FailureMalformedResponse, "duplicate_key",
		},
		{"quoted integer", testutil.Step{Body: `[{"id":"9001"}]`}, treg.FailureMalformedResponse, "decode"},
		{
			"quoted micro amount",
			testutil.Step{Body: "[" + row(`,"cost_charged_micro":"1500"`) + "]"},
			treg.FailureMalformedResponse, "decode",
		},
		{
			"fractional micro amount",
			testutil.Step{Body: "[" + row(`,"cost_charged_micro":1.5`) + "]"},
			treg.FailureMalformedResponse, "decode",
		},
		{"object instead of array", testutil.Step{Body: `{"calls":[]}`}, treg.FailureMalformedResponse, "decode"},
		{"trailing data", testutil.Step{Body: `[] []`}, treg.FailureMalformedResponse, "trailing_data"},
		{"invalid JSON", testutil.Step{Body: `[{"id":1,}]`}, treg.FailureMalformedResponse, "invalid_json"},
		{
			"wrong content type",
			testutil.Step{Header: http.Header{"Content-Type": {"text/html"}}, Body: "[]"},
			treg.FailureUnexpectedResponse, "content_type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := authReadStep(t, tc.step, list)
			requireCallError(t, err, callErrorWant{Op: treg.OpListCalls, Kind: tc.kind, Reason: tc.reason, Status: http.StatusOK})
		})
	}
	t.Run("duplicate key in a ledger entry", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{
			Body: `{"call":` + row("") + `,"ledger":[{"kind":"charge","kind":"refund","amount_micro":1,"created_at":"x"}]}`,
		}, get)
		requireCallError(t, err, callErrorWant{
			Op: treg.OpGetCall, Kind: treg.FailureMalformedResponse, Reason: "duplicate_key", Status: http.StatusOK,
		})
	})
	t.Run("duplicate key inside async_task is kept", func(t *testing.T) {
		t.Parallel()
		island := `{"state":"pending","state":"done"}`
		res, err := authReadStep(t, testutil.Step{Body: "[" + row(`,"async_task":`+island) + "]"}, list)
		require.NoError(t, err)
		require.Len(t, res.Value, 1)
		assertExactJSON(t, island, res.Value[0].AsyncTask)
	})
	t.Run("duplicate key inside the detail async_task is kept", func(t *testing.T) {
		t.Parallel()
		island := `{"task_id":"t1","task_id":"t2"}`
		res, err := authReadStep(t, testutil.Step{
			Body: `{"call":` + row("") + `,"async_task":` + island + `,"ledger":[],"charged_micro":0}`,
		}, get)
		require.NoError(t, err)
		assertExactJSON(t, island, res.Value.AsyncTask)
		assert.Equal(t, treg.Some(treg.MicroUSD(0)), res.Value.ChargedMicro)
	})
}

func TestCallsPrivacy(t *testing.T) {
	t.Parallel()
	emails := []string{authReadEmailMarker, "second.member@example.com"}
	bodies := []string{"BODYMARKER", "MARKERPATH"}
	res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "calls/list.json"),
		bind(treg.CallsService.List, treg.ListCallsRequest{}), treg.WithResponseCapture())
	page := res.Value
	require.Len(t, page, 3)
	row := page[0]
	stored, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "calls/result_stored.json"),
		bind(treg.CallsService.Result, treg.CallResultRequest{Ref: "9003"}))
	sr := stored.Value

	t.Run("CallRecord and CallPage print their shape", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, authReadEmailMarker, row.UserEmail, "the data stays accessible")
		testutil.AssertNeverPrints(t, &row, emails...)
		testutil.AssertNeverPrints(t, page, emails...)
		testutil.AssertNeverPrints(t, &page, emails...)
		testutil.AssertNeverPrints(t, res, emails...)
		assert.Equal(t, "treg.CallRecord{id: 9001, status: 402}", page[2].String(), "an empty ref is not printed")
		assert.Equal(t, page[1].String(), fmt.Sprintf("%#v", page[1]))
		b, err := json.Marshal(row)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"CallRecord","id":9003,"status_code":200,"call_ref":"`+authReadCallRef+`"}`, string(b))
		b, err = json.Marshal(page)
		require.NoError(t, err)
		testutil.AssertRedacted(t, string(b), append(emails, "example-search"))
	})
	for _, verb := range []string{"%v", "%+v"} {
		t.Run("CallPage prints its rows with "+verb, func(t *testing.T) {
			t.Parallel()
			out := fmt.Sprintf(verb, page)
			assert.NotContains(t, out, authReadEmailMarker)
			assert.Contains(t, out, "treg.CallRecord{id: 9003, status: 200, callRef: "+authReadCallRef+"}")
		})
	}
	t.Run("StoredResult prints its shape", func(t *testing.T) {
		t.Parallel()
		req, ok := sr.Request.Get()
		require.True(t, ok)
		require.Contains(t, req.URL, "MARKERPATH", "the data stays accessible")
		resp, ok := sr.Response.Get()
		require.True(t, ok)
		body, ok := resp.BodyText.Get()
		require.True(t, ok)
		require.Contains(t, body, "BODYMARKER")
		testutil.AssertNeverPrints(t, &sr, bodies...)
		testutil.AssertNeverPrints(t, stored, bodies...)
		want := "treg.StoredResult{id: 9003, status: 200, stored: true, callRef: " + authReadCallRef + "}"
		assert.Equal(t, want, fmt.Sprintf("%+v", sr))
		b, err := json.Marshal(sr)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"StoredResult","id":9003,"status_code":200,"stored":true,"call_ref":"`+
			authReadCallRef+`"}`, string(b))
	})
	t.Run("CallRecord every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &row, emails...)
	})
	t.Run("StoredResult every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &sr, bodies...)
	})
}

func TestCallsCapture(t *testing.T) {
	t.Parallel()
	fixture := synthetic(t, "calls/list.json")
	list := func(opts ...treg.CallOption) typedRead[treg.CallsService, treg.CallPage] {
		return bind(treg.CallsService.List, treg.ListCallsRequest{}, opts...)
	}
	checkCaptureCases(t, fixture, treg.OpListCalls,
		func(t *testing.T, step testutil.Step, opts []treg.Option, callOpts ...treg.CallOption) (treg.Capture, error) {
			t.Helper()
			_, captured, err := outcome(authReadStep(t, step, list(callOpts...), opts...))
			return captured, err
		},
		captureCase{
			name:     "strict model failure keeps its bytes",
			step:     func(string) testutil.Step { return testutil.Step{Body: `[{"id":1,"id":2}]`} },
			callOpts: []treg.CallOption{treg.CaptureResponse(true)}, state: treg.CaptureComplete,
			failure: &callErrorWant{Kind: treg.FailureMalformedResponse, Reason: "duplicate_key", Status: http.StatusOK},
		},
	)
	t.Run("a capture prints no data", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, fixture, list(treg.CaptureResponse(true)))
		require.Equal(t, treg.CaptureComplete, res.Captured.State())
		assert.Equal(t, fixture, string(res.Captured.Bytes()), "the exact bytes, whitespace included")
		assert.NotContains(t, fmt.Sprint(res.Captured), authReadEmailMarker)
	})
}
