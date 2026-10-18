package treg

import (
	"bytes"
	"encoding/json"
	"errors"
)

// errNotRoutedEnvelope reports a body without a "_treg" object.
var errNotRoutedEnvelope = errors.New("treg: body is not a routed envelope")

// RouteOutcome is a routed call's outcome. It is an open set: compare with
// the constants, and use Known to detect a value this SDK release does not
// list.
type RouteOutcome string

// Route outcomes.
const (
	// RouteOutcomeHit is a full answer.
	RouteOutcomeHit RouteOutcome = "hit"

	// RouteOutcomeWeak is a partial answer.
	RouteOutcomeWeak RouteOutcome = "weak"

	// RouteOutcomeMiss is no answer (HTTP 200, with the output's key field
	// null). A miss may still be charged: see RoutedInfo.ChargedMicro.
	RouteOutcomeMiss RouteOutcome = "miss"

	// RouteOutcomePending is an asynchronous child still running (HTTP
	// 202). Keep RoutedInfo.CallRef and RoutedInfo.Async, never resubmit,
	// and check on it later with your own explicit call.
	RouteOutcomePending RouteOutcome = "pending"
)

// Known reports whether o is one of the outcome constants.
func (o RouteOutcome) Known() bool {
	switch o {
	case RouteOutcomeHit, RouteOutcomeWeak, RouteOutcomeMiss, RouteOutcomePending:
		return true
	}
	return false
}

// Routed is the envelope of a routed call (a treg.* catalog id the caller
// chose): the normalized Output, the serving provider's Raw document byte
// for byte, and Treg's _treg block. Decode it with
// CallJSON[treg.Routed[O]], where O is your own type for the output.
//
// A routed 202 (outcome pending) is a success, not an error. The SDK never
// substitutes a routed id, picks a provider, falls back or polls.
//
// Only _treg is checked strictly (no repeated keys); Output and Raw are
// provider data.
type Routed[O any] struct {
	Output O               `json:"output"`
	Raw    json.RawMessage `json:"raw"`
	Treg   RoutedInfo      `json:"_treg"`
}

// UnmarshalJSON decodes a routed envelope. It requires a "_treg" object.
func (r *Routed[O]) UnmarshalJSON(b []byte) error {
	if r == nil {
		return errNilTarget
	}
	members, err := objectMembers(b)
	if err != nil {
		return err
	}
	info, ok := members.member("_treg")
	if !ok || !isJSONObject(info) {
		return errNotRoutedEnvelope
	}
	if err := checkStrict(info, nil); err != nil {
		return err
	}
	var out Routed[O]
	if err := json.Unmarshal(info, &out.Treg); err != nil {
		return err
	}
	if raw, found := members.member("output"); found {
		if err := json.Unmarshal(raw, &out.Output); err != nil {
			return err
		}
	}
	if raw, found := members.member("raw"); found {
		out.Raw = json.RawMessage(bytes.Clone(raw))
	}
	*r = out
	return nil
}

// RoutedInfo is the _treg block of a routed envelope.
type RoutedInfo struct {
	// ServedBy is the catalog id of the child that served, or null on a
	// miss.
	ServedBy Optional[EndpointID] `json:"served_by,omitzero"`

	// Provider is the serving provider, or null.
	Provider Optional[string] `json:"provider,omitzero"`

	// Tier is the credential tier that served.
	Tier string `json:"tier"`

	// Outcome is the routed outcome.
	Outcome RouteOutcome `json:"outcome"`

	// Tried lists every child attempt, in order.
	Tried []RouteAttempt `json:"tried"`

	// ChargedMicro is the total charge. It is null while pending: unknown,
	// not zero.
	ChargedMicro Optional[MicroUSD] `json:"charged_micro,omitzero"`

	// ReservedMicro is the amount held for a pending child.
	ReservedMicro Optional[MicroUSD] `json:"reserved_micro,omitzero"`

	// CallRef is the pending child's call reference.
	CallRef string `json:"call_ref"`

	// Async is the pending child's task, on a 202.
	Async Optional[RoutedAsync] `json:"async,omitzero"`

	// Verification, Advice, IgnoredFilters and Dropped are kept raw.
	Verification   json.RawMessage `json:"verification"`
	Advice         json.RawMessage `json:"advice"`
	IgnoredFilters json.RawMessage `json:"ignored_filters"`
	Dropped        json.RawMessage `json:"dropped"`

	// Capped reports that the routed call stopped at its cost ceiling.
	Capped bool `json:"capped"`

	// MergedFrom lists the children whose answers were merged.
	MergedFrom []EndpointID `json:"merged_from"`
}

// RouteAttempt is one child attempt of a routed call.
type RouteAttempt struct {
	EndpointID     EndpointID         `json:"endpoint_id"`
	Provider       string             `json:"provider"`
	Outcome        string             `json:"outcome"`
	Status         Optional[int64]    `json:"status,omitzero"`
	ChargedMicro   Optional[MicroUSD] `json:"charged_micro,omitzero"`
	Detail         json.RawMessage    `json:"detail"`
	IgnoredFilters json.RawMessage    `json:"ignored_filters"`
}

// RoutedAsync is a pending child's task: its id, and its poll and status
// descriptors kept raw.
type RoutedAsync struct {
	TaskID string          `json:"task_id"`
	Poll   json.RawMessage `json:"poll"`
	Status json.RawMessage `json:"status"`
}
