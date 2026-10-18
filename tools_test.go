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

// Tool markers: an owner email and a secret id, which must never print.
const (
	toolsOwnerMarker  = "owner.marker@example.com"
	toolsSecretMarker = "sec_MARKERMARKER"
)

// toolsReads returns every read of ToolsService, each with a valid request.
func toolsReads() []namedRead[treg.ToolsService] {
	return []namedRead[treg.ToolsService]{
		{"List", bind(treg.ToolsService.List, treg.ListToolsRequest{}).erased()},
		{"ByName", bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: "example-search"}).erased()},
	}
}

func TestToolsListDecodesFixture(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, proto, synthetic(t, "tools/list.json"), bind(treg.ToolsService.List, treg.ListToolsRequest{}))
			assert.Equal(t, string(proto), r.Proto)
			assertAuthReadWire(t, r, "/tools", "")
			assert.Equal(t, treg.OpListTools, res.Meta.Op)
			require.Len(t, res.Value, 2)
			assertToolsSearch(t, res.Value[0], 2)
			assertToolsBareTool(t, res.Value[1])
		})
	}
}

// assertToolsSearch checks the fixtures' example-search tool, which has
// bindings bindings.
func assertToolsSearch(t *testing.T, tool treg.Tool, bindings int) {
	t.Helper()
	assert.Equal(t, int64(501), tool.ID)
	assert.Equal(t, "example-search", tool.Name)
	assert.Equal(t, toolsOwnerMarker, tool.Owner)
	assert.Equal(t, "https://api.example.com/v1", tool.BaseURL)
	assert.Equal(t, "api.example.com", tool.Host)
	assertExactJSON(t, `{"method": "GET", "path": "/health", "expect_status": 200}`, tool.HealthCheck)
	assertExactJSON(t, `[{"title": "Search", "method": "POST", "path": "/search", "body": {"q": "example"}}]`,
		tool.Examples)
	assertExactJSON(t, `{"command": "example-search", "args": ["--json"]}`, tool.CLI)
	assert.True(t, tool.ServerRunnable)
	assert.Equal(t, "treg.Tool{id: 501, name: example-search, bindings: "+fmt.Sprint(bindings)+"}", tool.String())
	require.Len(t, tool.Bindings, bindings)
	header := tool.Bindings[0]
	assertExactJSON(t, `"`+toolsSecretMarker+`"`, header.SecretID)
	assert.Equal(t, treg.Some("header"), header.Injector)
	assert.Equal(t, treg.Some("header"), header.Location)
	assert.Equal(t, treg.Some("Authorization"), header.Name)
	assert.Equal(t, treg.Some("Bearer {value}"), header.ValueFormat)
	assert.Equal(t, treg.Null[string](), header.SecretField)
	assert.Equal(t, treg.Null[string](), header.TokenEncode)
	if bindings < 2 {
		return
	}
	query := tool.Bindings[1]
	assertExactJSON(t, "77", query.SecretID)
	assert.Equal(t, treg.Some("query"), query.Injector)
	assert.Equal(t, treg.Some("api_key"), query.Name)
	assert.Equal(t, treg.Null[string](), query.ValueFormat)
	assert.Equal(t, treg.Some("key"), query.SecretField)
	assert.Equal(t, treg.Some("none"), query.TokenEncode)
	assert.Equal(t, treg.Null[int64](), tool.ProjectID, "null: org-wide")
	assert.Equal(t, treg.Some[int64](3), tool.BundleID)
}

// assertToolsBareTool checks the fixture's tool without bindings, health
// check or CLI.
func assertToolsBareTool(t *testing.T, tool treg.Tool) {
	t.Helper()
	assert.Equal(t, int64(502), tool.ID)
	assert.Equal(t, "example-tts", tool.Name)
	assert.NotNil(t, tool.Bindings)
	assert.Empty(t, tool.Bindings)
	assertExactJSON(t, "null", tool.HealthCheck)
	assertExactJSON(t, "[]", tool.Examples)
	assertExactJSON(t, "null", tool.CLI)
	assert.False(t, tool.ServerRunnable)
	assert.Equal(t, treg.Some[int64](9), tool.ProjectID)
	assert.Equal(t, treg.Null[int64](), tool.BundleID)
	assert.Equal(t, "treg.Tool{id: 502, name: example-tts, bindings: 0}", tool.String())
}

func TestToolsByNameDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP2, synthetic(t, "tools/by_name.json"),
		bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: "example-search"}))
	assertAuthReadWire(t, r, "/tools/by-name/example-search", "")
	assert.Equal(t, treg.OpToolByName, res.Meta.Op)
	assertToolsSearch(t, res.Value, 1)
	assert.Equal(t, treg.Some[int64](9), res.Value.ProjectID)
	assert.Equal(t, treg.Null[int64](), res.Value.BundleID)
}

func TestToolsWire(t *testing.T) {
	t.Parallel()
	byName := func(name string) anyRead[treg.ToolsService] {
		return bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: name}).erased()
	}
	long := strings.Repeat("t", 128)
	tests := []struct {
		name, fixture, want string
		read                anyRead[treg.ToolsService]
	}{
		{"List", "tools/list.json", "/tools", bind(treg.ToolsService.List, treg.ListToolsRequest{}).erased()},
		{"ByName", "tools/by_name.json", "/tools/by-name/example-search", byName("example-search")},
		{
			"ByName unreserved punctuation", "tools/by_name.json", "/tools/by-name/my_tool.v2~beta-1",
			byName("my_tool.v2~beta-1"),
		},
		{"ByName dots inside", "tools/by_name.json", "/tools/by-name/...a", byName("...a")},
		{"ByName max length", "tools/by_name.json", "/tools/by-name/" + long, byName(long)},
		{"ByName http without colon", "tools/by_name.json", "/tools/by-name/https-proxy", byName("https-proxy")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAuthReadWire(t, synthetic(t, tc.fixture), tc.read, tc.want)
		})
	}
}

func TestToolsValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, tool, field, reason string
	}{
		{"empty", "", "Name", "required"},
		{"slash", "a/b", "Name", "invalid_character"},
		{"https prefix", "https:example", "Name", "invalid_character"},
		{"http prefix upper case", "HTTP:example", "Name", "invalid_character"},
		{"space", "a b", "Name", "invalid_character"},
		{"percent escape", "a%2Fb", "Name", "invalid_character"},
		{"question mark", "a?b", "Name", "invalid_character"},
		{"non-ASCII", "naïve", "Name", "invalid_character"},
		{"control character", "a\nb", "Name", "control_character"},
		{"dot", ".", "Name", "dot_segment"},
		{"dot dot", "..", "Name", "dot_segment"},
		{"too long", strings.Repeat("t", 129), "Name", "too_long"},
		{"token as name", testToken, "Request", "credential_in_request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			_, err := bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: tc.tool}).on(t.Context(), newClient(t, script))
			requireValidation(t, err, tc.field, tc.reason)
			assert.Zero(t, script.Calls(), "nothing is sent")
		})
	}
}

func TestToolsZeroValue(t *testing.T) {
	t.Parallel()
	requireAuthReadsNotInitialized(t.Context(), t, toolsReads())
}

func TestToolsOptionsAndContext(t *testing.T) {
	t.Parallel()
	requireAuthReadsRefuseOptions(t.Context(), t, toolsReads())
}

func TestToolsStatusErrors(t *testing.T) {
	t.Parallel()
	t.Run("ByName 404", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{Status: http.StatusNotFound, Body: `{"detail":"tool not found"}`},
			bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: "missing-tool"}))
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpToolByName, Kind: treg.FailureHTTPStatus, Status: http.StatusNotFound, Marked: new(false),
		})
		assert.Nil(t, ce.Refusal)
		assert.Equal(t, "treg: tool_by_name: http_status (HTTP 404)", err.Error())
	})
	t.Run("List 403 marked", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{
			Status: http.StatusForbidden, Header: gatewayHeader(), Body: `{"detail":{"error":"tool_access_denied"}}`,
		}, bind(treg.ToolsService.List, treg.ListToolsRequest{}))
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpListTools, Kind: treg.FailureHTTPStatus, Status: http.StatusForbidden, Marked: new(true),
		})
		require.NotNil(t, ce.Refusal)
		assert.Equal(t, "tool_access_denied", ce.Refusal.Code)
		assert.Equal(t, "treg: list_tools: http_status (HTTP 403, gateway): tool_access_denied", err.Error())
	})
}

