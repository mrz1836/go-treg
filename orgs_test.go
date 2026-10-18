package treg_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// orgsReads returns every read of OrgsService, each with a valid request.
func orgsReads() []namedRead[treg.OrgsService] {
	return []namedRead[treg.OrgsService]{
		{"Balance", bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42}).erased()},
		{"Usage", bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}).erased()},
		{"Spend", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}).erased()},
		{"UsageByTag", bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42}).erased()},
		{"TagKeys", bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 42}).erased()},
		{"ProviderResources", bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42}).erased()},
	}
}

func TestOrgsBalanceDecodesFixture(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			res, r := authReadBody(t, proto, synthetic(t, "orgs/balance.json"),
				bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Limit: 5, Offset: 10}))
			assert.Equal(t, string(proto), r.Proto)
			assertAuthReadWire(t, r, "/orgs/42/balance?limit=5&offset=10", "")
			assert.Equal(t, treg.OpOrgBalance, res.Meta.Op)
			assertOrgsAdminBalance(t, res.Value)
		})
	}
	t.Run("member view", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/balance_member.json"),
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42}))
		b := res.Value
		assert.Equal(t, treg.MicroUSD(0), b.BalanceMicro)
		assert.Equal(t, treg.Some(treg.Decimal("0.0")), b.BalanceUSD, "the number text is kept")
		assert.Equal(t, treg.Some(treg.MicroUSD(0)), b.PromoGrantMicro, "zero is a value")
		assert.NotNil(t, b.Blocks)
		assert.Empty(t, b.Blocks, "blocks are for admins only")
		assert.Empty(t, b.Holds)
		assert.Equal(t, int64(20), b.Entries.Limit)
		assert.Equal(t, int64(0), b.Entries.Offset)
		assert.Empty(t, b.Entries.Items, "entries are for admins only")
	})
}

// assertOrgsAdminBalance checks the admin balance fixture.
func assertOrgsAdminBalance(t *testing.T, b treg.Balance) {
	t.Helper()
	assert.Equal(t, int64(42), b.OrgID)
	assert.Equal(t, treg.MicroUSD(12_500_000), b.BalanceMicro)
	assert.Equal(t, treg.Some(treg.Decimal("12.5")), b.BalanceUSD, "12.5 stays 12.5")
	assert.Equal(t, treg.Some(treg.MicroUSD(5_000_000)), b.PromoGrantMicro)
	require.Len(t, b.Blocks, 2)
	assert.Equal(t, treg.CreditBlock{
		ID: 301, Kind: "promo", AmountMicro: 5_000_000, RemainingMicro: 2_500_000,
		RemainingUSD: treg.Some(treg.Decimal("2.50")), Currency: treg.Some("usd"),
		ExpiresAt: treg.Some("2026-12-31T00:00:00Z"), CreatedAt: "2026-09-01T00:00:00Z",
	}, b.Blocks[0])
	assert.Equal(t, treg.Some(treg.Decimal("10")), b.Blocks[1].RemainingUSD)
	assert.Equal(t, treg.Null[string](), b.Blocks[1].ExpiresAt)
	require.Len(t, b.Holds, 1)
	assert.Equal(t, treg.BalanceHold{
		CallRef: treg.Some("8e1d7c6b5a4f3e2d1c0b9a8f7e6d5c4b"), EndpointID: treg.Some[treg.EndpointID]("example.audio.tts"),
		AmountMicro: 3000, CreatedAt: "2026-10-16T12:30:00Z",
	}, b.Holds[0])
	assert.Equal(t, int64(5), b.Entries.Limit)
	assert.Equal(t, int64(10), b.Entries.Offset)
	require.Len(t, b.Entries.Items, 2)
	charge := b.Entries.Items[0]
	assert.Equal(t, int64(7001), charge.ID)
	assert.Equal(t, "charge", charge.Kind)
	assert.Equal(t, treg.MicroUSD(-1500), charge.AmountMicro)
	assert.Equal(t, "-$0.0015", charge.AmountMicro.String())
	assert.Equal(t, treg.Some(treg.Decimal("-0.0015")), charge.AmountUSD)
	assert.Equal(t, treg.Some[int64](302), charge.BlockID)
	assert.Equal(t, treg.Some(authReadCallRef), charge.CallRef)
	assert.Equal(t, treg.Some[treg.EndpointID]("example.web.search"), charge.EndpointID)
	assertExactJSON(t, `{"tags": {"customer": "c-001"}, "note": "synthetic"}`, charge.Meta)
	topup := b.Entries.Items[1]
	assert.Equal(t, treg.Some(treg.Decimal("10.000000")), topup.AmountUSD, "trailing zeros are kept")
	assert.Equal(t, treg.Null[int64](), topup.BlockID)
	assert.Equal(t, treg.Null[string](), topup.CallRef)
	assert.Equal(t, treg.Null[treg.EndpointID](), topup.EndpointID)
	assertExactJSON(t, "{}", topup.Meta)
}

func TestOrgsUsageDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP2, synthetic(t, "orgs/usage.json"),
		bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42, Days: 30}))
	assertAuthReadWire(t, r, "/orgs/42/usage?days=30", "")
	assert.Equal(t, treg.OpOrgUsage, res.Meta.Op)
	u := res.Value
	assert.Equal(t, int64(30), u.Days)
	assert.Equal(t, "2026-09-17", u.Since)
	assert.Equal(t, treg.UsageTotals{Call: 120, LocalRun: 8, ServerRun: 4, Total: 132}, u.Totals)
	assert.Equal(t, []treg.UserUsage{
		{UserEmail: authReadEmailMarker, Call: 100, LocalRun: 8, ServerRun: 0, Total: 108},
		{UserEmail: "second.member@example.com", Call: 20, LocalRun: 0, ServerRun: 4, Total: 24},
	}, u.ByUser)
	assert.Equal(t, []treg.NamedTotal{{Name: "example-search", Total: 90}, {Name: "example-tts", Total: 42}}, u.ByTool)
	assert.Equal(t, []treg.DayTotal{{Day: "2026-10-15", Total: 70}, {Day: "2026-10-16", Total: 62}}, u.ByDay)
	assert.Equal(t, treg.Some(treg.OrgSpendSummary{
		SpendMicro: 4_321_000, SpendUSD: treg.Some(treg.Decimal("4.321")), BilledCalls: 97,
	}), u.Spend)
}

