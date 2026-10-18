// Command offline walks through go-treg against a local fake of Treg: no
// network, no token, nothing billed. It discovers an endpoint, calls it with
// a caller-owned type, streams a binary answer and reads a refusal.
//
//	go run ./examples/offline
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/mrz1836/go-treg"
)

// Outcomes the walkthrough does not expect. Each one stops it with an error.
var (
	errNoResults  = errors.New("offline: the search found nothing")
	errNoMetrics  = errors.New("offline: the call returned no domain authority")
	errNotRefused = errors.New("offline: the paid call was not refused")
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "offline:", err)
		os.Exit(1)
	}
}

// fakeTreg answers the four requests the walkthrough makes.
func fakeTreg() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/catalog/search":
			_, _ = io.WriteString(w, `{"query":"url metrics","count":1,"total":1,"hints":[],`+
				`"results":[{"id":"moz.web.url.metrics","kind":"data","method":"POST"}]}`)
		case "/call/moz.web.url.metrics":
			w.Header().Set("X-Treg-Call-Id", "0123456789abcdef0123456789abcdef")
			w.Header().Set("X-Treg-Cost-Micro", "6667")
			_, _ = io.WriteString(w, `{"results":[{"page":"example.com/","domain_authority":91}]}`)
		case "/call/fishaudio.tts.s2-1-pro":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = io.WriteString(w, "ID3 fake audio bytes")
		default:
			w.Header().Set("X-Treg-Error", "1")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"detail":{"error":"insufficient_balance","balance_micro":0,"estimated_cost_micro":24000}}`)
		}
	})
}

// run serves the fake over local TLS and performs the walkthrough against it.
func run(ctx context.Context, out io.Writer) error {
	srv := httptest.NewTLSServer(fakeTreg())
	defer srv.Close()
	opts := []treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}
	return walkthrough(ctx, out, opts...)
}

// walkthrough discovers an endpoint, calls it, streams a binary answer and
// reads a refusal through a Client built with opts, writing what it sees to
// out. An outcome it does not expect is an error.
func walkthrough(ctx context.Context, out io.Writer, opts ...treg.Option) error {
	client, err := treg.NewClient("trg_offline_example_0123456789", opts...)
	if err != nil {
		return err
	}
	if err = discover(ctx, client, out); err != nil {
		return err
	}
	if err = callMetrics(ctx, client, out); err != nil {
		return err
	}
	if err = streamAudio(ctx, client, out); err != nil {
		return err
	}
	return readRefusal(ctx, client, out)
}

// discover searches the catalog: public, unauthenticated, and never executes
// anything.
func discover(ctx context.Context, client *treg.Client, out io.Writer) error {
	found, err := client.Catalog().Search(ctx, treg.SearchRequest{Query: "url metrics", Limit: 5})
	if err != nil {
		return err
	}
	if len(found.Value.Results) == 0 {
		return errNoResults
	}
	_, _ = fmt.Fprintln(out, "found:", found.Value.Results[0].ID)
	return nil
}

// callMetrics calls the endpoint with a type you own.
func callMetrics(ctx context.Context, client *treg.Client, out io.Writer) error {
	type metrics struct {
		Results []struct {
			Page            string `json:"page"`
			DomainAuthority *int   `json:"domain_authority"`
		} `json:"results"`
	}
	res, err := treg.CallJSON[metrics](ctx, client, treg.CallRequest{
		Target:   treg.CatalogEndpoint("moz.web.url.metrics"),
		Method:   http.MethodPost,
		Body:     treg.JSONBody(map[string][]string{"targets": {"example.com"}}),
		Controls: treg.Controls{MaxCost: new(treg.MicroUSD(20_000)), IdempotencyKey: "offline-demo-1"},
	})
	if err != nil {
		return err
	}
	if len(res.Value.Results) == 0 || res.Value.Results[0].DomainAuthority == nil {
		return errNoMetrics
	}
	_, _ = fmt.Fprintln(out, "domain authority:", *res.Value.Results[0].DomainAuthority, "cost:", res.Meta.CostMicro)
	return nil
}

// streamAudio streams a binary answer.
func streamAudio(ctx context.Context, client *treg.Client, out io.Writer) error {
	resp, err := client.Call(ctx, treg.CallRequest{
		Target: treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"),
		Method: http.MethodPost,
		Header: http.Header{"Model": {"s2.1-pro"}},
		Body:   treg.JSONBody(map[string]string{"text": "Hello from example.com"}),
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Close() }()
	if serr := resp.StatusError(ctx); serr != nil {
		return serr
	}
	n, err := io.Copy(io.Discard, resp)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "audio bytes:", n)
	return nil
}

// readRefusal makes a paid call the fake refuses: a refusal is a typed error
// that prints no data.
func readRefusal(ctx context.Context, client *treg.Client, out io.Writer) error {
	_, err := treg.CallJSON[any](ctx, client, treg.CallRequest{
		Target: treg.CatalogEndpoint("vendor.paid.thing"), Method: http.MethodGet,
	})
	ce, ok := errors.AsType[*treg.CallError](err)
	switch {
	case err == nil:
		return errNotRefused
	case !ok || ce.Refusal == nil:
		return err // not a refusal: a local, transport or unparsed failure
	}
	_, _ = fmt.Fprintln(out, "refused:", ce.Refusal.Code, "gateway:", errors.Is(err, treg.ErrGatewayRefusal))
	return nil
}