func TestToolsMalformedResponses(t *testing.T) {
	t.Parallel()
	tool := func(extra string) string {
		return `{"id":501,"name":"t","owner":"o@example.com","base_url":"https://example.com","host":"example.com",` +
			`"server_runnable":false` + extra + `}`
	}
	list := bind(treg.ToolsService.List, treg.ListToolsRequest{}).erased()
	for _, tc := range []struct {
		name, body string
		read       anyRead[treg.ToolsService]
		op         treg.Operation
		reason     string
	}{
		{"List object body", `{"tools":[]}`, list, treg.OpListTools, "decode"},
		{"List duplicate key in a tool", "[" + tool(`,"name":"u"`) + "]", list, treg.OpListTools, "duplicate_key"},
		{
			"List duplicate key in a binding", "[" + tool(`,"bindings":[{"secret_id":1,"name":"a","name":"b"}]`) + "]",
			list, treg.OpListTools, "duplicate_key",
		},
		{
			"List duplicate key differing in case", "[" + tool(`,"Owner":"p@example.com"`) + "]",
			list, treg.OpListTools, "duplicate_key",
		},
		{"List quoted id", `[{"id":"501"}]`, list, treg.OpListTools, "decode"},
		{"List quoted project id", "[" + tool(`,"project_id":"9"`) + "]", list, treg.OpListTools, "decode"},
		{"List string boolean", `[{"id":501,"server_runnable":"true"}]`, list, treg.OpListTools, "decode"},
		{
			"ByName array body", "[" + tool("") + "]",
			bind(treg.ToolsService.ByName, treg.ToolByNameRequest{Name: "t"}).erased(), treg.OpToolByName, "decode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallError(t, authReadStepErr(t, testutil.Step{Body: tc.body}, tc.read), callErrorWant{
				Op: tc.op, Kind: treg.FailureMalformedResponse, Reason: tc.reason, Status: http.StatusOK,
			})
		})
	}
}

func TestToolsPrivacy(t *testing.T) {
	t.Parallel()
	markers := []string{toolsOwnerMarker, toolsSecretMarker}
	res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "tools/list.json"), bind(treg.ToolsService.List, treg.ListToolsRequest{}))
	tools := res.Value
	require.Len(t, tools, 2)
	tool := tools[0]
	require.Len(t, tool.Bindings, 2)
	binding := tool.Bindings[0]
	t.Run("Tool prints its id name and binding count", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, toolsOwnerMarker, tool.Owner, "the data stays accessible")
		require.Contains(t, string(binding.SecretID), toolsSecretMarker)
		testutil.AssertNeverPrints(t, &tool, markers...)
		testutil.AssertNeverPrints(t, tools, markers...)
		testutil.AssertNeverPrints(t, &tools, markers...)
		testutil.AssertNeverPrints(t, res, markers...)
		b, err := json.Marshal(tool)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"Tool","id":501,"count":2}`, string(b))
		b, err = json.Marshal(tools)
		require.NoError(t, err)
		testutil.AssertRedacted(t, string(b), markers)
	})
	for _, verb := range []string{"%v", "%+v"} {
		t.Run("Tools print their ids names and binding counts with "+verb, func(t *testing.T) {
			t.Parallel()
			out := fmt.Sprintf(verb, tools)
			testutil.AssertRedacted(t, out, markers)
			assert.Equal(t, "[treg.Tool{id: 501, name: example-search, bindings: 2} "+
				"treg.Tool{id: 502, name: example-tts, bindings: 0}]", out)
		})
	}
	t.Run("an unsafe name is masked", func(t *testing.T) {
		t.Parallel()
		odd := treg.Tool{ID: 9, Name: authReadEmailMarker, Owner: toolsOwnerMarker}
		assert.Equal(t, "treg.Tool{id: 9, name: ?, bindings: 0}", odd.String())
		testutil.AssertNeverPrints(t, &odd, authReadEmailMarker, toolsOwnerMarker)
	})
	t.Run("ToolBinding prints its type only", func(t *testing.T) {
		t.Parallel()
		testutil.AssertNeverPrints(t, &binding, markers...)
		testutil.AssertNeverPrints(t, tool.Bindings, markers...)
		b, err := json.Marshal(binding)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"ToolBinding"}`, string(b))
	})
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run("ToolBinding prints its type only with "+verb, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "treg.ToolBinding{}", fmt.Sprintf(verb, binding))
		})
	}
	t.Run("Tool every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &tool, markers...)
	})
	t.Run("ToolBinding every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &binding, markers...)
	})
}