func TestOrgsSpendDecodesFixture(t *testing.T) {
	t.Parallel()
	t.Run("ready", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/spend_ready.json"),
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Group: treg.SpendGroupWeek, Stack: treg.SpendStackKey}))
		assert.Equal(t, treg.OpOrgSpend, res.Meta.Op)
		s := res.Value
		assert.True(t, s.Ready)
		assert.Equal(t, treg.Some("2026-01-01"), s.HistoryFrom)
		assert.Equal(t, treg.Some("2026-10-01"), s.From)
		assert.Equal(t, treg.Some("2026-10-16"), s.To)
		assert.Equal(t, treg.Some(treg.SpendGroupWeek), s.Group)
		assert.Equal(t, treg.Some(treg.SpendStackKey), s.Stack)
		assert.Equal(t, treg.Some(treg.MicroUSD(4_321_000)), s.SpentMicro)
		assert.Equal(t, treg.Some[int64](97), s.Charges)
		assertExactJSON(t, `[{"bucket": "2026-09-28", "total_micro": 1200000, "stack": {"7": 1000000, "8": 200000}}, `+
			`{"bucket": "2026-10-05", "total_micro": 3121000, "stack": {"7": 3121000}}]`, s.Series)
		assertExactJSON(t, `["2026-09-28", "2026-10-05"]`, s.Buckets)
		assertExactJSON(t, `[{"id": 7, "name": "ci-runner", "spent_micro": 4121000}, `+
			`{"id": 8, "name": "laptop", "spent_micro": 200000}]`, s.Ranking)
		opts, ok := s.Options.Get()
		require.True(t, ok)
		assert.Equal(t, []treg.SpendKey{
			{ID: 7, Name: "ci-runner", SpentMicro: 4_121_000}, {ID: 8, Name: "laptop", SpentMicro: 200_000},
		}, opts.Keys)
		require.Len(t, opts.Providers, 2)
		assert.Equal(t, []treg.SpendProvider{
			{ID: "examplecorp", SpentMicro: 4_000_000}, {ID: "voiceco", SpentMicro: 321_000},
		}, opts.Providers)
		assert.Equal(t, treg.Optional[string]{}, opts.Providers[0].Name)
	})
	t.Run("not ready", func(t *testing.T) {
		t.Parallel()
		res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/spend_not_ready.json"),
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}))
		s := res.Value
		assert.False(t, s.Ready)
		assert.Equal(t, treg.Some("2026-01-01"), s.HistoryFrom)
		const absent = "absent until the rollup is ready"
		assert.Equal(t, treg.Optional[string]{}, s.From, absent)
		assert.Equal(t, treg.Optional[string]{}, s.To, absent)
		assert.Equal(t, treg.Optional[treg.SpendGroup]{}, s.Group, absent)
		assert.Equal(t, treg.Optional[treg.SpendStack]{}, s.Stack, absent)
		assert.Equal(t, treg.Optional[treg.MicroUSD]{}, s.SpentMicro, absent)
		assert.Equal(t, treg.Optional[int64]{}, s.Charges, absent)
		assert.Equal(t, treg.Optional[treg.SpendOptions]{}, s.Options, absent)
		assert.Nil(t, s.Series)
		assert.Nil(t, s.Buckets)
		assert.Nil(t, s.Ranking)
	})
	t.Run("not ready without history", func(t *testing.T) {
		t.Parallel()
		res, err := authReadStep(t, testutil.Step{Body: `{"ready":false,"history_from":null}`},
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}))
		require.NoError(t, err)
		assert.Equal(t, treg.Null[string](), res.Value.HistoryFrom)
	})
	t.Run("echoes outside the request's set still decode", func(t *testing.T) {
		t.Parallel()
		res, err := authReadStep(t, testutil.Step{Body: `{"ready":true,"group":"quarter","stack":null}`},
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}))
		require.NoError(t, err)
		assert.Equal(t, treg.Some(treg.SpendGroup("quarter")), res.Value.Group)
		assert.Equal(t, treg.Null[treg.SpendStack](), res.Value.Stack)
	})
}

func TestOrgsUsageByTagDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/usage_by_tag.json"),
		bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: "customer", Days: 30}))
	assertAuthReadWire(t, r, "/orgs/42/usage/by-tag?days=30&key=customer", "")
	assert.Equal(t, treg.OpOrgUsageByTag, res.Meta.Op)
	u := res.Value
	assert.Equal(t, "customer", u.Key)
	assert.Equal(t, int64(30), u.Days)
	assert.Equal(t, "2026-09-17", u.Since)
	assert.Equal(t, []treg.TagUsageRow{
		{Value: "c-001", ChargedMicro: 3_000_000, ChargedUSD: treg.Some(treg.Decimal("3.0")), Calls: 60},
		{Value: "c-002", ChargedMicro: 821_000, ChargedUSD: treg.Some(treg.Decimal("0.821")), Calls: 25},
	}, u.Rows)
	assert.Equal(t, treg.MicroUSD(3_821_000), u.AttributedMicro)
	assert.Equal(t, treg.MicroUSD(500_000), u.UnattributedMicro)
	assert.Equal(t, treg.MicroUSD(4_321_000), u.TotalMicro)
	assert.Equal(t, u.TotalMicro, u.AttributedMicro+u.UnattributedMicro)
	assert.Equal(t, treg.Some(treg.Decimal("4.321")), u.TotalUSD)
	assert.Equal(t, "4.321", u.TotalMicro.DecimalUSD())
}

func TestOrgsTagKeysDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/tag_keys.json"),
		bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 42}))
	assertAuthReadWire(t, r, "/orgs/42/tag-keys", "")
	assert.Equal(t, treg.OpOrgTagKeys, res.Meta.Op)
	assert.Equal(t, treg.TagKeys{
		Seen: []string{"customer", "env", "team"}, Budgetable: []string{"customer", "team"}, Primary: treg.Some("customer"),
	}, res.Value)
}

