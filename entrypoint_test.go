package treg_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// entryPoint is one exported method that takes a context, called on a
// client.
type entryPoint struct {
	name string
	call func(c *treg.Client, ctx context.Context) error
}

// errOf drops a method's result and returns its error.
func errOf(_ any, err error) error { return err }

// callMethods lists the three methods of the open execution seam, each
// making the call req and returning only its error.
func callMethods(req treg.CallRequest) []entryPoint {
	return []entryPoint{
		{"Call", func(c *treg.Client, ctx context.Context) error { return errOf(c.Call(ctx, req)) }},
		{"CallBytes", func(c *treg.Client, ctx context.Context) error { return errOf(c.CallBytes(ctx, req)) }},
		{"CallJSON", func(c *treg.Client, ctx context.Context) error { return errOf(treg.CallJSON[any](ctx, c, req)) }},
	}
}

// entryPoints lists every exported method of Client, CatalogClient and the
// services that takes a context. Each request is valid, so only the
// receiver or the context can fail it.
func entryPoints() []entryPoint {
	return append(callMethods(getRequest()), []entryPoint{
		{"EndpointAccess", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.EndpointAccess(ctx, treg.EndpointAccessRequest{ID: "x.y.z"}))
		}},
		{"Catalog.ServerInfo", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().ServerInfo(ctx, treg.ServerInfoRequest{}))
		}},
		{"Catalog.Platforms", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Platforms(ctx, treg.PlatformsRequest{}))
		}},
		{"Catalog.Platform", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Platform(ctx, treg.PlatformRequest{Slug: "web"}))
		}},
		{"Catalog.Provider", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Provider(ctx, treg.ProviderRequest{Service: "moz"}))
		}},
		{"Catalog.Search", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Search(ctx, treg.SearchRequest{Query: "x"}))
		}},
		{"Catalog.Endpoint", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Endpoint(ctx, treg.EndpointRequest{ID: "x.y.z"}))
		}},
		{"Catalog.Example", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Example(ctx, treg.ExampleRequest{ID: "x.y.z"}))
		}},
		{"Catalog.Find", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Catalog().Find(ctx, treg.FindRequest{Query: "x"}))
		}},
		{"Calls.List", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Calls().List(ctx, treg.ListCallsRequest{}))
		}},
		{"Calls.Get", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Calls().Get(ctx, treg.GetCallRequest{Ref: "r"}))
		}},
		{"Calls.Result", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Calls().Result(ctx, treg.CallResultRequest{Ref: "1"}))
		}},
		{"Account.Me", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Account().Me(ctx, treg.MeRequest{}))
		}},
		{"Account.Orgs", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Account().Orgs(ctx, treg.AccountOrgsRequest{}))
		}},
		{"Account.Usage", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Account().Usage(ctx, treg.MemberUsageRequest{}))
		}},
		{"Orgs.Balance", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().Balance(ctx, treg.BalanceRequest{OrgID: 1}))
		}},
		{"Orgs.Usage", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().Usage(ctx, treg.OrgUsageRequest{OrgID: 1}))
		}},
		{"Orgs.Spend", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().Spend(ctx, treg.SpendRequest{OrgID: 1}))
		}},
		{"Orgs.UsageByTag", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().UsageByTag(ctx, treg.UsageByTagRequest{OrgID: 1}))
		}},
		{"Orgs.TagKeys", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().TagKeys(ctx, treg.TagKeysRequest{OrgID: 1}))
		}},
		{"Orgs.ProviderResources", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Orgs().ProviderResources(ctx, treg.ProviderResourcesRequest{OrgID: 1}))
		}},
		{"Tools.List", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Tools().List(ctx, treg.ListToolsRequest{}))
		}},
		{"Tools.ByName", func(c *treg.Client, ctx context.Context) error {
			return errOf(c.Tools().ByName(ctx, treg.ToolByNameRequest{Name: "stripe"}))
		}},
	}...)
}

