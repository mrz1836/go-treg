package treg_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func idemReq(key string) treg.CallRequest {
	return treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"), Method: http.MethodPost,
		Body:     treg.JSONBody(map[string][]string{"targets": {"example.com"}}),
		Controls: treg.Controls{IdempotencyKey: key},
	}
}

func TestIdempotencyReplayMetadata(t *testing.T) {
	t.Parallel()
	ref := strings.Repeat("ef", 16)
	script := testutil.NewScript(testutil.Step{
		Header: http.Header{
			"Content-Type": {"application/json"}, "X-Treg-Idempotent-Replay": {"true"}, "X-Treg-Call-Id": {ref},
			"X-Treg-Cost-Micro": {"0"}, "X-Treg-Original-Cost-Micro": {"24000"},
		},
		Body: `{"results":[]}`,
	})
	c := newClient(t, script)
	res, err := treg.CallJSON[map[string]any](t.Context(), c, idemReq("job-42 recovery"[:6]))
	require.NoError(t, err)
	assert.True(t, res.Meta.Replayed)
	assert.Equal(t, ref, res.Meta.CallRef, "a replay carries the original call's ref")
	require.NotNil(t, res.Meta.CostMicro)
	assert.Equal(t, treg.MicroUSD(0), *res.Meta.CostMicro, "a replay costs zero")
	require.NotNil(t, res.Meta.OriginalCostMicro)
	assert.Equal(t, treg.MicroUSD(24000), *res.Meta.OriginalCostMicro)
	assert.Equal(t, "job-42", headerFold(testutil.OnlyRequest(t, script).Header, "Idempotency-Key"), "the key is sent exactly")
}

func TestIdempotencyRefusalsAreNotRetried(t *testing.T) {
	t.Parallel()
	ref := strings.Repeat("12", 16)
	tests := []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"409 in progress", 409, `{"detail":"a call with Idempotency-Key 'k' is still in progress — retry shortly"}`, ""},
		{"422 mismatch", 422, `{"detail":"Idempotency-Key reused for a different request"}`, ""},
		{
			"410 lost", 410, `{"detail":{"error":"idempotency_response_lost","call_id":"` + ref + `","charged_micro":24000,"message":"m"}}`,
			"idempotency_response_lost",
		},
		{
			"410 outcome unknown", 410, `{"detail":{"error":"idempotency_outcome_unknown","call_id":"` + ref + `"}}`,
			"idempotency_outcome_unknown",
		},
		{
			"410 too large", 410, `{"detail":{"error":"idempotency_response_too_large","call_id":"` + ref + `","size_bytes":3000000}}`,
			"idempotency_response_too_large",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript(testutil.Step{
				Status: tt.status, Header: gatewayHeader(), Body: tt.body,
			})
			c := newClient(t, script)
			_, err := treg.CallJSON[any](t.Context(), c, idemReq("job-42"))
			ce := requireCallError(t, err, callErrorWant{Is: treg.ErrGatewayRefusal})
			assert.Equal(t, 1, script.Calls(), "no retry")
			assert.Equal(t, "job-42", headerFold(testutil.OnlyRequest(t, script).Header, "Idempotency-Key"), "no new key is invented")
			if tt.code == "" {
				assert.Nil(t, ce.Refusal, "string details are never parsed into codes")
				return
			}
			require.NotNil(t, ce.Refusal)
			assert.Equal(t, tt.code, ce.Refusal.Code)
			assert.Equal(t, ref, ce.Refusal.CallRef)
		})
	}
}

func TestIdempotencyNeverInvented(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Status: 500, Body: `{}`})
	c := newClient(t, script)
	_, err := treg.CallJSON[any](t.Context(), c, treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"), Method: http.MethodPost, Body: treg.JSONBody(1),
	})
	require.ErrorIs(t, err, treg.ErrOutcomeUnknown)
	r := testutil.OnlyRequest(t, script)
	for k := range r.Header {
		assert.NotEqual(t, "idempotency-key", strings.ToLower(k), "the SDK never invents a key")
	}
	assert.Equal(t, 1, script.Calls(), "and never retries")
}

// headerFold returns the first value of name, matched ignoring case, as a
// server would read it.
func headerFold(h http.Header, name string) string {
	for k, vs := range h {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}