func TestOrgsProviderResourcesDecodesFixture(t *testing.T) {
	t.Parallel()
	res, r := authReadBody(t, testutil.HTTP2, synthetic(t, "orgs/provider_resources.json"),
		bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, IncludeDeleted: true}))
	assertAuthReadWire(t, r, "/orgs/42/provider-resources?include_deleted=true", "")
	assert.Equal(t, treg.OpOrgProviderResources, res.Meta.Op)
	require.Len(t, res.Value, 3)
	platform, byok, deleted := res.Value[0], res.Value[1], res.Value[2]

	assertExactJSON(t, "101", platform.ID)
	var n int64
	require.NoError(t, json.Unmarshal(platform.ID, &n))
	assert.Equal(t, int64(101), n)
	assert.Equal(t, "voiceco", platform.Provider)
	assert.Equal(t, "voice", platform.Kind)
	assert.Equal(t, treg.Some("vc_up_0001"), platform.UpstreamID)
	assert.Equal(t, "Narrator A", platform.DisplayName)
	assert.Equal(t, treg.Some("member-17"), platform.CreatedBy)
	assert.Equal(t, treg.Some(authReadCallRef), platform.SourceCallRef)
	assert.Equal(t, "active", platform.Status)
	assert.Equal(t, treg.Some("2026-10-01T09:00:00Z"), platform.CreatedAt)
	assert.Equal(t, treg.Null[string](), platform.DeletedAt)

	assertExactJSON(t, `"vc_byok_7f3a"`, byok.ID)
	var s string
	require.NoError(t, json.Unmarshal(byok.ID, &s))
	assert.Equal(t, "vc_byok_7f3a", s)
	assert.Equal(t, treg.Null[string](), byok.CreatedBy)
	assert.Equal(t, treg.Null[string](), byok.SourceCallRef)
	assert.Equal(t, treg.Null[string](), byok.CreatedAt)
	assert.Equal(t, treg.Null[string](), byok.UpdatedAt)

	assert.Equal(t, "102", string(deleted.ID))
	assert.Equal(t, "deleted", deleted.Status)
	assert.Equal(t, treg.Some("2026-10-10T09:00:00Z"), deleted.DeletedAt)
}

func TestOrgsWire(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, fixture, want string
		read                anyRead[treg.OrgsService]
	}{
		{"Balance defaults", "balance_member", "/orgs/42/balance", bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42}).erased()},
		{
			"Balance limit and offset", "balance", "/orgs/42/balance?limit=200&offset=10",
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Limit: 200, Offset: 10}).erased(),
		},
		{
			"Balance min limit", "balance", "/orgs/42/balance?limit=1",
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Limit: 1}).erased(),
		},
		{"Usage defaults", "usage", "/orgs/42/usage", bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}).erased()},
		{
			"Usage max days", "usage", "/orgs/42/usage?days=365",
			bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42, Days: 365}).erased(),
		},
		{"Spend defaults", "spend_not_ready", "/orgs/42/usage/spend", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}).erased()},
		{
			"Spend every field", "spend_ready",
			"/orgs/42/usage/spend?from=2026-10-01&group=week&key=7&provider=examplecorp&stack=tool&to=2026-10-16",
			bind(treg.OrgsService.Spend, treg.SpendRequest{
				OrgID: 42, From: "2026-10-01", To: "2026-10-16", Group: treg.SpendGroupWeek, KeyID: 7,
				Provider: "examplecorp", Stack: treg.SpendStackTool,
			}).erased(),
		},
		{
			"Spend zero key id omitted", "spend_ready", "/orgs/42/usage/spend?from=2026-10-01",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, From: "2026-10-01", KeyID: 0}).erased(),
		},
		{
			"Spend group day stack key", "spend_ready", "/orgs/42/usage/spend?group=day&stack=key",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Group: treg.SpendGroupDay, Stack: treg.SpendStackKey}).erased(),
		},
		{
			"Spend group month from only", "spend_ready", "/orgs/42/usage/spend?from=2024-02-29&group=month",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Group: treg.SpendGroupMonth, From: "2024-02-29"}).erased(),
		},
		{
			"UsageByTag defaults", "usage_by_tag", "/orgs/42/usage/by-tag",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42}).erased(),
		},
		{
			"UsageByTag key and days", "usage_by_tag", "/orgs/42/usage/by-tag?days=7&key=cost_center_2",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: "cost_center_2", Days: 7}).erased(),
		},
		{"TagKeys", "tag_keys", "/orgs/42/tag-keys", bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 42}).erased()},
		{
			"TagKeys large org id", "tag_keys", "/orgs/9007199254740993/tag-keys",
			bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 9007199254740993}).erased(),
		},
		{
			"ProviderResources defaults", "provider_resources", "/orgs/42/provider-resources",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42}).erased(),
		},
		{
			"ProviderResources every field", "provider_resources",
			"/orgs/42/provider-resources?include_deleted=true&kind=voice&provider=voiceco&source=platform",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{
				OrgID: 42, Provider: "voiceco", Kind: "voice", IncludeDeleted: true, Source: treg.ResourceSourcePlatform,
			}).erased(),
		},
		{
			"ProviderResources source auto", "provider_resources", "/orgs/42/provider-resources?source=auto",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Source: treg.ResourceSourceAuto}).erased(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAuthReadWire(t, synthetic(t, "orgs/"+tc.fixture+".json"), tc.read, tc.want)
		})
	}
}

