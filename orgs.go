package treg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// Org request bounds.
const (
	maxBalanceLimit = 200
	maxUsageDays    = 365
	orgsPathRoot    = "/orgs/"
)

// OrgsService reads one team's money and usage: GET /orgs/{org_id}/...
// Get it from Client.Orgs; the zero OrgsService returns ErrInvalidConfig.
// Usage, Spend, UsageByTag and TagKeys need an admin or owner; Balance shows
// credit blocks and ledger entries to admins only. Treg answers 403 when the
// org id is not the token's team.
type OrgsService struct {
	c *Client
}

// Orgs returns the team reads.
func (c *Client) Orgs() OrgsService { return OrgsService{c: c} }

// BalanceRequest is the request of OrgsService.Balance.
type BalanceRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64

	// Limit is 1 to 200 ledger entries; 0 means Treg's default (20).
	Limit int64

	// Offset skips ledger entries; it must not be negative.
	Offset int64
}

// Balance is GET /orgs/{org_id}/balance. BalanceUSD and the other *USD
// fields are display text: compute with the micro fields. Blocks and
// Entries.Items are empty unless the caller is an admin or owner.
type Balance struct {
	OrgID           int64              `json:"org_id"`
	BalanceMicro    MicroUSD           `json:"balance_micro"`
	BalanceUSD      Optional[Decimal]  `json:"balance_usd,omitzero"`
	PromoGrantMicro Optional[MicroUSD] `json:"promo_grant_micro,omitzero"`
	Blocks          []CreditBlock      `json:"blocks"`
	Holds           []BalanceHold      `json:"holds"`
	Entries         BalanceEntries     `json:"entries"`
}

// CreditBlock is a block of credit.
type CreditBlock struct {
	ID             int64             `json:"id"`
	Kind           string            `json:"kind"`
	AmountMicro    MicroUSD          `json:"amount_micro"`
	RemainingMicro MicroUSD          `json:"remaining_micro"`
	RemainingUSD   Optional[Decimal] `json:"remaining_usd,omitzero"`
	Currency       Optional[string]  `json:"currency,omitzero"`
	ExpiresAt      Optional[string]  `json:"expires_at,omitzero"`
	CreatedAt      string            `json:"created_at"`
}

// BalanceHold is money held for a call in flight.
type BalanceHold struct {
	CallRef     Optional[string]     `json:"call_id,omitzero"`
	EndpointID  Optional[EndpointID] `json:"endpoint_id,omitzero"`
	AmountMicro MicroUSD             `json:"amount_micro"`
	CreatedAt   string               `json:"created_at"`
}

// BalanceEntries is one page of the ledger.
type BalanceEntries struct {
	Limit  int64          `json:"limit"`
	Offset int64          `json:"offset"`
	Items  []BalanceEntry `json:"items"`
}

// BalanceEntry is one ledger entry. Meta stays raw.
type BalanceEntry struct {
	ID          int64                `json:"id"`
	Kind        string               `json:"kind"`
	AmountMicro MicroUSD             `json:"amount_micro"`
	AmountUSD   Optional[Decimal]    `json:"amount_usd,omitzero"`
	BlockID     Optional[int64]      `json:"block_id,omitzero"`
	CallRef     Optional[string]     `json:"call_id,omitzero"`
	EndpointID  Optional[EndpointID] `json:"endpoint_id,omitzero"`
	Meta        json.RawMessage      `json:"meta,omitempty"`
	CreatedAt   string               `json:"created_at"`
}

