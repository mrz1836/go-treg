// Package urlmetrics is an example of an external adapter: a small typed
// package, written outside go-treg and using only its public API, that wraps
// one Treg catalog endpoint, "moz.web.url.metrics" (Moz Domain Authority,
// Page Authority and Spam Score for up to 50 URLs or domains).
//
// It shows that a typed integration needs no fork of the SDK, no registry,
// no reflection, no runtime schema fetch, no copy of the Treg token and no
// new dependency: it owns its request and response types and calls
// treg.CallJSON with treg.CatalogEndpoint.
//
// The types follow the catalog descriptor whose "verified" date is
// 2026-07-31 (see Verified). Only the fields this package needs are modeled:
// the descriptor's optional body fields (distributions and the history
// lists, each of which adds quota rows) are left out, and unknown response
// fields are ignored, so additive changes on Treg's side are safe.
//
// The endpoint is metered: its cost type is quota_rows, one row per target,
// and Treg reports the charge in X-Treg-Cost-Micro, which arrives as
// Result.Meta.CostMicro. Which provider to use (this id, one of its catalog
// siblings or a routed treg.* id), which targets to look up and how to rank
// the results are the caller's choice: the package never substitutes an id,
// sorts, filters or retries.
package urlmetrics

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mrz1836/go-treg"
)

const (
	// EndpointID is the catalog id this package calls.
	EndpointID = "moz.web.url.metrics"

	// Verified is the catalog descriptor's "verified" date this package was
	// written against. Compare it with Endpoint.Verified from
	// CatalogClient.Endpoint to notice drift.
	Verified = "2026-07-31"

	// MaxTargets is the most targets one call accepts, from the descriptor:
	// "1-50 URLs or domains".
	MaxTargets = 50
)

// ErrTargets reports a Request whose Targets is empty, holds more than
// MaxTargets entries, or holds a blank entry. Nothing was sent.
var ErrTargets = errors.New("urlmetrics: 1 to 50 targets")

// Request is the JSON body of moz.web.url.metrics.
type Request struct {
	// Targets lists 1 to 50 URLs or domains; a bare domain is treated as
	// that page. Each target is one quota row.
	Targets []string `json:"targets"`
}

// Response is the JSON answer of moz.web.url.metrics: one Metrics per
// target.
type Response struct {
	Results []Metrics `json:"results"`
}

// Metrics is one target's row. Moz may send any field as null: a null number
// stays nil, kept apart from zero, and a null string reads as "".
type Metrics struct {
	Page                    string   `json:"page"`
	Subdomain               string   `json:"subdomain"`
	RootDomain              string   `json:"root_domain"`
	Title                   string   `json:"title"`
	LastCrawled             string   `json:"last_crawled"`
	HTTPCode                *int     `json:"http_code"`
	PageAuthority           *int     `json:"page_authority"`
	DomainAuthority         *int     `json:"domain_authority"`
	SpamScore               *int     `json:"spam_score"`
	LinkPropensity          *float64 `json:"link_propensity"`
	RootDomainsToRootDomain *int64   `json:"root_domains_to_root_domain"`
}

// Validate applies this package's rule: 1 to MaxTargets targets, none blank.
// It returns ErrTargets or nil.
func (r Request) Validate() error {
	if len(r.Targets) == 0 || len(r.Targets) > MaxTargets {
		return ErrTargets
	}
	for _, t := range r.Targets {
		if strings.TrimSpace(t) == "" {
			return ErrTargets
		}
	}
	return nil
}

// Lookup makes one POST to moz.web.url.metrics and decodes the answer.
//
// Parameters:
//   - ctx: bounds the call, with the Client's own timeouts
//   - c: a Client from treg.NewClient; the adapter never sees the token
//   - in: the targets, checked with Validate before anything is sent
//   - ctl: Treg's controls, passed through unchanged; set MaxCost to cap the
//     reserve and IdempotencyKey to make a recovery attempt safe. The
//     package never invents either
//
// Returns:
//   - the decoded Response with Treg's metadata (CallRef, CostMicro)
//   - ErrTargets when in breaks the package's rule; nothing was sent
//   - the SDK's error otherwise, unwrapped: a *treg.ValidationError (nothing
//     sent) or a *treg.CallError. A 402 or other refusal carries the parsed
//     Refusal; a 5xx matches treg.ErrOutcomeUnknown, so reconcile it through
//     Client.Calls().Get before repeating; a 2xx that fails to decode
//     executed and must not be resubmitted
func Lookup(ctx context.Context, c *treg.Client, in Request, ctl treg.Controls) (*treg.Result[Response], error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return treg.CallJSON[Response](ctx, c, treg.CallRequest{
		Target:   treg.CatalogEndpoint(EndpointID),
		Method:   http.MethodPost,
		Body:     treg.JSONBody(in),
		Controls: ctl,
	})
}
