package treg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// maxRefusalBytes bounds the part of an error body read for a Refusal.
const maxRefusalBytes = 64 << 10

// Refusal holds the machine-readable fields of a 4xx or 5xx JSON body: Treg's
// own refusals carry them under "detail" (FastAPI's envelope) or at the top
// level; the parser accepts both. String messages, such as idempotency and
// tombstone prose, are never parsed into codes.
//
// An upstream reached on its own key can send a body of the same shape, so
// treat every field as evidence. Code is printed by Error, String, GoString
// and MarshalJSON only when Known; otherwise they print "refused".
type Refusal struct {
	// Code is detail.error or error, when it is 1 to 64 bytes of
	// [a-z0-9_], such as "insufficient_balance" or "route_max_cost". The set
	// is open.
	Code string

	// Saturated reports {"treg_saturated": true}: Treg's pool was saturated
	// (HTTP 503 without X-Treg-Error); nothing was relayed or billed.
	Saturated bool

	// BalanceMicro is balance_micro (402 insufficient_balance).
	BalanceMicro *MicroUSD

	// EstimatedCostMicro is estimated_cost_micro (402).
	EstimatedCostMicro *MicroUSD

	// MaxCostMicro is max_cost_micro (402 route_max_cost).
	MaxCostMicro *MicroUSD

	// ChargedMicro is charged_micro (routed failures, idempotency 410s).
	ChargedMicro *MicroUSD

	// RetryAfterSeconds is retry_after_s, rounded up to whole seconds.
	RetryAfterSeconds *int64

	// CallRef is detail.call_id of an idempotency 410, when it is a valid
	// call reference.
	CallRef string
}

// Known reports whether Code is in the SDK's compiled allowlist of codes
// Treg documents or its source was observed to send.
func (r Refusal) Known() bool {
	return knownRefusalCode(r.Code)
}

// String prints the refusal with an unknown code masked as "refused".
func (r Refusal) String() string {
	var fields []string
	if c := r.printableCode(); c != "" {
		fields = append(fields, "code: "+c)
	}
	if r.Saturated {
		fields = append(fields, "saturated: true")
	}
	for _, f := range [...]struct {
		name string
		v    *MicroUSD
	}{
		{"balanceMicro", r.BalanceMicro},
		{"estimatedCostMicro", r.EstimatedCostMicro},
		{"maxCostMicro", r.MaxCostMicro},
		{"chargedMicro", r.ChargedMicro},
	} {
		if f.v != nil {
			fields = append(fields, f.name+": "+strconv.FormatInt(int64(*f.v), 10))
		}
	}
	if r.RetryAfterSeconds != nil {
		fields = append(fields, "retryAfterSeconds: "+strconv.FormatInt(*r.RetryAfterSeconds, 10))
	}
	if validCallRef(r.CallRef) {
		fields = append(fields, "callRef: "+r.CallRef)
	}
	return "treg.Refusal{" + strings.Join(fields, ", ") + "}"
}

// GoString returns the same text as String.
func (r Refusal) GoString() string { return r.String() }

// Format prints String for every verb.
func (r Refusal) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the fields by the rule of String.
func (r Refusal) MarshalJSON() ([]byte, error) {
	out := refusalJSON{
		Code:               r.printableCode(),
		Saturated:          r.Saturated,
		BalanceMicro:       r.BalanceMicro,
		EstimatedCostMicro: r.EstimatedCostMicro,
		MaxCostMicro:       r.MaxCostMicro,
		ChargedMicro:       r.ChargedMicro,
		RetryAfterSeconds:  r.RetryAfterSeconds,
	}
	if validCallRef(r.CallRef) {
		out.CallRef = r.CallRef
	}
	return json.Marshal(out)
}

// printableCode returns Code when it is known, "refused" when it is set but
// unknown, and "" when it is empty.
func (r Refusal) printableCode() string {
	switch {
	case r.Code == "":
		return ""
	case r.Known():
		return r.Code
	}
	return "refused"
}

// refusalJSON is the JSON form of a Refusal.
type refusalJSON struct {
	Code               string    `json:"code,omitempty"`
	Saturated          bool      `json:"saturated,omitempty"`
	BalanceMicro       *MicroUSD `json:"balance_micro,omitempty"`
	EstimatedCostMicro *MicroUSD `json:"estimated_cost_micro,omitempty"`
	MaxCostMicro       *MicroUSD `json:"max_cost_micro,omitempty"`
	ChargedMicro       *MicroUSD `json:"charged_micro,omitempty"`
	RetryAfterSeconds  *int64    `json:"retry_after_s,omitempty"`
	CallRef            string    `json:"call_ref,omitempty"`
}