func TestEntryPointsUninitializedClient(t *testing.T) {
	t.Parallel()
	for _, ep := range entryPoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name string
				c    *treg.Client
			}{
				{"nil client", nil},
				{"zero client", &treg.Client{}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					var err error
					require.NotPanics(t, func() { err = ep.call(tc.c, t.Context()) })
					requireConfigValidation(t, err, "client", "not_initialized")
					require.ErrorIs(t, ep.call(tc.c, nil), treg.ErrInvalidConfig, "the receiver is checked before the context")
				})
			}
		})
	}
}

func TestEntryPointsNilContext(t *testing.T) {
	t.Parallel()
	for _, ep := range entryPoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			var err error
			require.NotPanics(t, func() { err = ep.call(c, nil) })
			requireValidation(t, err, "ctx", "nil")
			assert.Zero(t, script.Calls())
		})
	}
}

// invalidRead is one read called with an invalid request, and the field its
// error names.
type invalidRead struct {
	name, field, reason string
	call                func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error
}

// invalidReads lists every read whose request can be invalid, each called
// with an invalid request. Find takes no options.
func invalidReads() []invalidRead {
	return []invalidRead{
		{"EndpointAccess", "ID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.EndpointAccess(ctx, treg.EndpointAccessRequest{}, opts...))
		}},
		{"Catalog.Platform", "Slug", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Catalog().Platform(ctx, treg.PlatformRequest{}, opts...))
		}},
		{"Catalog.Provider", "Service", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Catalog().Provider(ctx, treg.ProviderRequest{}, opts...))
		}},
		{"Catalog.Search", "Limit", "out_of_range", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Catalog().Search(ctx, treg.SearchRequest{Limit: -1}, opts...))
		}},
		{"Catalog.Endpoint", "ID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Catalog().Endpoint(ctx, treg.EndpointRequest{}, opts...))
		}},
		{"Catalog.Example", "ID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Catalog().Example(ctx, treg.ExampleRequest{}, opts...))
		}},
		{"Catalog.Find", "Query", "required", func(c *treg.Client, ctx context.Context, _ ...treg.CallOption) error {
			return errOf(c.Catalog().Find(ctx, treg.FindRequest{}))
		}},
		{"Calls.List", "Limit", "out_of_range", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Calls().List(ctx, treg.ListCallsRequest{Limit: -1}, opts...))
		}},
		{"Calls.Get", "Ref", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Calls().Get(ctx, treg.GetCallRequest{}, opts...))
		}},
		{"Calls.Result", "Ref", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Calls().Result(ctx, treg.CallResultRequest{}, opts...))
		}},
		{"Orgs.Balance", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().Balance(ctx, treg.BalanceRequest{}, opts...))
		}},
		{"Orgs.Usage", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().Usage(ctx, treg.OrgUsageRequest{}, opts...))
		}},
		{"Orgs.Spend", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().Spend(ctx, treg.SpendRequest{}, opts...))
		}},
		{"Orgs.UsageByTag", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().UsageByTag(ctx, treg.UsageByTagRequest{}, opts...))
		}},
		{"Orgs.TagKeys", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().TagKeys(ctx, treg.TagKeysRequest{}, opts...))
		}},
		{"Orgs.ProviderResources", "OrgID", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Orgs().ProviderResources(ctx, treg.ProviderResourcesRequest{}, opts...))
		}},
		{"Tools.ByName", "Name", "required", func(c *treg.Client, ctx context.Context, opts ...treg.CallOption) error {
			return errOf(c.Tools().ByName(ctx, treg.ToolByNameRequest{}, opts...))
		}},
	}
}

// TestReadsCheckOrder pins the order of a read's local checks: the client,
// then the request, then the context, then the call options.
func TestReadsCheckOrder(t *testing.T) {
	t.Parallel()
	var nilCtx context.Context
	for _, rd := range invalidReads() {
		t.Run(rd.name, func(t *testing.T) {
			t.Parallel()
			err := rd.call(nil, nilCtx, treg.UseNumber())
			requireConfigValidation(t, err, "client", "not_initialized")

			script := testutil.NewScript()
			err = rd.call(newClient(t, script), nilCtx, treg.UseNumber())
			// The request is checked before the context and the options.
			requireValidation(t, err, rd.field, rd.reason)
			assert.Zero(t, script.Calls())
		})
	}
}