func TestOrgsValidation(t *testing.T) {
	t.Parallel()
	tooLong := strings.Repeat("a", 65)
	tests := []struct {
		name          string
		read          anyRead[treg.OrgsService]
		field, reason string
	}{
		{"Balance org id zero", bind(treg.OrgsService.Balance, treg.BalanceRequest{}).erased(), "OrgID", "required"},
		{"Balance org id negative", bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: -1}).erased(), "OrgID", "required"},
		{
			"Balance limit over",
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Limit: 201}).erased(),
			"Limit", "out_of_range",
		},
		{
			"Balance limit negative",
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Limit: -1}).erased(),
			"Limit", "out_of_range",
		},
		{
			"Balance offset negative",
			bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42, Offset: -1}).erased(),
			"Offset", "negative",
		},
		{"Usage org id zero", bind(treg.OrgsService.Usage, treg.OrgUsageRequest{Days: 30}).erased(), "OrgID", "required"},
		{"Usage days over", bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42, Days: 366}).erased(), "Days", "out_of_range"},
		{"Usage days negative", bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42, Days: -1}).erased(), "Days", "out_of_range"},
		{"Spend org id zero", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 0}).erased(), "OrgID", "required"},
		{
			"Spend from unpadded",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, From: "2026-1-1"}).erased(),
			"From", "invalid_format",
		},
		{"Spend from bogus", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, From: "bogus"}).erased(), "From", "invalid_format"},
		{
			"Spend from timestamp",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, From: "2026-10-01T00:00:00Z"}).erased(),
			"From", "invalid_format",
		},
		{
			"Spend to bad month",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, To: "2026-13-01"}).erased(),
			"To", "invalid_format",
		},
		{"Spend to bad day", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, To: "2026-02-30"}).erased(), "To", "invalid_format"},
		{"Spend group year", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Group: "year"}).erased(), "Group", "invalid_format"},
		{
			"Spend group capitalized",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Group: "Week"}).erased(),
			"Group", "invalid_format",
		},
		{"Spend key id negative", bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, KeyID: -1}).erased(), "KeyID", "out_of_range"},
		{
			"Spend provider space",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Provider: "a b"}).erased(),
			"Provider", "invalid_character",
		},
		{
			"Spend provider slash",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Provider: "a/b"}).erased(),
			"Provider", "invalid_character",
		},
		{
			"Spend provider control",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Provider: "a\x00b"}).erased(),
			"Provider", "control_character",
		},
		{
			"Spend provider too long",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Provider: tooLong}).erased(),
			"Provider", "too_long",
		},
		{
			"Spend stack tool without provider",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Stack: treg.SpendStackTool}).erased(),
			"Stack", "conflict",
		},
		{
			"Spend stack bogus",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Stack: "bogus"}).erased(),
			"Stack", "invalid_format",
		},
		{
			"Spend token as provider",
			bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42, Provider: testToken}).erased(),
			"Request", "credential_in_request",
		},
		{
			"UsageByTag org id zero",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{Key: "customer"}).erased(),
			"OrgID", "required",
		},
		{
			"UsageByTag key with dash",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: "Bad-Key"}).erased(),
			"Key", "invalid_character",
		},
		{
			"UsageByTag key upper case",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: "CUSTOMER"}).erased(),
			"Key", "invalid_character",
		},
		{
			"UsageByTag key control",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: "a\nb"}).erased(),
			"Key", "control_character",
		},
		{
			"UsageByTag key too long",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Key: strings.Repeat("k", 33)}).erased(),
			"Key", "too_long",
		},
		{
			"UsageByTag days over",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Days: 400}).erased(),
			"Days", "out_of_range",
		},
		{
			"UsageByTag days negative",
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42, Days: -1}).erased(),
			"Days", "out_of_range",
		},
		{"TagKeys org id zero", bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{}).erased(), "OrgID", "required"},
		{"TagKeys org id negative", bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: -7}).erased(), "OrgID", "required"},
		{
			"ProviderResources org id zero",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{}).erased(),
			"OrgID", "required",
		},
		{
			"ProviderResources provider space",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Provider: "a b"}).erased(),
			"Provider", "invalid_character",
		},
		{
			"ProviderResources provider control",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Provider: "a\tb"}).erased(),
			"Provider", "control_character",
		},
		{
			"ProviderResources provider too long",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Provider: tooLong}).erased(),
			"Provider", "too_long",
		},
		{
			"ProviderResources kind space",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Kind: "voice clone"}).erased(),
			"Kind", "invalid_character",
		},
		{
			"ProviderResources kind control",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Kind: "a\x7fb"}).erased(),
			"Kind", "control_character",
		},
		{
			"ProviderResources kind too long",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Kind: tooLong}).erased(),
			"Kind", "too_long",
		},
		{
			"ProviderResources source remote",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Source: "remote"}).erased(),
			"Source", "invalid_format",
		},
		{
			"ProviderResources source upper case",
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42, Source: "AUTO"}).erased(),
			"Source", "invalid_format",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			requireValidation(t, tc.read.on(t.Context(), newClient(t, script)), tc.field, tc.reason)
			assert.Zero(t, script.Calls(), "nothing is sent")
		})
	}
}

