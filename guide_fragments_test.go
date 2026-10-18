package treg_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
)

// This file holds the Go blocks of docs/integration-guide.md, each quoted
// verbatim inside code that compiles. TestReadmeGoBlocksAreCompiled
// requires every block of the guide to appear here or in the example
// package it quotes, so a block that would not compile fails the build.
// Only guideTestClient runs (offline); the other holders call Treg and are
// only compiled.

// The block of "From descriptor to Go types" that turns a descriptor into
// types: package-level, as in a package of your own.

var ErrTargets = errors.New("urlmetrics: 1 to 50 targets")

type Request struct {
	Targets []string `json:"targets"`
}

// guideOneOff holds the block of "Call directly, or write a package?".
func guideOneOff(ctx context.Context, client *treg.Client) (int, error) {
	type in struct {
		Targets []string `json:"targets"`
	}
	type out struct {
		Results []struct {
			Page            string `json:"page"`
			DomainAuthority *int   `json:"domain_authority"`
		} `json:"results"`
	}
	res, err := treg.CallJSON[out](ctx, client, treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"),
		Method: http.MethodPost,
		Body:   treg.JSONBody(in{Targets: []string{"example.com"}}),
	})
	if err != nil {
		return 0, err
	}
	return len(res.Value.Results), nil
}

// guideDescriptor holds the block of "From descriptor to Go types" that
// reads a descriptor.
func guideDescriptor(ctx context.Context) string {
	cat, err := treg.NewCatalogClient() // or client.Catalog(); catalog reads never send the token
	if err != nil {
		panic(err)
	}
	desc, err := cat.Endpoint(ctx, treg.EndpointRequest{ID: "moz.web.url.metrics"})
	if err != nil {
		panic(err)
	}
	ep := desc.Value.Endpoint
	verified, _ := ep.Verified.Get() // "2026-07-31" when urlmetrics was written
	return verified
}

// guideStream holds the block of "Choosing the method": the streaming
// shape, inside a function returning the guide's Result.
func guideStream(ctx context.Context, c *treg.Client, in any, ctl treg.Controls) (treg.Metadata, error) {
	type Result struct{ Meta treg.Metadata }
	stream := func() (Result, error) {
		resp, err := c.Call(ctx, treg.CallRequest{
			Target:   treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"),
			Method:   http.MethodPost,
			Header:   http.Header{"Model": {"s2.1-pro"}},
			Body:     treg.JSONBody(in),
			Controls: ctl,
		})
		if err != nil {
			return Result{}, err // a *treg.ValidationError (nothing sent) or a *treg.CallError
		}
		defer func() { _ = resp.Close() }() // ends the request and its timers
		res := Result{Meta: resp.Meta()}
		if err = resp.StatusError(ctx); err != nil {
			return res, err // a *treg.CallError for a 4xx or 5xx, with the Refusal parsed
		}
		// Check resp.Header().Get("Content-Type") before writing anything, then copy.
		return res, nil
	}
	res, err := stream()
	return res.Meta, err
}

// guideReconcile holds the block of "Errors" that reconciles an unknown
// outcome. The block prints with fmt.Println, as the README's blocks (the
// package's examples) do; forbidigo allows that only in godoc examples, and
// this holder is not one, so it would not be published.
//
//nolint:forbidigo // the guide's block prints, as the README's examples do
func guideReconcile(ctx context.Context, client *treg.Client, err error) {
	if ce, ok := errors.AsType[*treg.CallError](err); ok && errors.Is(err, treg.ErrOutcomeUnknown) {
		if ce.Meta.CallRef != "" {
			detail, gerr := client.Calls().Get(ctx, treg.GetCallRequest{Ref: ce.Meta.CallRef})
			if gerr == nil {
				charged, settled := detail.Value.ChargedMicro.Get()
				fmt.Println(detail.Value.Call, charged, settled) // what happened, and what the ledger settled
			}
		}
		// Without a ref (no response arrived), look through client.Calls().List.
	}
}

// guideRouted holds the block of "Routed ids".
func guideRouted(ctx context.Context, client *treg.Client, req treg.CallRequest) (treg.RouteOutcome, error) {
	type MyOutput struct {
		Answer string `json:"answer"`
	}
	res, err := treg.CallJSON[treg.Routed[MyOutput]](ctx, client, req)
	if err != nil {
		return "", err
	}
	return res.Value.Treg.Outcome, nil
}

// guideTestClient holds the block of "Testing your package".
func guideTestClient(t *testing.T, handler http.Handler) *treg.Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	c, err := treg.NewClient("test-token-0123456789abcdef",
		treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client()))
	require.NoError(t, err)
	return c
}

func TestGuideFragments(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, guideTestClient(t, http.NotFoundHandler()), "the testing block builds a client")
	for _, holder := range []any{ErrTargets, Request{}, guideOneOff, guideDescriptor, guideStream, guideReconcile, guideRouted} {
		assert.NotNil(t, holder)
	}
}
