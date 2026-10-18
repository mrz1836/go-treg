package treg

import (
	"bytes"
	"context"
	"encoding/json"
)

// Catalog request bounds.
const (
	maxQueryBytes   = 500
	maxSearchLimit  = 100
	maxAddedWithin  = 365
	maxAuthMethod   = 64
	catalogPathRoot = "/catalog/"
)

// ServerInfoRequest is the request of CatalogClient.ServerInfo. It has no
// fields; it exists so that the method can grow without breaking callers.
type ServerInfoRequest struct{}

// ServerInfo is GET /meta: the deployment's public settings. PublicURL is
// informational: the SDK never sends a request there.
type ServerInfo struct {
	PublicURL       string                     `json:"public_url"`
	GitHub          bool                       `json:"github"`
	Google          bool                       `json:"google"`
	AppVersion      string                     `json:"app_version"`
	TregVersion     string                     `json:"treg_version"`
	Hub             bool                       `json:"hub"`
	PausedProviders map[string]PausedProvider  `json:"paused_providers"`
	Referral        Optional[ReferralSettings] `json:"referral,omitzero"`
}

// PausedProvider is a provider Treg has paused.
type PausedProvider struct {
	DisplayName string `json:"display_name"`
	Message     string `json:"message"`
}

// ReferralSettings are the referral credits.
type ReferralSettings struct {
	ReferrerMicro MicroUSD `json:"referrer_micro"`
	ReferredMicro MicroUSD `json:"referred_micro"`
}

// ServerInfo reads GET /meta. It accepts the CaptureResponse option.
func (c *CatalogClient) ServerInfo(ctx context.Context, req ServerInfoRequest, opts ...CallOption) (
	*Result[ServerInfo], error,
) {
	return read[ServerInfo](ctx, c.reader(), req.route, opts)
}

// route returns the request of CatalogClient.ServerInfo.
func (ServerInfoRequest) route() (readRequest, error) {
	return readRequest{op: OpServerInfo, rawPath: "/meta"}, nil
}

// EndpointKind is a catalog row's kind: data, action, account, utility or
// routed. The set is open; Known reports a listed value.
type EndpointKind string

// Endpoint kinds.
const (
	EndpointKindData    EndpointKind = "data"
	EndpointKindAction  EndpointKind = "action"
	EndpointKindAccount EndpointKind = "account"
	EndpointKindUtility EndpointKind = "utility"
	EndpointKindRouted  EndpointKind = "routed"
)

// Known reports whether k is one of the listed kinds.
func (k EndpointKind) Known() bool {
	switch k {
	case EndpointKindData, EndpointKindAction, EndpointKindAccount, EndpointKindUtility, EndpointKindRouted:
		return true
	}
	return false
}

// CostType is a price model: free, per_call, per_result, per_success or
// quota_rows. The set is open; Known reports a listed value.
type CostType string

// Cost types.
const (
	CostFree       CostType = "free"
	CostPerCall    CostType = "per_call"
	CostPerResult  CostType = "per_result"
	CostPerSuccess CostType = "per_success"
	CostQuotaRows  CostType = "quota_rows"
)

// Known reports whether t is one of the listed cost types.
func (t CostType) Known() bool {
	switch t {
	case CostFree, CostPerCall, CostPerResult, CostPerSuccess, CostQuotaRows:
		return true
	}
	return false
}

// EndpointID is a catalog endpoint id, direct or routed, such as
// "moz.web.url.metrics" or "treg.web.search". The models name endpoints by
// it, and CatalogEndpoint and the catalog reads take it, so an id read from
// the catalog can be passed on as it is. A request checks its shape (see
// CatalogEndpoint); a decoded id is kept as received.
type EndpointID string

