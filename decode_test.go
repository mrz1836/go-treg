package treg_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// decodeWith answers one CallJSON with body and content type ct.
func decodeWith[T any](t *testing.T, ct, body string, opts ...treg.CallOption) (*treg.Result[T], error) {
	t.Helper()
	h := http.Header{}
	if ct != "" {
		h.Set("Content-Type", ct)
	}
	c := newClient(t, testutil.NewScript(testutil.Step{Header: h, Body: body}))
	return treg.CallJSON[T](t.Context(), c, getRequest(), opts...)
}

// decodes returns a check that body, served with the content type ct (none
// when empty), decodes into a T equal to want.
func decodes[T any](ct, body string, want T, opts ...treg.CallOption) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()
		res, err := decodeWith[T](t, ct, body, opts...)
		require.NoError(t, err)
		assert.Equal(t, want, res.Value)
	}
}

func TestDecodeValues(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		check func(t *testing.T)
	}{
		{"object", decodes("application/json", ` {"a":1} `, map[string]any{"a": 1.0})},
		{"array with charset", decodes("application/json; charset=utf-8", `[1,2,3]`, []int{1, 2, 3})},
		{"problem+json scalar", decodes("application/problem+json", `"ok"`, "ok")},
		{"null without a content type", decodes[*int]("", `null`, nil)},
		{
			"UseNumber keeps big numbers",
			decodes[any]("application/json", `{"n":12345678901234567890}`,
				map[string]any{"n": json.Number("12345678901234567890")}, treg.UseNumber()),
		},
		{"max int64", decodes("application/json", `{"N":9223372036854775807}`, struct{ N int64 }{N: 9223372036854775807})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}

func TestDecodeContentTypes(t *testing.T) {
	t.Parallel()
	for _, ct := range []string{"text/plain", "text/html", "text/csv", "application/octet-stream", "application/json-seq", "bogus;;"} {
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			_, err := decodeWith[any](t, ct, `{"a":1}`)
			requireCallError(t, err, callErrorWant{Kind: treg.FailureUnexpectedResponse, Reason: "content_type"})

			res, err := decodeWith[map[string]int](t, ct, `{"a":1}`, treg.IgnoreContentType())
			require.NoError(t, err)
			assert.Equal(t, 1, res.Value["a"])
		})
	}
	t.Run("repeated", func(t *testing.T) {
		t.Parallel()
		c := newClient(t, testutil.NewScript(testutil.Step{
			Header: http.Header{"Content-Type": {"application/json", "text/html"}}, Body: `{}`,
		}))
		_, err := treg.CallJSON[any](t.Context(), c, getRequest())
		require.ErrorIs(t, err, treg.ErrUnexpectedResponse, "a repeated Content-Type is refused")
	})
}

func TestDecodeContentEncoding(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		header http.Header
		body   string
		reason string // "" when the body decodes
	}{
		{
			"br is refused",
			http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"br"}},
			"\x1b\x00", "content_encoding",
		},
		{
			"identity in any case is accepted",
			http.Header{"Content-Type": {"application/json"}, "content-encoding": {"identity"}},
			`{}`, "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{Header: tt.header, Body: tt.body}))
			_, err := treg.CallJSON[any](t.Context(), c, getRequest(), treg.CaptureResponse(true))
			if tt.reason == "" {
				require.NoError(t, err)
				return
			}
			ce := requireCallError(t, err, callErrorWant{Kind: treg.FailureUnexpectedResponse, Reason: tt.reason})
			assert.Equal(t, treg.CaptureUnavailable, ce.Captured().State())
		})
	}
}

func TestDecodeMalformed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{"invalid", `{"a":}`, "invalid_json"},
		{"truncated", `{"a":[1,2`, "truncated_json"},
		{"trailing", `{"a":1} {"b":2}`, "trailing_data"},
		{"trailing garbage", `{"a":1}x`, "trailing_data"},
		{"wrong type", `{"a":"x"}`, "decode"},
		{"html", `<html>`, "invalid_json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeWith[struct{ A int }](t, "application/json", tt.body, treg.CaptureResponse(true))
			// OutcomeUnknown is false: the 2xx executed.
			ce := requireCallError(t, err, callErrorWant{Kind: treg.FailureMalformedResponse, Reason: tt.reason})
			assert.Equal(t, treg.CaptureComplete, ce.Captured().State(), "the whole body was read, so it is captured")
			assert.Equal(t, tt.body, string(ce.Captured().Bytes()))
			assert.NotContains(t, err.Error(), tt.body)
		})
	}
}

func TestDecodeCallerTypesAreNotStrict(t *testing.T) {
	t.Parallel()
	res, err := decodeWith[map[string]int](t, "application/json", `{"a":1,"a":2}`)
	require.NoError(t, err, "the caller's type follows encoding/json: last duplicate wins")
	assert.Equal(t, 2, res.Value["a"])
}

func TestDecodeStrictModels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{"duplicate", `{"org":"a","org":"b","used_today":1,"cap":2}`, "duplicate_key"},
		{"duplicate case", `{"org":"a","ORG":"b","used_today":1,"cap":2}`, "duplicate_key"},
		{"duplicate escaped", testutil.Esc(`{"org":"a","~u006frg":"b","used_today":1,"cap":2}`), "duplicate_key"},
		{"quoted number", `{"org":"a","used_today":"1","cap":2}`, "decode"},
		{"too deep", `{"org":` + strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + `}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{Body: tt.body}))
			_, err := c.Account().Usage(t.Context(), treg.MemberUsageRequest{})
			require.ErrorIs(t, err, treg.ErrMalformedResponse)
			if tt.reason != "" {
				assert.Equal(t, tt.reason, testutil.RequireErrorAs[*treg.CallError](t, err).Reason)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	for _, s := range []string{`{}`, `[]`, `null`, `"x"`, `{"a":1,"a":2}`, `{"a":`, `1 2`, `{"detail":{"error":"x"}}`, "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		c := newClient(t, testutil.NewScript(
			testutil.Step{Body: body},
			testutil.Step{Body: body},
			testutil.Step{Status: 502, Body: body},
		))
		res, err := treg.CallJSON[any](t.Context(), c, getRequest(), treg.CaptureResponse(true))
		if err == nil {
			assert.True(t, json.Valid([]byte(body)))
			assert.Equal(t, body, string(res.Captured.Bytes()))
		} else {
			_ = testutil.RequireErrorAs[*treg.CallError](t, err)
		}
		_, err = c.Account().Usage(t.Context(), treg.MemberUsageRequest{})
		if err != nil {
			_ = testutil.RequireErrorAs[*treg.CallError](t, err)
		}
		_, err = treg.CallJSON[any](t.Context(), c, getRequest())
		require.ErrorIs(t, err, treg.ErrHTTPStatus)
	})
}
