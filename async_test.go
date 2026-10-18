package treg_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestAsyncDescriptorForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		raw   string
		check func(t *testing.T, d *treg.AsyncDescriptor)
	}{
		{
			"catalog poll and result path",
			`{"id_from":"id","poll":{"endpoint":"replicate.predictions.get","param":{"in":"pathParams","name":"id"}},` +
				`"status":{"path":"status","success":["succeeded"],"failure":["failed","canceled"]},"result":{"path":"output"},"interval":2}`,
			func(t *testing.T, d *treg.AsyncDescriptor) {
				t.Helper()
				assert.Equal(t, "id", d.IDFrom)
				assert.Equal(t, treg.EndpointID("replicate.predictions.get"), d.Poll.Endpoint)
				assert.Equal(t, &treg.AsyncParam{In: "pathParams", Name: "id"}, d.Poll.Param)
				assert.Len(t, d.Status.Failure, 2)
				assert.Equal(t, "output", d.Result.Path)
				assert.Nil(t, d.Result.FetchParam)
				assert.Equal(t, treg.Decimal("2"), d.Interval)
				assert.False(t, d.MaxAge.IsPresent())
			},
		},
		{
			"url poll and fetch result",
			`{"id_from":"data.task_id","poll":{"url_from":"urls.get","url_hosts":["api.example.com"]},` +
				`"status":{"path":"data.status","success":[20000],"progress":[20100,40601],"billed_failure":[40101]},` +
				`"result":{"fetch":"vendor.task.result","fetch_param":{"in":"queryParams","name":"id","value_from":"data.task_id"},` +
				`"ttl_note":"URLs expire after 1 hour"},"interval":0.5,"max_age":300,"terminal_on_submission":true,"future_key":{"x":1}}`,
			func(t *testing.T, d *treg.AsyncDescriptor) {
				t.Helper()
				assert.Equal(t, "urls.get", d.Poll.URLFrom)
				assert.Equal(t, []string{"api.example.com"}, d.Poll.URLHosts)
				assert.Nil(t, d.Poll.Param)
				assert.Equal(t, []json.RawMessage{json.RawMessage("20000")}, d.Status.Success, "integer statuses stay raw")
				assert.Len(t, d.Status.Progress, 2)
				assert.Equal(t, "vendor.task.result", d.Result.Fetch)
				assert.Equal(t, &treg.AsyncFetchParam{In: "queryParams", Name: "id", ValueFrom: "data.task_id"}, d.Result.FetchParam)
				assert.Equal(t, "URLs expire after 1 hour", d.Result.TTLNote)
				assert.Equal(t, treg.Decimal("0.5"), d.Interval)
				assert.Equal(t, treg.Some(int64(300)), d.MaxAge)
				assert.True(t, d.TerminalOnSubmission)
				assert.Contains(t, string(d.Raw), `"future_key"`, "unmodeled members stay in Raw")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{
				Status: 201,
				Header: http.Header{"Content-Type": {"application/json"}, "X-Treg-Async": {tt.raw}, "X-Treg-Cost-Micro": {"40000"}},
				Body:   `{"id":"pred-1","status":"starting"}`,
			}))
			sub, err := c.CallBytes(t.Context(), treg.CallRequest{
				Target: treg.CatalogEndpoint("replicate.image-gen.flux-schnell"),
				Method: http.MethodPost, Body: treg.JSONBody(map[string]any{"input": map[string]string{"prompt": "a lighthouse"}}),
			})
			require.NoError(t, err)
			d := sub.Meta().Async
			require.NotNil(t, d)
			assert.Equal(t, tt.raw, string(d.Raw))
			require.NotNil(t, sub.Meta().CostMicro)
			assert.Equal(t, treg.MicroUSD(40000), *sub.Meta().CostMicro, "on an async submission this is a reservation")
			tt.check(t, d)
		})
	}
}

// TestAsyncFollowUpIsExplicit follows Example 7: one submission, then one
// explicit status call built from descriptor data. The SDK makes nothing in
// between.
func TestAsyncFollowUpIsExplicit(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(
		testutil.Step{Status: 201, Header: http.Header{"Content-Type": {"application/json"}, "X-Treg-Async": {
			`{"id_from":"id","poll":{"endpoint":"replicate.predictions.get","param":{"in":"pathParams","name":"id"}},"status":{"path":"status","success":["succeeded"]},"result":{"path":"output"},"interval":2}`,
		}}, Body: `{"id":"pred-1"}`},
		testutil.Step{Body: `{"id":"pred-1","status":"processing"}`},
	)
	c := newClient(t, script)
	sub, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("replicate.image-gen.flux-schnell"), Method: http.MethodPost,
		Body: treg.JSONBody(map[string]string{"prompt": "x"}),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, script.Calls(), "no polling")
	d := sub.Meta().Async
	require.NotNil(t, d)
	require.NotNil(t, d.Poll.Param)
	var pred struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(sub.Body(), &pred))
	st, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint(d.Poll.Endpoint), Method: http.MethodGet,
		Query: url.Values{d.Poll.Param.Name: {pred.ID}},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"pred-1","status":"processing"}`, string(st.Body()))
	reqs := script.Requests()
	require.Len(t, reqs, 2)
	assert.Equal(t, testBaseURL+"/call/replicate.predictions.get?id=pred-1", reqs[1].URL)
}