// Endpoint is a catalog row. It is a list price and a description, never a
// quote: the charge is what Treg reports on the call. Input and TestRequest
// are Treg's own bespoke descriptor (not JSON Schema) and stay raw, like the
// other open fragments.
type Endpoint struct {
	ID              EndpointID   `json:"id"`
	Provider        string       `json:"provider"`
	ProviderDisplay string       `json:"provider_display"`
	Name            string       `json:"name"`
	Summary         string       `json:"summary"`
	Method          string       `json:"method"`
	Path            string       `json:"path"`
	Scope           string       `json:"scope"` // "", "any_account" or "own_account"
	Tier            string       `json:"tier"`  // "core" or "extended"
	Kind            EndpointKind `json:"kind"`
	Domain          string       `json:"domain"`
	CallTemplate    string       `json:"call_template"`

	Input         json.RawMessage `json:"input"`
	TestRequest   json.RawMessage `json:"test_request"`
	StrictQuery   bool            `json:"strict_query,omitempty"`
	StrictBody    bool            `json:"strict_body,omitempty"`
	BodyAllowlist bool            `json:"body_allowlist,omitempty"`

	Cost             Optional[Cost]            `json:"cost,omitzero"`
	Async            Optional[AsyncDescriptor] `json:"async,omitzero"`
	PlatformEligible Optional[bool]            `json:"platform_eligible,omitzero"`
	PlatformAuth     Optional[string]          `json:"platform_auth,omitzero"`
	PlatformBlocked  Optional[string]          `json:"platform_blocked,omitzero"`

	Miss     Optional[EndpointOutcome] `json:"miss,omitzero"`
	NotFound Optional[EndpointOutcome] `json:"not_found,omitzero"`

	// Status, StatusNote and SupersededBy describe a retired (tombstoned)
	// row. The SDK never follows SupersededBy.
	Status       Optional[string]     `json:"status,omitzero"`
	StatusNote   Optional[string]     `json:"status_note,omitzero"`
	SupersededBy Optional[EndpointID] `json:"superseded_by,omitzero"`

	Verified   Optional[string] `json:"verified,omitzero"` // a date
	Added      Optional[string] `json:"added,omitzero"`    // a date
	DocsURL    string           `json:"docs_url"`
	HasExample bool             `json:"has_example"`

	RoutedChildren []EndpointID    `json:"routed_children,omitempty"`
	ChildrenHidden json.RawMessage `json:"children_hidden,omitempty"`

	Capability            Optional[string]  `json:"capability,omitzero"`
	CapabilityDescription Optional[string]  `json:"capability_description,omitzero"`
	Platform              Optional[string]  `json:"platform,omitzero"`
	PlatformLabel         Optional[string]  `json:"platform_label,omitzero"`
	Score                 Optional[Decimal] `json:"score,omitzero"`
	Observed              json.RawMessage   `json:"observed,omitempty"`
	Reviews               json.RawMessage   `json:"reviews,omitempty"`

	// OAuth rows only, kept raw.
	AuthorizationMethod  json.RawMessage `json:"authorization_method,omitempty"`
	AuthorizationMethods json.RawMessage `json:"authorization_methods,omitempty"`
	AuthorizationPaths   json.RawMessage `json:"authorization_paths,omitempty"`
	RequiredScopes       json.RawMessage `json:"required_scopes,omitempty"`
	RequiredResource     json.RawMessage `json:"required_resource,omitempty"`
	TokenType            json.RawMessage `json:"token_type,omitempty"`
}

// EndpointOutcome describes what a status means for a row: a miss or a
// not-found answer.
type EndpointOutcome struct {
	Status int64  `json:"status"`
	Means  string `json:"means"`
}