// Balance reads GET /orgs/{org_id}/balance. A balance is not a reservation
// or a budget: the per-call reserve ceiling (Controls.MaxCost) is the only
// hard cap Treg enforces. It accepts the CaptureResponse option.
func (s OrgsService) Balance(ctx context.Context, req BalanceRequest, opts ...CallOption) (*Result[Balance], error) {
	return read[Balance](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.Balance.
func (r BalanceRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	q.intIn("Limit", "limit", r.Limit, maxBalanceLimit)
	q.nonNegative("Offset", "offset", r.Offset)
	return q.get(OpOrgBalance, orgPath(r.OrgID, "balance"))
}

// OrgUsageRequest is the request of OrgsService.Usage.
type OrgUsageRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64

	// Days is the window, 1 to 365 days; 0 means Treg's default (30).
	Days int64
}

// OrgUsage is GET /orgs/{org_id}/usage: call counts by member, tool and
// day, and the spend. ByUser holds email addresses.
type OrgUsage struct {
	Days   int64                     `json:"days"`
	Since  string                    `json:"since"`
	Totals UsageTotals               `json:"totals"`
	ByUser []UserUsage               `json:"by_user"`
	ByTool []NamedTotal              `json:"by_tool"`
	ByDay  []DayTotal                `json:"by_day"`
	Spend  Optional[OrgSpendSummary] `json:"spend,omitzero"`
}

// UsageTotals counts calls and runs.
type UsageTotals struct {
	Call      int64 `json:"call"`
	LocalRun  int64 `json:"local_run"`
	ServerRun int64 `json:"server_run"`
	Total     int64 `json:"total"`
}

// UserUsage is one member's usage. UserEmail is personal data, so a
// UserUsage prints its totals only.
type UserUsage struct {
	UserEmail string `json:"user_email"`
	Call      int64  `json:"call"`
	LocalRun  int64  `json:"local_run"`
	ServerRun int64  `json:"server_run"`
	Total     int64  `json:"total"`
}

// String prints the totals only.
func (u UserUsage) String() string {
	return "treg.UserUsage{call: " + strconv.FormatInt(u.Call, 10) + ", localRun: " + strconv.FormatInt(u.LocalRun, 10) +
		", serverRun: " + strconv.FormatInt(u.ServerRun, 10) + ", total: " + strconv.FormatInt(u.Total, 10) + "}"
}

// GoString returns the same text as String.
func (u UserUsage) GoString() string { return u.String() }

// Format prints String for every verb.
func (u UserUsage) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, u.String()) }

// MarshalJSON encodes the totals only.
func (u UserUsage) MarshalJSON() ([]byte, error) {
	return json.Marshal(UsageTotals{Call: u.Call, LocalRun: u.LocalRun, ServerRun: u.ServerRun, Total: u.Total})
}

// NamedTotal counts the calls of one tool.
type NamedTotal struct {
	Name  string `json:"name"`
	Total int64  `json:"total"`
}

// DayTotal counts the calls of one day.
type DayTotal struct {
	Day   string `json:"day"`
	Total int64  `json:"total"`
}

// OrgSpendSummary is a team's settled spend in a window.
type OrgSpendSummary struct {
	SpendMicro  MicroUSD          `json:"spend_micro"`
	SpendUSD    Optional[Decimal] `json:"spend_usd,omitzero"`
	BilledCalls int64             `json:"billed_calls"`
}

// Usage reads GET /orgs/{org_id}/usage. It accepts the CaptureResponse
// option.
func (s OrgsService) Usage(ctx context.Context, req OrgUsageRequest, opts ...CallOption) (*Result[OrgUsage], error) {
	return read[OrgUsage](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.Usage.
func (r OrgUsageRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	q.intIn("Days", "days", r.Days, maxUsageDays)
	return q.get(OpOrgUsage, orgPath(r.OrgID, "usage"))
}

// SpendRequest is the request of OrgsService.Spend.
type SpendRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64

	// From and To bound the window as YYYY-MM-DD dates; empty means Treg's
	// default (the last 30 days).
	From string
	To   string

	// Group is SpendGroupDay, SpendGroupWeek or SpendGroupMonth; empty
	// means Treg's default.
	Group SpendGroup

	// KeyID keeps one API key's spend; 0 means every key.
	KeyID int64

	// Provider keeps one provider's spend: 1 to 64 bytes of letters,
	// digits and "._-".
	Provider string

	// Stack is SpendStackKey or SpendStackTool, or empty; SpendStackTool
	// needs Provider.
	Stack SpendStack
}

// SpendGroup is the bucket of OrgsService.Spend, day, week or month, on
// both the request (SpendRequest.Group) and the response, which echoes it
// (Spend.Group). On the request the set is closed: Spend refuses any other
// value. The echo decodes whatever value Treg sends.
type SpendGroup string

// Spend buckets.
const (
	SpendGroupDay   SpendGroup = "day"
	SpendGroupWeek  SpendGroup = "week"
	SpendGroupMonth SpendGroup = "month"
)

// SpendStack is how OrgsService.Spend stacks the spend, by key or by tool,
// on both the request (SpendRequest.Stack) and the response, which echoes
// it (Spend.Stack). On the request the set is closed: Spend refuses any
// other value. The echo decodes whatever value Treg sends.
type SpendStack string

// Spend stackings.
const (
	SpendStackKey  SpendStack = "key"
	SpendStackTool SpendStack = "tool"
)

// Spend is GET /orgs/{org_id}/usage/spend. Until Treg's rollup has caught
// up, Ready is false and only HistoryFrom is set. Series, Buckets and
// Ranking stay raw.
type Spend struct {
	Ready       bool                   `json:"ready"`
	HistoryFrom Optional[string]       `json:"history_from,omitzero"`
	From        Optional[string]       `json:"from,omitzero"`
	To          Optional[string]       `json:"to,omitzero"`
	Group       Optional[SpendGroup]   `json:"group,omitzero"`
	Stack       Optional[SpendStack]   `json:"stack,omitzero"`
	SpentMicro  Optional[MicroUSD]     `json:"spent_micro,omitzero"`
	Charges     Optional[int64]        `json:"charges,omitzero"`
	Series      json.RawMessage        `json:"series,omitempty"`
	Buckets     json.RawMessage        `json:"buckets,omitempty"`
	Ranking     json.RawMessage        `json:"ranking,omitempty"`
	Options     Optional[SpendOptions] `json:"options,omitzero"`
}

// SpendOptions lists the keys and providers that spent in the window.
type SpendOptions struct {
	Keys      []SpendKey      `json:"keys"`
	Providers []SpendProvider `json:"providers"`
}

// SpendKey is one API key's spend.
type SpendKey struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	SpentMicro MicroUSD `json:"spent_micro"`
}

