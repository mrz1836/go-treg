package treg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// accountReads returns every read of AccountService, each with a valid
// request.
func accountReads() []namedRead[treg.AccountService] {
	return []namedRead[treg.AccountService]{
		{"Me", bind(treg.AccountService.Me, treg.MeRequest{}).erased()},
		{"Orgs", bind(treg.AccountService.Orgs, treg.AccountOrgsRequest{}).erased()},
		{"Usage", bind(treg.AccountService.Usage, treg.MemberUsageRequest{}).erased()},
	}
}

func TestAccountMeDecodesFixture(t *testing.T) {
	t.Parallel()
	me := bind(treg.AccountService.Me, treg.MeRequest{})
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, proto, synthetic(t, "account/me.json"), me)
			assert.Equal(t, string(proto), r.Proto)
			assertAuthReadWire(t, r, "/auth/me", "")
			assert.Equal(t, treg.OpAuthMe, res.Meta.Op)
			id := res.Value
			assert.Equal(t, authReadEmailMarker, id.Email)
			assert.False(t, id.IsSuperadmin)
			assert.True(t, id.Onboarded)
			assert.Equal(t, treg.Some("ghMARKER"), id.GitHub)
			assert.Equal(t, treg.Some("https://example.com/avatars/AVATARMARKER.png"), id.AvatarURL)
			assert.Equal(t, treg.Some(true), id.Uploads)
			assert.Equal(t, treg.Some[int64](42), id.OrgID)
			assert.Equal(t, treg.Some(authReadOrg), id.Org)
			assert.Equal(t, treg.Some("admin"), id.Role)
			assert.Equal(t, "treg.Identity{org: true}", id.String())
		})
	}
	t.Run("identity token without a team", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "account/me_identity.json"), me)
		id := res.Value
		assert.True(t, id.IsSuperadmin)
		assert.False(t, id.Onboarded)
		assert.Equal(t, treg.Null[string](), id.GitHub)
		assert.Equal(t, treg.Null[string](), id.AvatarURL)
		assert.Equal(t, treg.Some(false), id.Uploads, "false is a value, not absent")
		assert.Equal(t, treg.Optional[int64]{}, id.OrgID, "OrgID is absent")
		assert.Equal(t, treg.Optional[string]{}, id.Org, "Org is absent, which is not null")
		assert.Equal(t, treg.Optional[string]{}, id.Role, "Role is absent")
		assert.Equal(t, "treg.Identity{org: false}", id.String())
	})
}

func TestAccountOrgsDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP2, synthetic(t, "account/orgs.json"),
		bind(treg.AccountService.Orgs, treg.AccountOrgsRequest{}))
	assertAuthReadWire(t, r, "/orgs", "")
	assert.Equal(t, treg.OpListOrgs, res.Meta.Op)
	require.Len(t, res.Value, 2)
	assert.Equal(t, treg.Org{
		OrgID: 42, Slug: authReadOrg, Name: "Acme Team", Role: "admin", Active: true,
		Demo: treg.Some(false), ToolCount: treg.Some[int64](7), AvatarURL: treg.Some("https://example.com/orgs/42.png"),
	}, res.Value[0])
	demo := res.Value[1]
	assert.Equal(t, int64(77), demo.OrgID)
	assert.Equal(t, "member", demo.Role)
	assert.False(t, demo.Active)
	assert.Equal(t, treg.Some(true), demo.Demo)
	assert.Equal(t, treg.Some[int64](0), demo.ToolCount, "zero is a value")
	assert.Equal(t, treg.Null[string](), demo.AvatarURL)
}

func TestAccountUsageDecodesFixture(t *testing.T) {
	t.Parallel()
	usage := bind(treg.AccountService.Usage, treg.MemberUsageRequest{})
	t.Run("capped", func(t *testing.T) {
		t.Parallel()
		res, r := authReadBody(t, testutil.HTTP1, synthetic(t, "account/usage.json"), usage)
		assertAuthReadWire(t, r, "/usage/me", "")
		assert.Equal(t, treg.OpUsageMe, res.Meta.Op)
		assert.Equal(t, treg.MemberUsage{Org: authReadOrg, UsedToday: 17, Cap: 500}, res.Value)
		assert.False(t, res.Value.Unlimited())
	})
	t.Run("unlimited", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "account/usage_unlimited.json"), usage)
		assert.Equal(t, int64(-1), res.Value.Cap)
		assert.Equal(t, int64(1234), res.Value.UsedToday)
		assert.True(t, res.Value.Unlimited())
	})
}

func TestMemberUsageUnlimited(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		limit int64
		want  bool
	}{
		{"minus one", -1, true},
		{"zero cap", 0, false},
		{"positive cap", 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, treg.MemberUsage{Cap: tc.limit}.Unlimited())
		})
	}
}

func TestAccountWireOrgHeader(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fixture, want string
		read                anyRead[treg.AccountService]
	}{
		{"Me", "account/me.json", "/auth/me", bind(treg.AccountService.Me, treg.MeRequest{}).erased()},
		{"Orgs", "account/orgs.json", "/orgs", bind(treg.AccountService.Orgs, treg.AccountOrgsRequest{}).erased()},
		{"Usage", "account/usage.json", "/usage/me", bind(treg.AccountService.Usage, treg.MemberUsageRequest{}).erased()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAuthReadWire(t, synthetic(t, tc.fixture), tc.read, tc.want)
		})
	}
}

func TestAccountZeroValue(t *testing.T) {
	t.Parallel()
	requireAuthReadsNotInitialized(t.Context(), t, accountReads())
}

func TestAccountOptionsAndContext(t *testing.T) {
	t.Parallel()
	requireAuthReadsRefuseOptions(t.Context(), t, accountReads())
}

func TestAccountStatusErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		read anyRead[treg.AccountService]
		step testutil.Step
		want callErrorWant
		text string
	}{
		{
			"Me 401", bind(treg.AccountService.Me, treg.MeRequest{}).erased(),
			testutil.Step{Status: http.StatusUnauthorized, Body: `{"detail":"not authenticated"}`},
			callErrorWant{Op: treg.OpAuthMe, Kind: treg.FailureHTTPStatus, Status: http.StatusUnauthorized, Marked: new(false)},
			"treg: auth_me: http_status (HTTP 401)",
		},
		{
			"Orgs 403 marked", bind(treg.AccountService.Orgs, treg.AccountOrgsRequest{}).erased(),
			testutil.Step{
				Status: http.StatusForbidden, Header: gatewayHeader(), Body: `{"detail":"an identity token is required"}`,
			},
			callErrorWant{Op: treg.OpListOrgs, Kind: treg.FailureHTTPStatus, Status: http.StatusForbidden, Marked: new(true)},
			"treg: list_orgs: http_status (HTTP 403, gateway)",
		},
		{
			"Usage 503 never outcome unknown", bind(treg.AccountService.Usage, treg.MemberUsageRequest{}).erased(),
			testutil.Step{Status: http.StatusServiceUnavailable, Header: gatewayHeader(), Body: `{"detail":"unavailable"}`},
			callErrorWant{
				Op: treg.OpUsageMe, Kind: treg.FailureHTTPStatus, Status: http.StatusServiceUnavailable, Marked: new(true),
			},
			"treg: usage_me: http_status (HTTP 503, gateway)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := authReadStepErr(t, tc.step, tc.read)
			ce := requireCallError(t, err, tc.want)
			assert.Nil(t, ce.Refusal, "a prose detail is not a refusal code")
			assert.Equal(t, tc.text, err.Error())
		})
	}
}

func TestAccountMalformedResponses(t *testing.T) {
	t.Parallel()
	me := bind(treg.AccountService.Me, treg.MeRequest{}).erased()
	orgs := bind(treg.AccountService.Orgs, treg.AccountOrgsRequest{}).erased()
	usage := bind(treg.AccountService.Usage, treg.MemberUsageRequest{}).erased()
	malformed := func(op treg.Operation, reason string) callErrorWant {
		return callErrorWant{Op: op, Kind: treg.FailureMalformedResponse, Reason: reason, Status: http.StatusOK}
	}
	for _, tc := range []struct {
		name, body string
		read       anyRead[treg.AccountService]
		want       callErrorWant
	}{
		{
			"Me duplicate key", `{"email":"a@example.com","email":"b@example.com","is_superadmin":false,"onboarded":true}`,
			me, malformed(treg.OpAuthMe, "duplicate_key"),
		},
		{
			"Me duplicate key differing in case", `{"email":"a@example.com","EMAIL":"b@example.com"}`,
			me, malformed(treg.OpAuthMe, "duplicate_key"),
		},
		{"Me quoted org id", `{"email":"a@example.com","org_id":"42"}`, me, malformed(treg.OpAuthMe, "decode")},
		{"Me string boolean", `{"email":"a@example.com","onboarded":"true"}`, me, malformed(treg.OpAuthMe, "decode")},
		{"Me array body", `[]`, me, malformed(treg.OpAuthMe, "decode")},
		{"Orgs object body", `{"orgs":[]}`, orgs, malformed(treg.OpListOrgs, "decode")},
		{"Orgs duplicate key in an org", `[{"org_id":1,"slug":"a","slug":"b"}]`, orgs, malformed(treg.OpListOrgs, "duplicate_key")},
		{"Orgs quoted tool count", `[{"org_id":1,"tool_count":"7"}]`, orgs, malformed(treg.OpListOrgs, "decode")},
		{"Usage quoted count", `{"org":"acme-team","used_today":"17","cap":500}`, usage, malformed(treg.OpUsageMe, "decode")},
		{"Usage fractional cap", `{"org":"acme-team","used_today":17,"cap":1.5}`, usage, malformed(treg.OpUsageMe, "decode")},
		{
			"Usage duplicate cap", `{"org":"acme-team","used_today":17,"cap":500,"cap":-1}`,
			usage, malformed(treg.OpUsageMe, "duplicate_key"),
		},
		{
			"Usage empty body", ``, usage,
			callErrorWant{Op: treg.OpUsageMe, Kind: treg.FailureUnexpectedResponse, Reason: "empty_body", Status: http.StatusOK},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallError(t, authReadStepErr(t, testutil.Step{Body: tc.body}, tc.read), tc.want)
		})
	}
}

func TestAccountIdentityPrivacy(t *testing.T) {
	t.Parallel()
	markers := []string{authReadEmailMarker, "ghMARKER", "AVATARMARKER"}
	res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "account/me.json"), bind(treg.AccountService.Me, treg.MeRequest{}))
	id := res.Value
	t.Run("prints its shape", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, authReadEmailMarker, id.Email, "the data stays accessible")
		require.Equal(t, treg.Some("ghMARKER"), id.GitHub)
		testutil.AssertNeverPrints(t, &id, markers...)
		testutil.AssertNeverPrints(t, res, markers...)
		testutil.AssertNeverPrints(t, []treg.Identity{id}, markers...)
		b, err := json.Marshal(id)
		require.NoError(t, err)
		assert.JSONEq(t, `{"type":"Identity"}`, string(b))
	})
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run("prints its shape with "+verb, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, "treg.Identity{org: true}", fmt.Sprintf(verb, id))
		})
	}
	t.Run("every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &id, markers...)
	})
}