func TestEntryPointsDoneContext(t *testing.T) {
	t.Parallel()
	for _, ep := range entryPoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			requireCallError(t, ep.call(c, ctx), callErrorWant{Kind: treg.FailureCanceled})
			assert.Zero(t, script.Calls(), "a done context sends nothing")
		})
	}
}

func TestEntryPointsZeroServices(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	errs := map[string]error{}
	_, errs["CatalogClient.ServerInfo"] = (&treg.CatalogClient{}).ServerInfo(ctx, treg.ServerInfoRequest{})
	_, errs["CatalogClient.Find"] = (*treg.CatalogClient)(nil).Find(ctx, treg.FindRequest{Query: "x"})
	_, errs["CallsService.List"] = treg.CallsService{}.List(ctx, treg.ListCallsRequest{})
	_, errs["AccountService.Me"] = treg.AccountService{}.Me(ctx, treg.MeRequest{})
	_, errs["OrgsService.TagKeys"] = treg.OrgsService{}.TagKeys(ctx, treg.TagKeysRequest{OrgID: 1})
	_, errs["ToolsService.List"] = treg.ToolsService{}.List(ctx, treg.ListToolsRequest{})
	for name, err := range errs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireConfigValidation(t, err, "client", "not_initialized")
		})
	}
	assert.Nil(t, (*treg.Client)(nil).Catalog())
	assert.Nil(t, (&treg.Client{}).Catalog())
}

func TestEntryPointsZeroResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		r    *treg.Response
	}{
		{"nil Response", nil},
		{"zero Response", &treg.Response{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := tc.r
			assert.Zero(t, r.StatusCode())
			assert.Equal(t, treg.Metadata{}, r.Meta())
			assert.Empty(t, r.Header())
			assert.False(t, r.Decompressed())
			_, err := r.Read(make([]byte, 1))
			require.ErrorIs(t, err, treg.ErrInvalidConfig)
			require.ErrorIs(t, r.Close(), treg.ErrInvalidConfig)
			require.ErrorIs(t, r.StatusError(t.Context()), treg.ErrInvalidConfig)
		})
	}
	for _, tc := range []struct {
		name string
		r    *treg.RawResponse
	}{
		{"nil RawResponse", nil},
		{"zero RawResponse", &treg.RawResponse{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := tc.r
			assert.Zero(t, r.StatusCode())
			assert.Equal(t, treg.Metadata{}, r.Meta())
			assert.Nil(t, r.Body())
			assert.Empty(t, r.Header())
			assert.False(t, r.Decompressed())
			require.ErrorIs(t, r.StatusError(), treg.ErrInvalidConfig)
		})
	}
	for _, tc := range []struct {
		name string
		s    *treg.FindStream
	}{
		{"nil FindStream", nil},
		{"zero FindStream", &treg.FindStream{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := tc.s
			assert.Equal(t, treg.Metadata{}, s.Meta())
			require.ErrorIs(t, s.Close(), treg.ErrInvalidConfig)
			for _, err := range s.Events() {
				require.ErrorIs(t, err, treg.ErrInvalidConfig)
			}
		})
	}
	t.Run("zero Capture", func(t *testing.T) {
		t.Parallel()
		var capture treg.Capture
		assert.Equal(t, treg.CaptureDisabled, capture.State())
		assert.Nil(t, capture.Bytes())
	})
	t.Run("zero Target", func(t *testing.T) {
		t.Parallel()
		require.ErrorIs(t, treg.Target{}.Validate(), treg.ErrInvalidRequest)
	})
	t.Run("nil CallError", func(t *testing.T) {
		t.Parallel()
		var ce *treg.CallError
		assert.Equal(t, "treg: <nil CallError>", ce.Error())
		assert.Equal(t, "(*treg.CallError)(nil)", ce.GoString())
		require.NoError(t, ce.Unwrap())
		assert.False(t, ce.Timeout())
		assert.Nil(t, ce.Body())
		assert.Equal(t, treg.CaptureDisabled, ce.Captured().State())
	})
	t.Run("nil ValidationError", func(t *testing.T) {
		t.Parallel()
		var ve *treg.ValidationError
		assert.Equal(t, "treg: <nil ValidationError>", ve.Error())
		require.NoError(t, ve.Unwrap())
	})
}
