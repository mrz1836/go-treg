package treg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// AccountService reads the caller's own identity and usage: GET /auth/me,
// /orgs and /usage/me. Get it from Client.Account; the zero AccountService
// returns ErrInvalidConfig.
type AccountService struct {
	c *Client
}

// Account returns the account reads.
func (c *Client) Account() AccountService { return AccountService{c: c} }

// MeRequest is the request of AccountService.Me.
type MeRequest struct{}

// Identity is GET /auth/me: who the token belongs to. OrgID, Org and Role
// are present when the caller authenticated with a token. It is personal
// data, so it prints only whether those fields are set.
type Identity struct {
	Email        string           `json:"email"`
	IsSuperadmin bool             `json:"is_superadmin"`
	Onboarded    bool             `json:"onboarded"`
	GitHub       Optional[string] `json:"github,omitzero"`
	AvatarURL    Optional[string] `json:"avatar_url,omitzero"`
	Uploads      Optional[bool]   `json:"uploads,omitzero"`
	OrgID        Optional[int64]  `json:"org_id,omitzero"`
	Org          Optional[string] `json:"org,omitzero"`
	Role         Optional[string] `json:"role,omitzero"`
}

// String prints the shape only.
func (i Identity) String() string {
	_, hasOrg := i.OrgID.Get()
	return "treg.Identity{org: " + strconv.FormatBool(hasOrg) + "}"
}

// GoString returns the same text as String.
func (i Identity) GoString() string { return i.String() }

// Format prints String for every verb.
func (i Identity) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, i.String()) }

// MarshalJSON encodes the shape only.
func (i Identity) MarshalJSON() ([]byte, error) { return json.Marshal(shapeJSON{Type: "Identity"}) }

// Me reads GET /auth/me. It accepts the CaptureResponse option.
func (s AccountService) Me(ctx context.Context, req MeRequest, opts ...CallOption) (*Result[Identity], error) {
	return read[Identity](ctx, s.c.reader(), req.route, opts)
}

// route returns the request of AccountService.Me.
func (MeRequest) route() (readRequest, error) {
	return readRequest{op: OpAuthMe, rawPath: "/auth/me"}, nil
}

// AccountOrgsRequest is the request of AccountService.Orgs.
type AccountOrgsRequest struct{}

// Org is one team the caller belongs to.
type Org struct {
	OrgID     int64            `json:"org_id"`
	Slug      string           `json:"slug"`
	Name      string           `json:"name"`
	Role      string           `json:"role"`
	Active    bool             `json:"active"`
	Demo      Optional[bool]   `json:"demo,omitzero"`
	ToolCount Optional[int64]  `json:"tool_count,omitzero"`
	AvatarURL Optional[string] `json:"avatar_url,omitzero"`
}

// Orgs reads GET /orgs: the caller's teams. Only identity tokens may list
// them; Treg refuses per-org and agent tokens. It accepts the
// CaptureResponse option.
func (s AccountService) Orgs(ctx context.Context, req AccountOrgsRequest, opts ...CallOption) (*Result[[]Org], error) {
	return read[[]Org](ctx, s.c.reader(), req.route, opts)
}

// route returns the request of AccountService.Orgs.
func (AccountOrgsRequest) route() (readRequest, error) {
	return readRequest{op: OpListOrgs, rawPath: "/orgs"}, nil
}

// MemberUsageRequest is the request of AccountService.Usage.
type MemberUsageRequest struct{}

// MemberUsage is GET /usage/me: the caller's calls today and daily cap. Cap
// is -1 when unlimited.
type MemberUsage struct {
	Org       string `json:"org"`
	UsedToday int64  `json:"used_today"`
	Cap       int64  `json:"cap"`
}

// Unlimited reports whether the member has no daily cap.
func (u MemberUsage) Unlimited() bool { return u.Cap < 0 }

// Usage reads GET /usage/me. It accepts the CaptureResponse option.
func (s AccountService) Usage(ctx context.Context, req MemberUsageRequest, opts ...CallOption) (
	*Result[MemberUsage], error,
) {
	return read[MemberUsage](ctx, s.c.reader(), req.route, opts)
}

// route returns the request of AccountService.Usage.
func (MemberUsageRequest) route() (readRequest, error) {
	return readRequest{op: OpUsageMe, rawPath: "/usage/me"}, nil
}
