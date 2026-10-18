package treg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
)

// Calls request bounds.
const (
	maxCallsLimit = 500
	maxCallsDays  = 365
	maxRefBytes   = 256
)

// CallsService reads the team's call audit log: GET /calls,
// /calls/{call_ref} and /calls/{call_id}/result. Get it from Client.Calls;
// the zero CallsService returns ErrInvalidConfig.
type CallsService struct {
	c *Client
}

// Calls returns the call audit reads.
func (c *Client) Calls() CallsService { return CallsService{c: c} }

// ListCallsRequest is the request of CallsService.List. Zero fields are
// omitted, so Treg's defaults apply; out-of-range values are refused, never
// clamped.
type ListCallsRequest struct {
	// Limit is 1 to 500 rows; 0 means Treg's default (50).
	Limit int64

	// Days is the window, 1 to 365 days ending today; 0 means Treg's
	// default.
	Days int64

	// BeforeID returns rows with an id below it: the next page. 0 means
	// the newest rows.
	BeforeID int64

	// APIKeyID keeps the rows of one API key; 0 means every key.
	APIKeyID int64
}

// CallPage is one page of GET /calls, newest first. Async poll rows are not
// listed. It is analytics, not an invoice: money is reconciled through Get.
type CallPage []CallRecord

// NextRequest returns prev with BeforeID set to the last row's id, and true.
// It returns prev and false when the page is empty or its last row's id is
// not positive: there is no cursor, and a BeforeID of 0 would ask for the
// newest rows again. It builds the request only: the SDK never pages on its
// own.
func (p CallPage) NextRequest(prev ListCallsRequest) (ListCallsRequest, bool) {
	if len(p) == 0 || p[len(p)-1].ID <= 0 {
		return prev, false
	}
	next := prev
	next.BeforeID = p[len(p)-1].ID
	return next, true
}

// CallRecord is one audited call. UserEmail is personal data, so a
// CallRecord prints only its id, status and call ref.
type CallRecord struct {
	ID                 int64                       `json:"id"`
	CallRef            string                      `json:"call_ref"`
	UserEmail          string                      `json:"user_email"`
	ToolName           string                      `json:"tool_name"`
	Method             string                      `json:"method"`
	Path               string                      `json:"path"`
	StatusCode         int64                       `json:"status_code"`
	Kind               string                      `json:"kind"`
	Client             string                      `json:"client"`
	APIKeyID           Optional[int64]             `json:"api_key_id,omitzero"`
	APIKeyName         Optional[string]            `json:"api_key_name,omitzero"`
	APIKeyPrefix       Optional[string]            `json:"api_key_prefix,omitzero"`
	EndpointID         Optional[EndpointID]        `json:"endpoint_id,omitzero"`
	Provider           Optional[string]            `json:"provider,omitzero"`
	CredentialTier     Optional[string]            `json:"credential_tier,omitzero"`
	Cached             Optional[bool]              `json:"cached,omitzero"`
	HasResult          Optional[bool]              `json:"has_result,omitzero"`
	CostEstimatedMicro Optional[MicroUSD]          `json:"cost_estimated_micro,omitzero"`
	CostObservedMicro  Optional[MicroUSD]          `json:"cost_observed_micro,omitzero"`
	CostChargedMicro   Optional[MicroUSD]          `json:"cost_charged_micro,omitzero"` // null while an async task is pending
	DurationMS         Optional[int64]             `json:"duration_ms,omitzero"`
	ResponseBytes      Optional[int64]             `json:"response_bytes,omitzero"`
	ParamsHash         Optional[string]            `json:"params_hash,omitzero"`
	RefusedBy          Optional[string]            `json:"refused_by,omitzero"` // set when Treg refused before relaying
	BudgetDim          Optional[string]            `json:"budget_dim,omitzero"`
	BudgetVal          Optional[string]            `json:"budget_val,omitzero"`
	Tags               Optional[map[string]string] `json:"tags,omitzero"`
	CreatedAt          string                      `json:"created_at"`
	AsyncTask          json.RawMessage             `json:"async_task,omitempty"`
}

// String prints the id, the status and the call ref only.
func (r CallRecord) String() string {
	return "treg.CallRecord{id: " + strconv.FormatInt(r.ID, 10) + ", status: " + strconv.FormatInt(r.StatusCode, 10) +
		safeRef(r.CallRef) + "}"
}

// GoString returns the same text as String.
func (r CallRecord) GoString() string { return r.String() }

// Format prints String for every verb.
func (r CallRecord) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the fields String prints.
func (r CallRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{
		Type: "CallRecord", ID: &r.ID, StatusCode: r.StatusCode, CallRef: printable(r.CallRef, validCallRef, ""),
	})
}