// SpendProvider is one provider's spend.
type SpendProvider struct {
	ID         string           `json:"id"`
	Name       Optional[string] `json:"name,omitzero"`
	SpentMicro MicroUSD         `json:"spent_micro"`
}

// Spend reads GET /orgs/{org_id}/usage/spend. It accepts the
// CaptureResponse option.
func (s OrgsService) Spend(ctx context.Context, req SpendRequest, opts ...CallOption) (*Result[Spend], error) {
	return read[Spend](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.Spend.
func (r SpendRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	q.date("From", "from", r.From)
	q.date("To", "to", r.To)
	oneOf(&q, "Group", "group", r.Group, SpendGroupDay, SpendGroupWeek, SpendGroupMonth)
	q.id("KeyID", "key", r.KeyID)
	q.check("Provider", "provider", r.Provider, checkProvider)
	oneOf(&q, "Stack", "stack", r.Stack, SpendStackKey, SpendStackTool)
	q.refuse(r.Stack == SpendStackTool && r.Provider == "", "Stack", reasonConflict)
	return q.get(OpOrgSpend, orgPath(r.OrgID, "usage/spend"))
}

// UsageByTagRequest is the request of OrgsService.UsageByTag.
type UsageByTagRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64

	// Key is the tag key, 1 to 32 bytes of [a-z0-9_]; empty means the
	// team's primary key.
	Key string

	// Days is the window, 1 to 365 days; 0 means Treg's default (30).
	Days int64
}

// TagUsage is GET /orgs/{org_id}/usage/by-tag: settled spend per value of
// one tag, from the ledger. Attributed plus unattributed equals total.
type TagUsage struct {
	Key               string            `json:"key"`
	Days              int64             `json:"days"`
	Since             string            `json:"since"`
	Rows              []TagUsageRow     `json:"rows"`
	AttributedMicro   MicroUSD          `json:"attributed_micro"`
	UnattributedMicro MicroUSD          `json:"unattributed_micro"`
	TotalMicro        MicroUSD          `json:"total_micro"`
	TotalUSD          Optional[Decimal] `json:"total_usd,omitzero"`
}

// TagUsageRow is the spend of one tag value.
type TagUsageRow struct {
	Value        string            `json:"value"`
	ChargedMicro MicroUSD          `json:"charged_micro"`
	ChargedUSD   Optional[Decimal] `json:"charged_usd,omitzero"`
	Calls        int64             `json:"calls"`
}

// UsageByTag reads GET /orgs/{org_id}/usage/by-tag. It accepts the
// CaptureResponse option.
func (s OrgsService) UsageByTag(ctx context.Context, req UsageByTagRequest, opts ...CallOption) (*Result[TagUsage], error) {
	return read[TagUsage](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.UsageByTag.
func (r UsageByTagRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	q.check("Key", "key", r.Key, checkTagKey)
	q.intIn("Days", "days", r.Days, maxUsageDays)
	return q.get(OpOrgUsageByTag, orgPath(r.OrgID, "usage/by-tag"))
}

// TagKeysRequest is the request of OrgsService.TagKeys.
type TagKeysRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64
}

// TagKeys is GET /orgs/{org_id}/tag-keys: the tag keys the team has sent,
// the ones it may budget on, and its primary key.
type TagKeys struct {
	Seen       []string         `json:"seen"`
	Budgetable []string         `json:"budgetable"`
	Primary    Optional[string] `json:"primary,omitzero"`
}

// TagKeys reads GET /orgs/{org_id}/tag-keys. It accepts the CaptureResponse
// option.
func (s OrgsService) TagKeys(ctx context.Context, req TagKeysRequest, opts ...CallOption) (*Result[TagKeys], error) {
	return read[TagKeys](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.TagKeys.
func (r TagKeysRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	return q.get(OpOrgTagKeys, orgPath(r.OrgID, "tag-keys"))
}

// ProviderResourcesRequest is the request of OrgsService.ProviderResources.
type ProviderResourcesRequest struct {
	// OrgID is the team's numeric id. Required.
	OrgID int64

	// Provider and Kind filter the resources, such as "fishaudio" and
	// "voice": 1 to 64 bytes of letters, digits and "._-", or empty.
	Provider string
	Kind     string

	// IncludeDeleted lists deleted resources too.
	IncludeDeleted bool

	// Source is ResourceSourceAuto or ResourceSourcePlatform; empty means
	// Treg's default (auto), which may list a provider account the team
	// connected itself.
	Source ResourceSource
}

// ResourceSource is where OrgsService.ProviderResources lists from: auto or
// platform. The set is closed: ProviderResources refuses any other value.
type ResourceSource string

// Resource sources.
const (
	ResourceSourceAuto     ResourceSource = "auto"
	ResourceSourcePlatform ResourceSource = "platform"
)

// ProviderResource is a durable resource, such as a cloned voice, created
// through a provider account. ID is an integer for a resource Treg
// recorded, and the provider's own id (a string) for one listed from the
// team's own provider account, so it stays raw. DisplayName and CreatedBy may
// be personal data, so a ProviderResource prints its provider, kind and
// status only.
type ProviderResource struct {
	ID            json.RawMessage  `json:"id"`
	Provider      string           `json:"provider"`
	Kind          string           `json:"kind"`
	UpstreamID    Optional[string] `json:"upstream_id,omitzero"`
	DisplayName   string           `json:"display_name"`
	CreatedBy     Optional[string] `json:"created_by,omitzero"`
	SourceCallRef Optional[string] `json:"source_call_id,omitzero"`
	Status        string           `json:"status"`
	CreatedAt     Optional[string] `json:"created_at,omitzero"`
	UpdatedAt     Optional[string] `json:"updated_at,omitzero"`
	DeletedAt     Optional[string] `json:"deleted_at,omitzero"`
}

// String prints the provider, kind and status only: the display name and
// creator may be personal data.
func (r ProviderResource) String() string {
	return "treg.ProviderResource{provider: " + printable(r.Provider, validProvider, "?") +
		", kind: " + printable(r.Kind, validProvider, "?") +
		", status: " + printable(r.Status, validProvider, "?") + "}"
}

// GoString returns the same text as String.
func (r ProviderResource) GoString() string { return r.String() }

// Format prints String for every verb.
func (r ProviderResource) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, r.String()) }

// MarshalJSON encodes the type only.
func (ProviderResource) MarshalJSON() ([]byte, error) {
	return json.Marshal(shapeJSON{Type: "ProviderResource"})
}

// ProviderResources reads GET /orgs/{org_id}/provider-resources. It accepts
// the CaptureResponse option.
func (s OrgsService) ProviderResources(ctx context.Context, req ProviderResourcesRequest, opts ...CallOption) (
	*Result[[]ProviderResource], error,
) {
	return read[[]ProviderResource](ctx, s.c.reader(), req.route, opts)
}

// route validates the request of OrgsService.ProviderResources.
func (r ProviderResourcesRequest) route() (readRequest, error) {
	var q readQuery
	q.orgID(r.OrgID)
	q.check("Provider", "provider", r.Provider, checkProvider)
	q.check("Kind", "kind", r.Kind, checkProvider)
	q.setIf(r.IncludeDeleted, "include_deleted", "true")
	oneOf(&q, "Source", "source", r.Source, ResourceSourceAuto, ResourceSourcePlatform)
	return q.get(OpOrgProviderResources, orgPath(r.OrgID, "provider-resources"))
}

// orgPath returns /orgs/{id}/{rest}.
func orgPath(id int64, rest string) string {
	return orgsPathRoot + strconv.FormatInt(id, 10) + "/" + rest
}
