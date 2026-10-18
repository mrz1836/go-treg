package treg_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestControlsInvalid(t *testing.T) {
	t.Parallel()
	tags := func(pairs ...string) []treg.Tag {
		var out []treg.Tag
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, treg.Tag{Key: pairs[i], Value: pairs[i+1]})
		}
		return out
	}
	tests := []struct {
		name   string
		ctl    treg.Controls
		field  string
		reason string
	}{
		{"key too long", treg.Controls{IdempotencyKey: strings.Repeat("k", 201)}, "Controls.IdempotencyKey", "too_long"},
		{"key space", treg.Controls{IdempotencyKey: "a b"}, "Controls.IdempotencyKey", "invalid_character"},
		{"key non-ascii", treg.Controls{IdempotencyKey: "clé"}, "Controls.IdempotencyKey", "invalid_character"},
		{"key control", treg.Controls{IdempotencyKey: "a\r\nb"}, "Controls.IdempotencyKey", "control_character"},
		{"max cost negative", treg.Controls{MaxCost: new(treg.MicroUSD(-1))}, "Controls.MaxCost", "negative"},
		{"max cost too high", treg.Controls{MaxCost: new(treg.MicroUSD(1_000_000_000_001))}, "Controls.MaxCost", "out_of_range"},
		{"six tags", treg.Controls{Tags: tags("a", "1", "b", "1", "c", "1", "d", "1", "e", "1", "f", "1")}, "Controls.Tags", "too_many"},
		{"tag key upper", treg.Controls{Tags: tags("Customer", "x")}, "Controls.Tags[0].Key", "invalid_character"},
		{"tag key control", treg.Controls{Tags: tags("a\tb", "x")}, "Controls.Tags[0].Key", "control_character"},
		{"tag key empty", treg.Controls{Tags: tags("", "x")}, "Controls.Tags[0].Key", "required"},
		{"tag key long", treg.Controls{Tags: tags(strings.Repeat("k", 33), "x")}, "Controls.Tags[0].Key", "too_long"},
		{"tag value email", treg.Controls{Tags: tags("customer", "a@example.com")}, "Controls.Tags[0].Value", "invalid_character"},
		{"tag value comma", treg.Controls{Tags: tags("customer", "a,b")}, "Controls.Tags[0].Value", "invalid_character"},
		{"tag value equals", treg.Controls{Tags: tags("customer", "a=b")}, "Controls.Tags[0].Value", "invalid_character"},
		{"tag value control", treg.Controls{Tags: tags("customer", "a\x00b")}, "Controls.Tags[0].Value", "control_character"},
		{"tag value empty", treg.Controls{Tags: tags("customer", "")}, "Controls.Tags[0].Value", "required"},
		{"tag value long", treg.Controls{Tags: tags("customer", strings.Repeat("v", 129))}, "Controls.Tags[0].Value", "too_long"},
		{"tag duplicate", treg.Controls{Tags: tags("a", "1", "a", "2")}, "Controls.Tags[1].Key", "duplicate"},
		{
			"tags too large",
			treg.Controls{Tags: tags(
				strings.Repeat("a", 32), strings.Repeat("v", 128), strings.Repeat("b", 32), strings.Repeat("v", 128),
				strings.Repeat("c", 32), strings.Repeat("v", 128), strings.Repeat("d", 32), strings.Repeat("v", 128),
			)},
			"Controls.Tags", "too_large",
		},
		{"max age negative", treg.Controls{MaxAge: new(-time.Second)}, "Controls.MaxAge", "negative"},
		{"max age fractional", treg.Controls{MaxAge: new(1500 * time.Millisecond)}, "Controls.MaxAge", "not_whole_seconds"},
		{"prefer bad", treg.Controls{Route: treg.RouteControls{Prefer: []string{"ok", "bad name"}}}, "Controls.Route.Prefer[1]", "invalid_character"},
		{"exclude control", treg.Controls{Route: treg.RouteControls{Exclude: []string{"a\x7fb"}}}, "Controls.Route.Exclude[0]", "control_character"},
		{"exclude empty", treg.Controls{Route: treg.RouteControls{Exclude: []string{""}}}, "Controls.Route.Exclude[0]", "required"},
		{"prefer too long", treg.Controls{Route: treg.RouteControls{Prefer: []string{strings.Repeat("p", 65)}}}, "Controls.Route.Prefer[0]", "too_long"},
		{"extra wrong namespace", treg.Controls{Extra: []treg.Control{{Name: "X-Other", Value: "v"}}}, "Controls.Extra[0].Name", "invalid_format"},
		{"extra empty suffix", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-", Value: "v"}}}, "Controls.Extra[0].Name", "invalid_format"},
		{"extra underscore", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-a_b", Value: "v"}}}, "Controls.Extra[0].Name", "invalid_character"},
		{"extra name control", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-a\r\nb", Value: "v"}}}, "Controls.Extra[0].Name", "control_character"},
		{"extra empty name", treg.Controls{Extra: []treg.Control{{Name: "", Value: "v"}}}, "Controls.Extra[0].Name", "required"},
		{"extra suffix long", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-" + strings.Repeat("a", 65), Value: "v"}}}, "Controls.Extra[0].Name", "too_long"},
		{"extra token", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-Token", Value: "v"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra org", treg.Controls{Extra: []treg.Control{{Name: "x-treg-org", Value: "v"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra client", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-Client", Value: "v"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra auth method", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-Authorization-Method", Value: "v"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra typed control", treg.Controls{Extra: []treg.Control{{Name: "X-TREG-META", Value: "a=b"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra max cost", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-Route-Max-Cost", Value: "1"}}}, "Controls.Extra[0].Name", "reserved_header"},
		{"extra duplicate", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-A", Value: "1"}, {Name: "x-treg-a", Value: "2"}}}, "Controls.Extra[1].Name", "duplicate"},
		{"extra value control", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-A", Value: "a\r\nb"}}}, "Controls.Extra[0].Value", "control_character"},
		{"extra value long", treg.Controls{Extra: []treg.Control{{Name: "X-Treg-A", Value: strings.Repeat("v", 1025)}}}, "Controls.Extra[0].Value", "too_long"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := testutil.NewScript()
			c := newClient(t, script)
			_, err := c.CallBytes(t.Context(), treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet, Controls: tt.ctl})
			requireValidation(t, err, tt.field, tt.reason)
			assert.Zero(t, script.Calls())
		})
	}
}

func TestControlsNoCacheConflict(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript()
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet,
		Header:   http.Header{"cache-control": {"max-age=0"}},
		Controls: treg.Controls{NoCache: true},
	})
	requireValidation(t, err, "Controls.NoCache", "conflict")
	assert.Zero(t, script.Calls())
}

func TestControlsBoundaries(t *testing.T) {
	t.Parallel()
	script := testutil.NewScript(testutil.Step{Body: `{}`})
	c := newClient(t, script)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet,
		Controls: treg.Controls{
			IdempotencyKey: strings.Repeat("k", 200),
			MaxCost:        new(treg.MicroUSD(1_000_000_000_000)),
			Tags: []treg.Tag{
				{Key: "a", Value: "1"}, {Key: "b", Value: "2"}, {Key: "c", Value: "3"}, {Key: "d", Value: "4"}, {Key: "e_9", Value: "Z.z:_-"},
			},
		},
	})
	require.NoError(t, err)
	h := testutil.OnlyRequest(t, script).Header
	assert.Equal(t, "1000000", h.Get("X-Treg-Route-Max-Cost"))
	assert.Equal(t, "a=1,b=2,c=3,d=4,e_9=Z.z:_-", h.Get("X-Treg-Meta"))
	//nolint:staticcheck // the SDK stores the key non-canonically on purpose
	assert.Equal(t, []string{strings.Repeat("k", 200)}, h["Idempotency-key"], "a 200-byte key is within the bound")
}