func TestOrgsZeroValue(t *testing.T) {
	t.Parallel()
	requireAuthReadsNotInitialized(t.Context(), t, orgsReads())
}

func TestOrgsOptionsAndContext(t *testing.T) {
	t.Parallel()
	requireAuthReadsRefuseOptions(t.Context(), t, orgsReads())
}

func TestOrgsAdminOnlyRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   treg.Operation
		read anyRead[treg.OrgsService]
	}{
		{"Usage", treg.OpOrgUsage, bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}).erased()},
		{"Spend", treg.OpOrgSpend, bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42}).erased()},
		{"UsageByTag", treg.OpOrgUsageByTag, bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42}).erased()},
		{"TagKeys", treg.OpOrgTagKeys, bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 42}).erased()},
		{"Balance of another team", treg.OpOrgBalance, bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 7}).erased()},
		{
			"ProviderResources", treg.OpOrgProviderResources,
			bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42}).erased(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			marked := testutil.Step{Status: http.StatusForbidden, Header: gatewayHeader(), Body: authReadAdminOnly}
			want := callErrorWant{Op: tc.op, Kind: treg.FailureHTTPStatus, Status: http.StatusForbidden, Marked: new(true)}

			err := authReadStepErr(t, marked, tc.read)
			ce := requireCallError(t, err, want)
			assert.Nil(t, ce.Refusal, "a prose detail is not a refusal code")
			assert.Nil(t, ce.Body(), "error bodies are off by default")
			assert.Equal(t, "treg: "+string(tc.op)+": http_status (HTTP 403, gateway)", err.Error())
			assert.NotContains(t, fmt.Sprintf("%v %+v %#v", err, err, err), "admin role")

			err = authReadStepErr(t, marked, tc.read, treg.WithErrorBodies())
			ce = requireCallError(t, err, want)
			assertExactJSON(t, authReadAdminOnly, ce.Body())
		})
	}
}