// List reads GET /calls. It accepts the CaptureResponse option.
func (s CallsService) List(ctx context.Context, req ListCallsRequest, opts ...CallOption) (*Result[CallPage], error) {
	return read[CallPage](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of CallsService.List.
func (r ListCallsRequest) route() (readRequest, error) {
	var q readQuery
	q.intIn("Limit", "limit", r.Limit, maxCallsLimit)
	q.intIn("Days", "days", r.Days, maxCallsDays)
	q.id("BeforeID", "before_id", r.BeforeID)
	q.id("APIKeyID", "api_key_id", r.APIKeyID)
	return q.get(OpListCalls, "/calls")
}

// GetCallRequest is the request of CallsService.Get.
type GetCallRequest struct {
	// Ref is a call reference (Metadata.CallRef), a routed child's
	// included: 1 to 256 bytes of visible ASCII. Required.
	Ref string
}

// CallDetail is GET /calls/{call_ref}: the audit row and the ledger's view
// of the call. ChargedMicro is what the ledger settled: reconcile money
// here, not from response headers.
type CallDetail struct {
	Call         CallRecord         `json:"call"`
	AsyncTask    json.RawMessage    `json:"async_task,omitempty"`
	Ledger       []LedgerEntry      `json:"ledger"`
	ChargedMicro Optional[MicroUSD] `json:"charged_micro,omitzero"`
}

// LedgerEntry is one money movement of a call.
type LedgerEntry struct {
	Kind        string               `json:"kind"`
	AmountMicro MicroUSD             `json:"amount_micro"`
	EndpointID  Optional[EndpointID] `json:"endpoint_id,omitzero"`
	CreatedAt   string               `json:"created_at"`
}

// Get reads GET /calls/{call_ref}. A 404 means Treg has no audit row and no
// ledger entry for the ref, not that the call never happened. It accepts
// the CaptureResponse option.
func (s CallsService) Get(ctx context.Context, req GetCallRequest, opts ...CallOption) (*Result[CallDetail], error) {
	return read[CallDetail](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of CallsService.Get.
func (r GetCallRequest) route() (readRequest, error) {
	var q readQuery
	q.segment("Ref", r.Ref, checkRef)
	return q.get(OpGetCall, "/calls/"+url.PathEscape(r.Ref))
}

// CallResultRequest is the request of CallsService.Result.
type CallResultRequest struct {
	// Ref is a call reference or a numeric audit row id: 1 to 256 bytes of
	// visible ASCII. Required.
	Ref string
}

// StoredResult is GET /calls/{call_id}/result: the stored request and answer
// of a metered platform 2xx call. When Stored is false, Note says why.
// Bodies are personal or provider data, so a StoredResult prints only its
// id, status and stored flag.
type StoredResult struct {
	ID             int64                    `json:"id"`
	CallRef        string                   `json:"call_ref"`
	EndpointID     Optional[EndpointID]     `json:"endpoint_id,omitzero"`
	Provider       Optional[string]         `json:"provider,omitzero"`
	CredentialTier Optional[string]         `json:"credential_tier,omitzero"`
	Method         string                   `json:"method"`
	Path           string                   `json:"path"`
	StatusCode     int64                    `json:"status_code"`
	Cached         Optional[bool]           `json:"cached,omitzero"`
	CreatedAt      string                   `json:"created_at"`
	Stored         bool                     `json:"stored"`
	Note           Optional[string]         `json:"note,omitzero"`
	Request        Optional[StoredRequest]  `json:"request,omitzero"`
	Response       Optional[StoredResponse] `json:"response,omitzero"`
}

// StoredRequest is the stored request, before credential injection. Its URL
// and body are request data, so it prints its method only.
type StoredRequest struct {
	Method   string           `json:"method"`
	URL      string           `json:"url"`
	BodyText Optional[string] `json:"body_text,omitzero"`
}

// StoredResponse is the stored answer. Its body is provider data, so it
// prints its status only.
type StoredResponse struct {
	StatusCode int64            `json:"status_code"`
	MediaType  Optional[string] `json:"media_type,omitzero"`
	SizeBytes  Optional[int64]  `json:"size_bytes,omitzero"`
	FetchedAt  Optional[string] `json:"fetched_at,omitzero"`
	Origin     Optional[string] `json:"origin,omitzero"`
	Version    json.RawMessage  `json:"version,omitempty"`
	BodyText   Optional[string] `json:"body_text,omitzero"`
}

// String prints the method and the presence of a body only.
func (r StoredRequest) String() string {
	_, hasBody := r.BodyText.Get()
	return "treg.StoredRequest{method: " + printable(r.Method, validMethod, "?") + ", body: " + strconv.FormatBool(hasBody) + "}"
}

// GoString returns the same text as String.
func (r StoredRequest) GoString() string { return r.String() }

// Format prints String for every verb.
func (r StoredRequest) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the type only.
func (StoredRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{Type: "StoredRequest"})
}

// String prints the status and the presence of a body only.
func (r StoredResponse) String() string {
	_, hasBody := r.BodyText.Get()
	return "treg.StoredResponse{status: " + strconv.FormatInt(r.StatusCode, 10) + ", body: " + strconv.FormatBool(hasBody) + "}"
}

// GoString returns the same text as String.
func (r StoredResponse) GoString() string { return r.String() }

// Format prints String for every verb.
func (r StoredResponse) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the status only.
func (r StoredResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{Type: "StoredResponse", StatusCode: r.StatusCode})
}

// String prints the id, the status and the stored flag only.
func (r StoredResult) String() string {
	return "treg.StoredResult{id: " + strconv.FormatInt(r.ID, 10) + ", status: " + strconv.FormatInt(r.StatusCode, 10) +
		", stored: " + strconv.FormatBool(r.Stored) + safeRef(r.CallRef) + "}"
}

// GoString returns the same text as String.
func (r StoredResult) GoString() string { return r.String() }

// Format prints String for every verb.
func (r StoredResult) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the fields String prints.
func (r StoredResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{
		Type: "StoredResult", ID: &r.ID, StatusCode: r.StatusCode, Stored: new(r.Stored),
		CallRef: printable(r.CallRef, validCallRef, ""),
	})
}

// Result reads GET /calls/{call_id}/result. It accepts the CaptureResponse
// option.
func (s CallsService) Result(ctx context.Context, req CallResultRequest, opts ...CallOption) (*Result[StoredResult], error) {
	return read[StoredResult](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of CallsService.Result.
func (r CallResultRequest) route() (readRequest, error) {
	var q readQuery
	q.segment("Ref", r.Ref, checkRef)
	return q.get(OpCallResult, "/calls/"+url.PathEscape(r.Ref)+"/result")
}
