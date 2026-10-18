package treg

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRefusal(t *testing.T) {
	t.Parallel()
	ref := strings.Repeat("0f", 16)
	tests := []struct {
		name string
		body string
		want *Refusal
	}{
		{
			"402 balance nested", `{"detail":{"error":"insufficient_balance","message":"top up","balance_micro":5000,` +
				`"estimated_cost_micro":20000,"topup_url":"/app#billing","provider":"moz"}}`,
			&Refusal{Code: "insufficient_balance", BalanceMicro: new(MicroUSD(5000)), EstimatedCostMicro: new(MicroUSD(20000))},
		},
		{
			"402 balance flat", `{"error":"insufficient_balance","balance_micro":0,"estimated_cost_micro":1}`,
			&Refusal{Code: "insufficient_balance", BalanceMicro: new(MicroUSD(0)), EstimatedCostMicro: new(MicroUSD(1))},
		},
		{
			"402 max cost", `{"detail":{"error":"route_max_cost","max_cost_micro":10000,"estimated_cost_micro":24000}}`,
			&Refusal{Code: "route_max_cost", MaxCostMicro: new(MicroUSD(10000)), EstimatedCostMicro: new(MicroUSD(24000))},
		},
		{
			"routed failure charged", `{"detail":{"error":"route_failed","charged_micro":300,"tried":[]}}`,
			&Refusal{Code: "route_failed", ChargedMicro: new(MicroUSD(300))},
		},
		{"409 string detail", `{"detail":"a call with Idempotency-Key 'k' is still in progress — retry shortly"}`, nil},
		{"422 string detail", `{"detail":"Idempotency-Key reused for a different request"}`, nil},
		{
			"410 lost", `{"detail":{"error":"idempotency_response_lost","call_id":"` + ref + `","charged_micro":20000,"message":"m"}}`,
			&Refusal{Code: "idempotency_response_lost", CallRef: ref, ChargedMicro: new(MicroUSD(20000))},
		},
		{
			"410 too large", `{"detail":{"error":"idempotency_response_too_large","call_id":"` + ref + `","size_bytes":3000000}}`,
			&Refusal{Code: "idempotency_response_too_large", CallRef: ref},
		},
		{
			"410 bad call id", `{"detail":{"error":"idempotency_outcome_unknown","call_id":"nope"}}`,
			&Refusal{Code: "idempotency_outcome_unknown"},
		},
		{"410 tombstone prose", `{"detail":"moz.old.thing is retired. Use moz.new.thing instead."}`, nil},
		{
			"428", `{"detail":{"error":"authorization_missing","provider":"slack","required_scopes":["a"]}}`,
			&Refusal{Code: "authorization_missing"},
		},
		{
			"429 org flow", `{"detail":{"error":"org_rate_limit","retry_after_s":2.5,"config_version":3}}`,
			&Refusal{Code: "org_rate_limit", RetryAfterSeconds: new(int64(3))},
		},
		{"429 share busy", `{"detail":{"error":"share_busy","retry_after_s":1}}`, &Refusal{Code: "share_busy", RetryAfterSeconds: new(int64(1))}},
		{
			"429 tag cap flat", `{"error":"tag_spend_cap_reached","dim":"customer","val":"c1","spent_micro":5,"cap_micro":5}`,
			&Refusal{Code: "tag_spend_cap_reached"},
		},
		{"503 paused", `{"detail":{"error":"provider_paused","message":"paused"}}`, &Refusal{Code: "provider_paused"}},
		{
			"503 capacity", `{"detail":{"error":"provider_capacity_unavailable","resets_at":null,"alternatives":[]}}`,
			&Refusal{Code: "provider_capacity_unavailable"},
		},
		{"503 saturated", `{"detail":"treg is busy","treg_saturated":true}`, &Refusal{Saturated: true}},
		{"saturated false", `{"detail":"x","treg_saturated":false}`, nil},
		{"502 buffer", `{"detail":{"error":"response_buffer_limit","message":"temporary"}}`, &Refusal{Code: "response_buffer_limit"}},
		{"prose error", `{"detail":{"error":"unknown endpoint 'x.y'","hint":"search"}}`, nil},
		{"error too long", `{"error":"` + strings.Repeat("a", 65) + `"}`, nil},
		{"error upper", `{"error":"Insufficient_Balance"}`, nil},
		{"unknown code kept", `{"detail":{"error":"sk_live_abcdef"}}`, &Refusal{Code: "sk_live_abcdef"}},
		{"negative retry ignored", `{"detail":{"error":"share_busy","retry_after_s":-1}}`, &Refusal{Code: "share_busy"}},
		{"fractional money ignored", `{"detail":{"error":"route_max_cost","max_cost_micro":1.5}}`, &Refusal{Code: "route_max_cost"}},
		{"quoted money ignored", `{"detail":{"error":"route_max_cost","max_cost_micro":"15"}}`, &Refusal{Code: "route_max_cost"}},
		{"not json", `<html>502</html>`, nil},
		{"array", `[1,2]`, nil},
		{"empty", ``, nil},
		{"null detail", `{"detail":null}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseRefusal([]byte(tt.body)))
		})
	}
}

func TestRefusalPrinting(t *testing.T) {
	t.Parallel()
	secretCode := "sk_live_abcdef"
	r := Refusal{Code: secretCode, ChargedMicro: new(MicroUSD(7))}
	assert.False(t, r.Known())
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			assert.NotContains(t, fmt.Sprintf(verb, r), secretCode, verb)
			assert.NotContains(t, fmt.Sprintf(verb, &r), secretCode, verb)
		})
	}
	out, err := json.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(out), secretCode)
	assert.Contains(t, string(out), `"code":"refused"`)

	ce := &CallError{Kind: FailureHTTPStatus, Op: OpCall, StatusCode: 402, GatewayMarked: true, Refusal: &r}
	assert.Equal(t, "treg: call: http_status (HTTP 402, gateway): refused", ce.Error())
	assert.NotContains(t, ce.GoString(), secretCode)
	out, err = json.Marshal(ce)
	require.NoError(t, err)
	assert.NotContains(t, string(out), secretCode)

	known := &CallError{
		Kind: FailureHTTPStatus, Op: OpCall, StatusCode: 402, GatewayMarked: true,
		Refusal: &Refusal{Code: "insufficient_balance"},
	}
	assert.Equal(t, "treg: call: http_status (HTTP 402, gateway): insufficient_balance", known.Error())
	assert.True(t, known.Refusal.Known())
	assert.Equal(t, "treg.Refusal{}", Refusal{}.String())
	assert.False(t, Refusal{}.Known())
}

func TestPreRelayRefusal(t *testing.T) {
	t.Parallel()
	assert.True(t, preRelayRefusal(&Refusal{Saturated: true}))
	assert.True(t, preRelayRefusal(&Refusal{Code: "provider_paused"}))
	assert.True(t, preRelayRefusal(&Refusal{Code: "provider_capacity_unavailable"}))
	assert.False(t, preRelayRefusal(&Refusal{Code: "route_failed"}))
	assert.False(t, preRelayRefusal(nil))
}