func TestOrgsServerErrorsNeverOutcomeUnknown(t *testing.T) {
	t.Parallel()
	balance := bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42})
	for _, status := range []int{
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
	} {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d marked %t", status, marked), func(t *testing.T) {
				t.Parallel()
				var header http.Header // a JSON answer
				if marked {
					header = gatewayHeader()
				}
				_, err := authReadStep(t, testutil.Step{Status: status, Header: header, Body: `{"detail":"upstream"}`}, balance)
				requireCallError(t, err, callErrorWant{
					Op: treg.OpOrgBalance, Kind: treg.FailureHTTPStatus, Status: status, Marked: new(marked),
				})
			})
		}
	}
	t.Run("saturated 503", func(t *testing.T) {
		t.Parallel()
		_, err := authReadStep(t, testutil.Step{Status: http.StatusServiceUnavailable, Body: `{"treg_saturated":true}`},
			bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}))
		// A saturation refusal matches ErrGatewayRefusal without the header.
		ce := requireCallError(t, err, callErrorWant{
			Op: treg.OpOrgUsage, Kind: treg.FailureHTTPStatus, Status: http.StatusServiceUnavailable, Is: treg.ErrGatewayRefusal,
		})
		assert.False(t, ce.GatewayMarked)
		require.NotNil(t, ce.Refusal)
		assert.True(t, ce.Refusal.Saturated)
	})
}

func TestOrgsMalformedResponses(t *testing.T) {
	t.Parallel()
	balance := func(extra string) string {
		return `{"org_id":42,"balance_micro":1,"blocks":[],"holds":[],"entries":{"limit":20,"offset":0,"items":[` +
			extra + `]}}`
	}
	entry := func(meta string) string {
		return `{"id":1,"kind":"charge","amount_micro":-1,"meta":` + meta + `,"created_at":"2026-10-16T00:00:00Z"}`
	}
	balanceRead := bind(treg.OrgsService.Balance, treg.BalanceRequest{OrgID: 42})
	spendRead := bind(treg.OrgsService.Spend, treg.SpendRequest{OrgID: 42})
	resources := bind(treg.OrgsService.ProviderResources, treg.ProviderResourcesRequest{OrgID: 42}).erased()
	for _, tc := range []struct {
		name, body string
		read       anyRead[treg.OrgsService]
		op         treg.Operation
		reason     string
	}{
		{
			"Balance duplicate top-level key", `{"org_id":42,"balance_micro":1,"balance_micro":2}`,
			balanceRead.erased(), treg.OpOrgBalance, "duplicate_key",
		},
		{
			"Balance duplicate key in a block", `{"org_id":42,"balance_micro":1,"blocks":[{"id":1,"kind":"a","kind":"b"}]}`,
			balanceRead.erased(), treg.OpOrgBalance, "duplicate_key",
		},
		{
			"Balance duplicate key in entries", `{"org_id":42,"balance_micro":1,"entries":{"limit":1,"limit":2}}`,
			balanceRead.erased(), treg.OpOrgBalance, "duplicate_key",
		},
		{
			"Balance quoted micro amount", `{"org_id":42,"balance_micro":"12500000"}`,
			balanceRead.erased(), treg.OpOrgBalance, "decode",
		},
		{
			"Balance quoted decimal", `{"org_id":42,"balance_micro":1,"balance_usd":"12.5"}`,
			balanceRead.erased(), treg.OpOrgBalance, "decode",
		},
		{
			"Balance fractional micro amount", `{"org_id":42,"balance_micro":12.5}`,
			balanceRead.erased(), treg.OpOrgBalance, "decode",
		},
		{"Balance array body", `[]`, balanceRead.erased(), treg.OpOrgBalance, "decode"},
		{
			"Balance duplicate key next to meta",
			balance(`{"id":1,"id":2,"kind":"charge","amount_micro":-1,"meta":{},"created_at":"x"}`),
			balanceRead.erased(), treg.OpOrgBalance, "duplicate_key",
		},
		{
			"Spend duplicate key in options",
			`{"ready":true,"options":{"keys":[{"id":7,"name":"a","name":"b","spent_micro":1}],"providers":[]}}`,
			spendRead.erased(), treg.OpOrgSpend, "duplicate_key",
		},
		{
			"Usage duplicate user email",
			`{"days":30,"by_user":[{"user_email":"a@example.com","user_email":"b@example.com","call":1}]}`,
			bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}).erased(), treg.OpOrgUsage, "duplicate_key",
		},
		{
			"UsageByTag quoted charge", `{"key":"customer","rows":[{"value":"c-001","charged_micro":"3000000","calls":1}]}`,
			bind(treg.OrgsService.UsageByTag, treg.UsageByTagRequest{OrgID: 42}).erased(), treg.OpOrgUsageByTag, "decode",
		},
		{
			"TagKeys duplicate primary", `{"seen":[],"budgetable":[],"primary":"a","Primary":"b"}`,
			bind(treg.OrgsService.TagKeys, treg.TagKeysRequest{OrgID: 42}).erased(), treg.OpOrgTagKeys, "duplicate_key",
		},
		{"ProviderResources object body", `{"resources":[]}`, resources, treg.OpOrgProviderResources, "decode"},
		{
			"ProviderResources duplicate id", `[{"id":1,"id":"x","provider":"p"}]`,
			resources, treg.OpOrgProviderResources, "duplicate_key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallError(t, authReadStepErr(t, testutil.Step{Body: tc.body}, tc.read), callErrorWant{
				Op: tc.op, Kind: treg.FailureMalformedResponse, Reason: tc.reason, Status: http.StatusOK,
			})
		})
	}
	t.Run("Balance duplicate key inside meta is kept", func(t *testing.T) {
		t.Parallel()
		meta := `{"note":"a","note":"b","nested":{"k":1,"k":2}}`
		res, err := authReadStep(t, testutil.Step{Body: balance(entry(meta))}, balanceRead)
		require.NoError(t, err)
		require.Len(t, res.Value.Entries.Items, 1)
		assertExactJSON(t, meta, res.Value.Entries.Items[0].Meta)
	})
	t.Run("Spend duplicate keys inside series buckets and ranking are kept", func(t *testing.T) {
		t.Parallel()
		series := `[{"bucket":"a","bucket":"b"}]`
		buckets := `[{"x":1,"x":2}]`
		ranking := `[{"id":7,"id":8}]`
		body := `{"ready":true,"series":` + series + `,"buckets":` + buckets + `,"ranking":` + ranking + `}`
		res, err := authReadStep(t, testutil.Step{Body: body}, spendRead)
		require.NoError(t, err)
		assertExactJSON(t, series, res.Value.Series)
		assertExactJSON(t, buckets, res.Value.Buckets)
		assertExactJSON(t, ranking, res.Value.Ranking)
	})
}

