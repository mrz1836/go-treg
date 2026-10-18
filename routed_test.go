package treg_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

type emailOut struct {
	Email      *string         `json:"email"`
	Confidence json.RawMessage `json:"confidence"`
}

// routedRequest returns a call to a routed catalog id.
func routedRequest() treg.CallRequest {
	return treg.CallRequest{
		Target: treg.CatalogEndpoint("treg.people.email.find"), Method: http.MethodPost,
		Body: treg.JSONBody(map[string]string{"domain": "example.com", "full_name": "Ada Example"}),
	}
}

const routedHit = `{"output":{"email":"ada@example.com","confidence":0.92},` +
	`"raw":{"data":{"email":"ada@example.com","score":92,"sources":[]} , "data2":1},` +
	`"_treg":{"served_by":"hunter.people.email.find","provider":"hunter","tier":"platform","outcome":"hit",` +
	`"tried":[{"endpoint_id":"prospeo.people.email.find","provider":"prospeo","outcome":"miss","status":200,"charged_micro":0},` +
	`{"endpoint_id":"hunter.people.email.find","provider":"hunter","outcome":"hit","status":200,"charged_micro":10000,` +
	`"detail":{"x":1}}],"charged_micro":10000,"verification":{"verdict":"valid"},"capped":false,"merged_from":["a.b.c"]}}`

func TestRoutedHit(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{
		Header: http.Header{
			"Content-Type": {"application/json"}, "X-Treg-Served-By": {"hunter.people.email.find"},
			"X-Treg-Cost-Micro": {"10000"},
		},
		Body: routedHit,
	}))
	res, err := treg.CallJSON[treg.Routed[emailOut]](t.Context(), c, routedRequest())
	require.NoError(t, err)
	r := res.Value
	require.NotNil(t, r.Output.Email)
	assert.Equal(t, "ada@example.com", *r.Output.Email)
	assert.JSONEq(t, `0.92`, string(r.Output.Confidence))
	assertSameBytes(t, `{"data":{"email":"ada@example.com","score":92,"sources":[]} , "data2":1}`, r.Raw,
		"the provider document is kept byte for byte")
	assert.Equal(t, treg.Some(treg.EndpointID("hunter.people.email.find")), r.Treg.ServedBy)
	assert.Equal(t, treg.RouteOutcomeHit, r.Treg.Outcome)
	assert.True(t, r.Treg.Outcome.Known())
	assert.Equal(t, "platform", r.Treg.Tier)
	require.Len(t, r.Treg.Tried, 2)
	assert.Equal(t, "miss", r.Treg.Tried[0].Outcome)
	assert.Equal(t, treg.Some(int64(200)), r.Treg.Tried[1].Status)
	assert.Equal(t, treg.Some(treg.MicroUSD(10000)), r.Treg.ChargedMicro)
	assert.JSONEq(t, `{"verdict":"valid"}`, string(r.Treg.Verification))
	assert.Equal(t, []treg.EndpointID{"a.b.c"}, r.Treg.MergedFrom)
	assert.Equal(t, treg.EndpointID("hunter.people.email.find"), res.Meta.ServedBy)
}

func TestRoutedPending(t *testing.T) {
	t.Parallel()
	ref := strings.Repeat("9", 32) + ":r1"
	body := `{"output":{"email":null},"raw":null,"_treg":{"served_by":"replicate.x.y","provider":"replicate","tier":"platform",` +
		`"outcome":"pending","tried":[],"call_ref":"` + ref + `","async":{"task_id":"t-123","poll":{"endpoint":"a.b.c"},` +
		`"status":{"path":"status"}},"reserved_micro":50000,"charged_micro":null}}`
	c := newClient(t, testutil.NewScript(testutil.Step{
		Status: http.StatusAccepted,
		Header: http.Header{
			"Content-Type": {"application/json"}, "X-Treg-Async": {`{"task_id":"t-123","poll":{"endpoint":"a.b.c"},"status":{"path":"status"}}`},
			"X-Treg-Route-Outcome": {"pending"},
		},
		Body: body,
	}, testutil.Step{Status: http.StatusAccepted, Body: body}))
	res, err := treg.CallJSON[treg.Routed[emailOut]](t.Context(), c, routedRequest())
	require.NoError(t, err, "a routed 202 is a success")
	assert.Equal(t, http.StatusAccepted, res.Meta.StatusCode)
	assert.Equal(t, treg.RouteOutcomePending, res.Value.Treg.Outcome)
	assert.True(t, res.Value.Treg.ChargedMicro.IsNull(), "null while pending: unknown, not zero")
	assert.Equal(t, treg.Some(treg.MicroUSD(50000)), res.Value.Treg.ReservedMicro)
	assert.Equal(t, ref, res.Value.Treg.CallRef)
	async, ok := res.Value.Treg.Async.Get()
	require.True(t, ok)
	assert.Equal(t, "t-123", async.TaskID)
	assert.Equal(t, "null", string(res.Value.Raw))
	assert.Nil(t, res.Value.Output.Email)
	require.NotNil(t, res.Meta.Async)
	assert.Equal(t, "t-123", res.Meta.Async.TaskID)
	assert.Nil(t, res.Meta.CostMicro, "no cost header on a pending routed call")

	plain, err := treg.CallJSON[map[string]any](t.Context(), c, routedRequest())
	require.NoError(t, err, "with another T the envelope decodes into T")
	assert.Equal(t, http.StatusAccepted, plain.Meta.StatusCode)
	assert.Contains(t, plain.Value, "_treg")
}