// preRelayRefusal reports whether r proves that Treg refused a request
// before relaying it, so that a 5xx leaves no outcome open.
func preRelayRefusal(r *Refusal) bool {
	if r == nil {
		return false
	}
	return r.Saturated || r.Code == "provider_paused" || r.Code == "provider_capacity_unavailable"
}

// parseRefusal reads the Refusal from the first 64 KiB of an error body. It
// returns nil when the body is not a JSON object or carries none of the
// fields.
func parseRefusal(body []byte) *Refusal {
	if len(body) > maxRefusalBytes {
		body = body[:maxRefusalBytes]
	}
	top, err := objectMembers(body)
	if err != nil {
		return nil
	}
	r := &Refusal{}
	fields := top
	if raw, ok := top.member("detail"); ok {
		if detail, derr := objectMembers(raw); derr == nil {
			fields = detail
		}
	}
	var code string
	if decodeMember(fields, "error", &code) && validCode(code) {
		r.Code = code
	} else if decodeMember(top, "error", &code) && validCode(code) {
		r.Code = code
	}
	var saturated bool
	if decodeMember(top, "treg_saturated", &saturated) && saturated {
		r.Saturated = true
	}
	r.BalanceMicro = microMember(fields, top, "balance_micro")
	r.EstimatedCostMicro = microMember(fields, top, "estimated_cost_micro")
	r.MaxCostMicro = microMember(fields, top, "max_cost_micro")
	r.ChargedMicro = microMember(fields, top, "charged_micro")
	r.RetryAfterSeconds = secondsMember(fields, top, "retry_after_s")
	var ref string
	if decodeMember(fields, "call_id", &ref) && validCallRef(ref) {
		r.CallRef = ref
	}
	if *r == (Refusal{}) {
		return nil
	}
	return r
}

// microMember returns the integer member key of fields, else of top.
func microMember(fields, top jsonObject, key string) *MicroUSD {
	for _, m := range [...]jsonObject{fields, top} {
		if raw, ok := m.member(key); ok {
			if v, valid := parseJSONInt(raw); valid {
				return new(MicroUSD(v))
			}
		}
	}
	return nil
}

// secondsMember returns the non-negative number member key, rounded up to
// whole seconds.
func secondsMember(fields, top jsonObject, key string) *int64 {
	for _, m := range [...]jsonObject{fields, top} {
		raw, ok := m.member(key)
		if !ok {
			continue
		}
		raw = bytes.TrimSpace(raw)
		if !isJSONNumber(raw) {
			continue
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || f < 0 || f > float64(math.MaxInt32) {
			continue
		}
		return new(int64(math.Ceil(f)))
	}
	return nil
}

// knownRefusalCode reports whether code is in the compiled allowlist of
// refusal codes that Treg documents or that its source (pinned revision
// 7b118d2) sends.
func knownRefusalCode(code string) bool {
	switch code {
	case "invalid_target", "catalog_parameter_invalid", "method_mismatch", "insufficient_balance",
		"route_max_cost", "tool_access_denied", "policy_denied", "capability_pinned", "tag_blocked",
		"metadata_pin_mismatch", "target_not_found", "credential_missing", "route_not_found", "unknown_endpoint",
		"idempotency_in_progress", "target_ambiguous", "idempotency_response_lost", "idempotency_outcome_unknown",
		"idempotency_response_too_large", "idempotency_mismatch", "metadata_invalid", "route_verify_unsupported",
		"authorization_required", "authorization_missing", "authorization_expired", "authorization_scope_required",
		"authorization_resource_required", "org_traffic_paused", "org_rate_limit", "org_concurrency_limit",
		"daily_cap_reached", "public_demo_rate_limited", "trial_allowance_reached", "trial_allowance_unavailable",
		"platform_daily_cap_reached", "platform_cap_unavailable", "tag_spend_cap_reached", "tag_call_cap_reached",
		"tag_budget_unavailable", "money_admission_timeout", "share_busy", "hub_busy", "connect_failed",
		"read_timeout", "injection_failed", "ssrf_refused", "refresh_failed", "response_buffer_limit",
		"route_failed", "response_redacted", "provider_capacity_unavailable", "provider_paused",
		"route_no_candidate":
		return true
	}
	return false
}