// Cost is a catalog list price: the typed head of Treg's cost block, with
// the whole block, rate tables and settlement rules included, in Raw. USD
// amounts are Decimal: display text, never money to compute with.
type Cost struct {
	Type       CostType          `json:"type"`
	Value      Optional[Decimal] `json:"value,omitzero"`
	Currency   Optional[string]  `json:"currency,omitzero"`
	Per        Optional[Decimal] `json:"per,omitzero"`
	Unit       Optional[string]  `json:"unit,omitzero"`
	USD        Optional[Decimal] `json:"usd,omitzero"`
	USDMin     Optional[Decimal] `json:"usd_min,omitzero"`
	Source     Optional[string]  `json:"source,omitzero"`
	SourceURL  Optional[string]  `json:"source_url,omitzero"`
	Checked    Optional[string]  `json:"checked,omitzero"`
	Confidence Optional[string]  `json:"confidence,omitzero"`
	Note       Optional[string]  `json:"note,omitzero"`
	Varies     Optional[bool]    `json:"varies,omitzero"`

	// Raw is the whole cost block as received.
	Raw json.RawMessage `json:"-"`
}

// costFields is Cost without its methods, for decoding.
type costFields Cost

// UnmarshalJSON decodes the typed head and keeps the whole block in Raw.
func (c *Cost) UnmarshalJSON(b []byte) error {
	if c == nil {
		return errNilTarget
	}
	var f costFields
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	f.Raw = json.RawMessage(bytes.Clone(bytes.TrimSpace(b)))
	*c = Cost(f)
	return nil
}

// MarshalJSON encodes Raw when it is set, else the typed head.
func (c Cost) MarshalJSON() ([]byte, error) {
	if len(c.Raw) > 0 && json.Valid(c.Raw) {
		return c.Raw, nil
	}
	return json.Marshal(costFields(c))
}

// PlatformsRequest is the request of CatalogClient.Platforms.
type PlatformsRequest struct{}

// PlatformList is GET /catalog/platforms.
type PlatformList struct {
	Platforms     []PlatformSummary `json:"platforms"`
	Providers     map[string]string `json:"providers"` // service: display name
	GeneratedFrom string            `json:"generated_from"`
}

// PlatformSummary is one platform of the catalog. Capabilities, Endpoints
// and Verified are counts.
type PlatformSummary struct {
	Slug         string          `json:"slug"`
	Label        string          `json:"label"`
	Category     string          `json:"category"`
	Featured     Optional[int64] `json:"featured,omitzero"`
	Summary      string          `json:"summary"`
	PriceFrom    Optional[Cost]  `json:"price_from,omitzero"`
	Capabilities int64           `json:"capabilities"`
	Endpoints    int64           `json:"endpoints"`
	Verified     int64           `json:"verified"`
	Providers    []string        `json:"providers"`
}

// Platforms reads GET /catalog/platforms. It accepts the CaptureResponse
// option.
func (c *CatalogClient) Platforms(ctx context.Context, req PlatformsRequest, opts ...CallOption) (
	*Result[PlatformList], error,
) {
	return read[PlatformList](ctx, c.reader(), req.route, opts)
}

// route returns the request of CatalogClient.Platforms.
func (PlatformsRequest) route() (readRequest, error) {
	return readRequest{op: OpCatalogPlatforms, rawPath: catalogPathRoot + "platforms"}, nil
}

// PlatformRequest is the request of CatalogClient.Platform.
type PlatformRequest struct {
	// Slug names the platform, such as "voice-gen". Required: 1 to 128
	// bytes of letters, digits and "._~-".
	Slug string

	// IncludeHidden adds the hidden account and utility rows.
	IncludeHidden bool
}

// PlatformDetail is GET /catalog/platforms/{slug}.
type PlatformDetail struct {
	Platform     PlatformRef             `json:"platform"`
	Capabilities []PlatformCapability    `json:"capabilities"`
	Domains      []PlatformDomain        `json:"domains"`
	Extended     []json.RawMessage       `json:"extended"`
	HiddenCount  int64                   `json:"hidden_count"`
	Providers    map[string]ProviderInfo `json:"providers"`
}