func TestOrgsUserUsagePrivacy(t *testing.T) {
	t.Parallel()
	emails := []string{authReadEmailMarker, "second.member@example.com"}
	res, _ := authReadBody(t, testutil.HTTP1, synthetic(t, "orgs/usage.json"),
		bind(treg.OrgsService.Usage, treg.OrgUsageRequest{OrgID: 42}))
	usage := res.Value
	require.Len(t, usage.ByUser, 2)
	member := usage.ByUser[0]
	want := "treg.UserUsage{call: 100, localRun: 8, serverRun: 0, total: 108}"
	t.Run("prints its totals", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, authReadEmailMarker, member.UserEmail, "the data stays accessible")
		testutil.AssertNeverPrints(t, &member, emails...)
		testutil.AssertNeverPrints(t, &usage, emails...)
		testutil.AssertNeverPrints(t, usage.ByUser, emails...)
		testutil.AssertNeverPrints(t, res, emails...)
		assert.Equal(t, want, fmt.Sprintf("%#v", member))
		b, err := json.Marshal(member)
		require.NoError(t, err)
		assert.JSONEq(t, `{"call":100,"local_run":8,"server_run":0,"total":108}`, string(b))
		b, err = json.Marshal(usage)
		require.NoError(t, err)
		testutil.AssertRedacted(t, string(b), emails)
	})
	for _, verb := range []string{"%v", "%+v"} {
		t.Run("OrgUsage prints its totals with "+verb, func(t *testing.T) {
			t.Parallel()
			out := fmt.Sprintf(verb, usage)
			testutil.AssertRedacted(t, out, emails)
			assert.Contains(t, out, want)
		})
	}
	t.Run("every holding", func(t *testing.T) {
		t.Parallel()
		assertAuthReadNeverPrints(t, &member, emails...)
	})
}