func TestRoutedMiss(t *testing.T) {
	t.Parallel()
	body := `{"output":{"email":null},"raw":null,"_treg":{"served_by":null,"provider":null,"tier":"platform","outcome":"miss",` +
		`"tried":[{"endpoint_id":"a.b.c","provider":"a","outcome":"miss","status":null,"charged_micro":null}],"charged_micro":2000}}`
	c := newClient(t, testutil.NewScript(testutil.Step{Body: body}))
	res, err := treg.CallJSON[treg.Routed[emailOut]](t.Context(), c, routedRequest())
	require.NoError(t, err)
	assert.Equal(t, treg.RouteOutcomeMiss, res.Value.Treg.Outcome)
	assert.True(t, res.Value.Treg.ServedBy.IsNull())
	require.Len(t, res.Value.Treg.Tried, 1)
	assert.True(t, res.Value.Treg.Tried[0].Status.IsNull())
	assert.Equal(t, treg.Some(treg.MicroUSD(2000)), res.Value.Treg.ChargedMicro, "a miss may still be charged")
}

func TestRoutedAsyncStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		treg    string
		present bool
		null    bool
		taskID  string
		encoded string
	}{
		{"absent", `{"outcome":"hit"}`, false, false, "", ""},
		{"null", `{"outcome":"hit","async":null}`, true, true, "", `null`},
		{
			"value", `{"outcome":"pending","async":{"task_id":"t-1","poll":{"endpoint":"a.b.c"}}}`, true, false, "t-1",
			`{"task_id":"t-1","poll":{"endpoint":"a.b.c"},"status":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{
				Body: `{"output":{},"_treg":` + tt.treg + `}`,
			}))
			res, err := treg.CallJSON[treg.Routed[emailOut]](t.Context(), c, routedRequest())
			require.NoError(t, err)
			async := res.Value.Treg.Async
			assert.Equal(t, tt.present, async.IsPresent())
			assert.Equal(t, tt.null, async.IsNull())
			v, ok := async.Get()
			assert.Equal(t, tt.taskID != "", ok)
			assert.Equal(t, tt.taskID, v.TaskID)

			out, err := json.Marshal(res.Value.Treg)
			require.NoError(t, err)
			var members map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out, &members))
			got, found := members["async"]
			assert.Equal(t, tt.present, found, "an absent async is omitted")
			if tt.present {
				assert.JSONEq(t, tt.encoded, string(got))
			}
		})
	}
}

func TestRoutedStrictness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		ok   bool
		// known is whether an accepted body's outcome is a known one.
		known bool
	}{
		{"plain body", `{"email":"a@example.com"}`, false, false},
		{"treg not object", `{"output":{},"_treg":[]}`, false, false},
		{"duplicate in _treg", `{"output":{},"_treg":{"outcome":"hit","outcome":"miss"}}`, false, false},
		{"duplicate case in _treg", `{"output":{},"_treg":{"outcome":"hit","Outcome":"miss"}}`, false, false},
		{"duplicate in raw", `{"output":{},"raw":{"a":1,"a":2},"_treg":{"outcome":"hit"}}`, true, true},
		{"duplicate in output", `{"output":{"email":"a","email":"b"},"_treg":{"outcome":"hit"}}`, true, true},
		{"unknown outcome", `{"output":{},"_treg":{"outcome":"partial"}}`, true, false},
		{"quoted charge", `{"output":{},"_treg":{"outcome":"hit","charged_micro":"5"}}`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{Body: tt.body}))
			res, err := treg.CallJSON[treg.Routed[emailOut]](t.Context(), c, routedRequest())
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.known, res.Value.Treg.Outcome.Known())
				return
			}
			// The call executed and was answered: its outcome is known.
			requireCallError(t, err, callErrorWant{Is: treg.ErrMalformedResponse})
		})
	}
}