// PlatformRef names a platform.
type PlatformRef struct {
	Slug     string `json:"slug"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// PlatformCapability is one capability of a platform and its endpoints.
type PlatformCapability struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Endpoints   []Endpoint `json:"endpoints"`
}

// PlatformDomain groups a platform's rows by domain.
type PlatformDomain struct {
	Domain string        `json:"domain"`
	Rows   []PlatformRow `json:"rows"`
}

// PlatformRow is one row of a platform domain.
type PlatformRow struct {
	Kind        string     `json:"kind"`
	Capability  string     `json:"capability"`
	Description string     `json:"description"`
	Domain      string     `json:"domain"`
	Endpoints   []Endpoint `json:"endpoints"`
}

// ProviderInfo describes a provider.
type ProviderInfo struct {
	Service     string           `json:"service"`
	DisplayName string           `json:"display_name"`
	Limits      Optional[string] `json:"limits,omitzero"`
	PricingURL  Optional[string] `json:"pricing_url,omitzero"`
	Docs        Optional[string] `json:"docs,omitzero"`
	AuthKind    Optional[string] `json:"auth_kind,omitzero"`
	Metered     Optional[bool]   `json:"metered,omitzero"`
}

// Platform reads GET /catalog/platforms/{slug}. It accepts the
// CaptureResponse option.
func (c *CatalogClient) Platform(ctx context.Context, req PlatformRequest, opts ...CallOption) (
	*Result[PlatformDetail], error,
) {
	return read[PlatformDetail](ctx, c.reader(), req.route, opts)
}

// route validates the request of CatalogClient.Platform.
func (r PlatformRequest) route() (readRequest, error) {
	var q readQuery
	q.segment("Slug", r.Slug, checkName)
	q.setIf(r.IncludeHidden, "include_hidden", "1")
	return q.get(OpCatalogPlatform, catalogPathRoot+"platforms/"+r.Slug)
}

// ProviderRequest is the request of CatalogClient.Provider.
type ProviderRequest struct {
	// Service names the provider, such as "fishaudio". Required: 1 to 128
	// bytes of letters, digits and "._~-".
	Service string
}

// ProviderDetail is GET /catalog/providers/{service}.
type ProviderDetail struct {
	Provider  ProviderInfo       `json:"provider"`
	Platforms []ProviderPlatform `json:"platforms"`
}

// ProviderPlatform is one platform a provider serves, with its endpoints.
type ProviderPlatform struct {
	Slug  string     `json:"slug"`
	Label string     `json:"label"`
	Tools []Endpoint `json:"tools"`
}

// Provider reads GET /catalog/providers/{service}. It accepts the
// CaptureResponse option.
func (c *CatalogClient) Provider(ctx context.Context, req ProviderRequest, opts ...CallOption) (
	*Result[ProviderDetail], error,
) {
	return read[ProviderDetail](ctx, c.reader(), req.route, opts)
}

// route validates the request of CatalogClient.Provider.
func (r ProviderRequest) route() (readRequest, error) {
	var q readQuery
	q.segment("Service", r.Service, checkName)
	return q.get(OpCatalogProvider, catalogPathRoot+"providers/"+r.Service)
}

// SearchRequest is the request of CatalogClient.Search. Zero numeric fields
// are omitted, so Treg's defaults apply; out-of-range values are refused,
// never clamped.
type SearchRequest struct {
	// Query is the search text: at most 500 bytes of UTF-8 without control
	// characters. Empty lists everything.
	Query string

	// Limit is 1 to 100 results; 0 means Treg's default (25).
	Limit int64

	// AddedWithinDays keeps rows added in the last 1 to 365 days.
	AddedWithinDays int64

	// Sort is SearchSortBest or SearchSortNewest; empty means Treg's
	// default.
	Sort SearchSort
}

// SearchSort is the order of CatalogClient.Search, best or newest, on both
// the request (SearchRequest.Sort) and the response, which echoes it
// (SearchResults.Sort). On the request the set is closed: Search refuses
// any other value. The echo decodes whatever value Treg sends.
type SearchSort string

// Search orders.
const (
	SearchSortBest   SearchSort = "best"
	SearchSortNewest SearchSort = "newest"
)

// SearchResults is GET /catalog/search. Ranking is advice: the SDK never
// picks or runs a result.
type SearchResults struct {
	Query           string               `json:"query"`
	Count           int64                `json:"count"`
	Total           int64                `json:"total"`
	Results         []Endpoint           `json:"results"`
	Hints           []string             `json:"hints"`
	Sort            Optional[SearchSort] `json:"sort,omitzero"`
	AddedWithinDays Optional[int64]      `json:"added_within_days,omitzero"`
	CappedAtDays    Optional[int64]      `json:"capped_at_days,omitzero"`
	LedBy           json.RawMessage      `json:"led_by,omitempty"`
	Near            json.RawMessage      `json:"near,omitempty"`
}

// Search reads GET /catalog/search. It accepts the CaptureResponse option.
func (c *CatalogClient) Search(ctx context.Context, req SearchRequest, opts ...CallOption) (
	*Result[SearchResults], error,
) {
	return read[SearchResults](ctx, c.reader(), req.route, opts)
}

// route validates the request of CatalogClient.Search.
func (r SearchRequest) route() (readRequest, error) {
	var q readQuery
	q.text("Query", "q", r.Query, maxQueryBytes)
	q.intIn("Limit", "limit", r.Limit, maxSearchLimit)
	q.intIn("AddedWithinDays", "added_within_days", r.AddedWithinDays, maxAddedWithin)
	oneOf(&q, "Sort", "sort", r.Sort, SearchSortBest, SearchSortNewest)
	return q.get(OpCatalogSearch, catalogPathRoot+"search")
}

// EndpointRequest is the request of CatalogClient.Endpoint.
type EndpointRequest struct {
	// ID is a catalog endpoint id, with the shape CatalogEndpoint requires.
	ID EndpointID
}

// EndpointDescriptor is GET /catalog/endpoints/{id}: one row with its
// provider, siblings, routing and example. ExampleResponse and Routing stay
// raw; the example is provider data.
type EndpointDescriptor struct {
	Endpoint            Endpoint            `json:"endpoint"`
	Provider            ProviderInfo        `json:"provider"`
	Siblings            []Endpoint          `json:"siblings"`
	ObservedPending     Optional[bool]      `json:"observed_pending,omitzero"`
	RelatedCapabilities []RelatedCapability `json:"related_capabilities,omitempty"`
	Routing             json.RawMessage     `json:"routing,omitempty"`
	CallTemplate        string              `json:"call_template"`
	ExampleResponse     json.RawMessage     `json:"example_response"`
	Hints               []string            `json:"hints"`
}

// RelatedCapability points from a row to a related capability.
type RelatedCapability struct {
	EndpointID EndpointID `json:"endpoint_id"`
	Capability string     `json:"capability"`
	Does       string     `json:"does"`
}

// Endpoint reads GET /catalog/endpoints/{id}. Discovery never executes: it
// makes no /call/ request. It accepts the CaptureResponse option.
func (c *CatalogClient) Endpoint(ctx context.Context, req EndpointRequest, opts ...CallOption) (
	*Result[EndpointDescriptor], error,
) {
	return read[EndpointDescriptor](ctx, c.reader(), req.route, opts)
}

// route validates the request of CatalogClient.Endpoint.
func (r EndpointRequest) route() (readRequest, error) {
	var q readQuery
	q.endpointID(r.ID)
	return q.get(OpCatalogEndpoint, catalogPathRoot+"endpoints/"+string(r.ID))
}

// ExampleRequest is the request of CatalogClient.Example.
type ExampleRequest struct {
	// ID is a catalog endpoint id, with the shape CatalogEndpoint requires.
	ID EndpointID
}

// Example reads GET /catalog/examples/{id}: a row's example response,
// provider data kept byte for byte (one JSON value, without surrounding
// whitespace). It is not in Treg's OpenAPI document. It accepts the
// CaptureResponse option.
func (c *CatalogClient) Example(ctx context.Context, req ExampleRequest, opts ...CallOption) (
	*Result[json.RawMessage], error,
) {
	return read[json.RawMessage](ctx, c.reader(), req.route, opts)
}

// route validates the request of CatalogClient.Example. Its body is
// provider data, kept byte for byte without the strict rules.
func (r ExampleRequest) route() (readRequest, error) {
	var q readQuery
	q.endpointID(r.ID)
	rr, err := q.get(OpCatalogExample, catalogPathRoot+"examples/"+string(r.ID))
	rr.verbatim = true
	return rr, err
}

// AccessTier is the access tier of EndpointAccess: none, restricted, tool,
// credential, anonymous, platform or routed. The set is open.
type AccessTier string

// Access tiers.
const (
	AccessNone       AccessTier = "none"
	AccessRestricted AccessTier = "restricted"
	AccessTool       AccessTier = "tool"
	AccessCredential AccessTier = "credential"
	AccessAnonymous  AccessTier = "anonymous"
	AccessPlatform   AccessTier = "platform"
	AccessRouted     AccessTier = "routed"
)

// Known reports whether t is one of the listed tiers.
func (t AccessTier) Known() bool {
	switch t {
	case AccessNone, AccessRestricted, AccessTool, AccessCredential, AccessAnonymous, AccessPlatform, AccessRouted:
		return true
	}
	return false
}

// EndpointAccessRequest is the request of Client.EndpointAccess.
type EndpointAccessRequest struct {
	// ID is a catalog endpoint id, with the shape CatalogEndpoint requires.
	ID EndpointID

	// AuthorizationMethod selects an OAuth grant, for rows that have
	// several: 1 to 64 bytes of letters, digits and "._~-". Optional.
	AuthorizationMethod string
}

// Access is GET /catalog/endpoints/{id}/access: how the caller's team would
// reach a row, and an estimate. It is a preview, never a quote or a
// reservation.
type Access struct {
	Tier                AccessTier         `json:"tier"`
	Detail              Optional[string]   `json:"detail,omitzero"`
	Metered             Optional[bool]     `json:"metered,omitzero"`
	EstimatedCostMicro  Optional[MicroUSD] `json:"estimated_cost_micro,omitzero"`
	EstimatedCostUSD    Optional[Decimal]  `json:"estimated_cost_usd,omitzero"`
	EstimatedCostUSDMin Optional[Decimal]  `json:"estimated_cost_usd_min,omitzero"`
	AuthorizationMethod Optional[string]   `json:"authorization_method,omitzero"`
	ConnectCapability   Optional[string]   `json:"connect_capability,omitzero"`
	ConnectCommand      Optional[string]   `json:"connect_command,omitzero"`
	ActionLabel         Optional[string]   `json:"action_label,omitzero"`
	MissingMessage      Optional[string]   `json:"missing_message,omitzero"`
	Plan                json.RawMessage    `json:"plan,omitempty"`
	Dropped             json.RawMessage    `json:"dropped,omitempty"`
}

// EndpointAccess reads GET /catalog/endpoints/{id}/access with the token.
// It is not in Treg's OpenAPI document. It accepts the CaptureResponse
// option.
func (c *Client) EndpointAccess(ctx context.Context, req EndpointAccessRequest, opts ...CallOption) (
	*Result[Access], error,
) {
	return read[Access](ctx, c.reader(), req.route, opts)
}

// route validates the request of Client.EndpointAccess.
func (r EndpointAccessRequest) route() (readRequest, error) {
	var q readQuery
	q.endpointID(r.ID)
	q.check("AuthorizationMethod", "authorization_method", r.AuthorizationMethod, checkAuthMethod)
	return q.get(OpEndpointAccess, catalogPathRoot+"endpoints/"+string(r.ID)+"/access")
}

// checkAuthMethod returns "" for an authorization method, 1 to 64 bytes of
// letters, digits and "._~-", else a reason.
func checkAuthMethod(m string) string { return checkBounded(m, maxAuthMethod, isUnreserved) }
