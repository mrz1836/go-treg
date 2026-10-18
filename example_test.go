package treg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/mrz1836/go-treg"
)

// exampleFake is a local stand-in for Treg in the examples: it answers each
// path with a fixed status, headers and body.
type exampleFake map[string]struct {
	status int
	header map[string]string
	body   string
}

// start serves the fake over TLS and returns a Client and a CatalogClient
// that talk to it, and a function that stops it.
func (f exampleFake) start() (*treg.Client, *treg.CatalogClient, func()) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := f[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		if a.status != 0 {
			w.WriteHeader(a.status)
		}
		_, _ = io.WriteString(w, a.body)
	}))
	opts := []treg.Option{treg.WithBaseURL(srv.URL), treg.WithHTTPClient(srv.Client())}
	client, err := treg.NewClient("trg_example_0123456789abcdef", opts...)
	if err != nil {
		panic(err)
	}
	catalog, err := treg.NewCatalogClient(opts...)
	if err != nil {
		panic(err)
	}
	return client, catalog, srv.Close
}

const exampleCallRef = "0123456789abcdef0123456789abcdef"

// errNoCatalogPoll is ExampleAsyncDescriptor's error for a descriptor
// without a catalog poll endpoint.
var errNoCatalogPoll = errors.New("no catalog poll endpoint")

func ExampleCatalogClient_Search() {
	_, cat, stop := exampleFake{
		"/catalog/search": {body: `{"query":"url metrics","count":1,"total":1,"hints":[],"results":[` +
			`{"id":"moz.web.url.metrics","kind":"data","cost":{"type":"quota_rows","usd":0.006667}}]}`},
		"/catalog/endpoints/moz.web.url.metrics": {body: `{"endpoint":{"id":"moz.web.url.metrics","method":"POST",` +
			`"input":{"bodyType":"json","body":{"targets":{"type":"array[string]","required":true}}}},` +
			`"provider":{"service":"moz","display_name":"Moz"},"siblings":[],"call_template":"","example_response":null,"hints":[]}`},
	}.start()
	defer stop()
	ctx := context.Background()

	found, err := cat.Search(ctx, treg.SearchRequest{Query: "url metrics", Limit: 5})
	if err != nil {
		panic(err)
	}
	for _, ep := range found.Value.Results {
		cost, _ := ep.Cost.Get()
		fmt.Println(ep.ID, ep.Kind, cost.Type) // ranking is advice; the SDK never picks or runs one
	}
	desc, err := cat.Endpoint(ctx, treg.EndpointRequest{ID: "moz.web.url.metrics"})
	if err != nil {
		panic(err)
	}
	fmt.Println(desc.Value.Endpoint.Method, string(desc.Value.Endpoint.Input) != "") // Treg's own descriptor, not JSON Schema
	// Output:
	// moz.web.url.metrics data quota_rows
	// POST true
}

func ExampleCallJSON() {
	client, _, stop := exampleFake{"/call/moz.web.url.metrics": {
		header: map[string]string{"Content-Type": "application/json", "X-Treg-Call-Id": exampleCallRef, "X-Treg-Cost-Micro": "6667"},
		body:   `{"results":[{"page":"example.com/","domain_authority":91,"page_authority":null,"spam_score":1}]}`,
	}}.start()
	defer stop()
	ctx := context.Background()

	type urlMetricsIn struct {
		Targets []string `json:"targets"`
	}
	type urlMetricsOut struct {
		Results []struct {
			Page            string `json:"page"`
			DomainAuthority *int   `json:"domain_authority"`
			PageAuthority   *int   `json:"page_authority"`
			SpamScore       *int   `json:"spam_score"`
		} `json:"results"`
	}
	res, err := treg.CallJSON[urlMetricsOut](ctx, client, treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"),
		Method: http.MethodPost,
		Body:   treg.JSONBody(urlMetricsIn{Targets: []string{"example.com"}}),
		Controls: treg.Controls{
			MaxCost:        new(treg.MicroUSD(20_000)), // $0.02 server-enforced reserve ceiling
			IdempotencyKey: "job-42-urlmetrics",        // reuse ONLY to recover this exact request
		},
	})
	if err != nil {
		panic(err) // encoding/validation errors return here before any byte is sent
	}
	fmt.Println(*res.Value.Results[0].DomainAuthority, res.Value.Results[0].PageAuthority == nil)
	fmt.Println(res.Meta.CallRef, res.Meta.CostMicro.DecimalUSD())
	// Output:
	// 91 true
	// 0123456789abcdef0123456789abcdef 0.006667
}

func ExampleClient_CallBytes() {
	client, _, stop := exampleFake{"/call/future.vendor.resource.read": {
		header: map[string]string{"Content-Type": "text/csv"}, body: "id,name\nabc,Example\n",
	}}.start()
	defer stop()
	ctx := context.Background()
	process := func(b []byte) { fmt.Print(string(b)) }

	raw, err := client.CallBytes(ctx, treg.CallRequest{
		Target: treg.CatalogEndpoint("future.vendor.resource.read"),
		Method: http.MethodGet,
		Query:  url.Values{"limit": {"5"}, "id": {"abc"}}, // {id}-style path params ride in the query (server-verified)
	})
	if err != nil {
		panic(err) // local/transport/size failures only
	}
	if err := raw.StatusError(); err != nil { // non-2xx stays inspectable; Refusal parsed when Treg refused
		panic(err)
	}
	process(raw.Body())
	// Output:
	// id,name
	// abc,Example
}

func ExampleClient_Call() {
	client, _, stop := exampleFake{"/call/fishaudio.tts.s2-1-pro": {
		header: map[string]string{"Content-Type": "audio/mpeg"}, body: "ID3\x04\x00\x00fake-audio",
	}}.start()
	defer stop()
	ctx := context.Background()
	var dst bytes.Buffer

	resp, err := client.Call(ctx, treg.CallRequest{
		Target: treg.CatalogEndpoint("fishaudio.tts.s2-1-pro"),
		Method: http.MethodPost,
		Header: http.Header{"Model": {"s2.1-pro"}},
		Body: treg.JSONBody(struct {
			Text string `json:"text"`
		}{"Hello from example.com"}),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Close() }() // ends the request and both timers; no goroutine survives
	if serr := resp.StatusError(ctx); serr != nil {
		panic(serr)
	}
	n, err := io.Copy(&dst, resp) // ≤ WithMaxStreamBytes; ctx/WithStreamTimeout stop it; n = bytes written
	if err != nil {
		panic(err)
	}
	fmt.Println(n, resp.Header().Get("Content-Type"))
	// Output:
	// 16 audio/mpeg
}

func ExampleRouted() {
	client, _, stop := exampleFake{"/call/treg.people.email.find": {
		status: http.StatusAccepted, header: map[string]string{"Content-Type": "application/json"},
		body: `{"output":{"email":null},"raw":null,"_treg":{"served_by":"vendor.people.email.find","provider":"vendor",` +
			`"tier":"platform","outcome":"pending","tried":[],"call_ref":"` + exampleCallRef + `:r0",` +
			`"async":{"task_id":"t-1","poll":{},"status":{}},"reserved_micro":50000,"charged_micro":null}}`,
	}}.start()
	defer stop()
	ctx := context.Background()

	type emailOut struct {
		Email      *string         `json:"email"`
		Confidence json.RawMessage `json:"confidence"`
	}
	res, err := treg.CallJSON[treg.Routed[emailOut]](ctx, client, treg.CallRequest{
		Target:   treg.CatalogEndpoint("treg.people.email.find"),
		Method:   http.MethodPost,
		Body:     treg.JSONBody(map[string]string{"domain": "example.com", "full_name": "Ada Example"}),
		Controls: treg.Controls{MaxCost: new(treg.MicroUSD(100_000)), Route: treg.RouteControls{Waterfall: new(false)}},
	})
	if err != nil {
		panic(err)
	}
	switch res.Value.Treg.Outcome {
	case treg.RouteOutcomePending: // HTTP 202: keep Treg.CallRef + Treg.Async; never resubmit; check later explicitly
		fmt.Println("pending", res.Meta.StatusCode, res.Value.Treg.CallRef, res.Value.Treg.ChargedMicro.IsNull())
	case treg.RouteOutcomeMiss: // 200 with output.email == null; may still be charged (Treg.ChargedMicro)
		fmt.Println("miss")
	case treg.RouteOutcomeHit, treg.RouteOutcomeWeak:
		fmt.Println("served by", res.Meta.ServedBy)
	}
	// Output:
	// pending 202 0123456789abcdef0123456789abcdef:r0 true
}

func ExampleAsyncDescriptor() {
	client, _, stop := exampleFake{
		"/call/replicate.image-gen.flux-schnell": {status: http.StatusCreated, header: map[string]string{
			"Content-Type": "application/json", "X-Treg-Cost-Micro": "3000",
			"X-Treg-Async": `{"id_from":"id","poll":{"endpoint":"replicate.predictions.get","param":{"in":"pathParams","name":"id"}},` +
				`"status":{"path":"status","success":["succeeded"],"failure":["failed","canceled"]},"result":{"path":"output"},"interval":2}`,
		}, body: `{"id":"pred-1","status":"starting"}`},
		"/call/replicate.predictions.get": {
			header: map[string]string{"Content-Type": "application/json"},
			body:   `{"id":"pred-1","status":"processing"}`,
		},
	}.start()
	defer stop()
	ctx := context.Background()
	submitReq := treg.CallRequest{
		Target: treg.CatalogEndpoint("replicate.image-gen.flux-schnell"), Method: http.MethodPost,
		Body: treg.JSONBody(map[string]any{"input": map[string]string{"prompt": "a lighthouse at dusk"}}),
	}
	run := func() error {
		sub, err := client.CallBytes(ctx, submitReq) // the one execution
		if err != nil {
			return err
		}
		d := sub.Meta().Async // nil → not async; when set, Meta().CostMicro is a RESERVATION
		var pred struct {
			ID string `json:"id"`
		} // caller's own type; d.IDFrom == "id" says where the id lives
		if jerr := json.Unmarshal(sub.Body(), &pred); jerr != nil {
			return jerr
		}
		if d == nil || d.Poll.Param == nil {
			return errNoCatalogPoll
		} // url_from form: caller validates host ∈ d.Poll.URLHosts, then UpstreamURL
		st, err := client.CallBytes(ctx, treg.CallRequest{
			Target: treg.CatalogEndpoint(d.Poll.Endpoint),    // descriptor data, still validated as a target
			Method: http.MethodGet,                           // the poll endpoint's own descriptor decides; caller's choice
			Query:  url.Values{d.Poll.Param.Name: {pred.ID}}, // path params ride in the query
		})
		if err != nil {
			return err
		}
		// Interpreting st against d.Status is the caller's code; unknown statuses are never success.
		fmt.Println(sub.Meta().CostMicro.DecimalUSD(), strings.TrimSpace(string(st.Body())))
		return nil
	}
	if err := run(); err != nil {
		panic(err)
	}
	// Output:
	// 0.003 {"id":"pred-1","status":"processing"}
}

func ExampleCallError() {
	client, _, stop := exampleFake{"/call/moz.web.url.metrics": {
		status: http.StatusPaymentRequired,
		header: map[string]string{"Content-Type": "application/json", "X-Treg-Error": "1", "X-Treg-Call-Id": exampleCallRef},
		body:   `{"detail":{"error":"insufficient_balance","balance_micro":1200,"estimated_cost_micro":6667,"topup_url":"/app#billing"}}`,
	}}.start()
	defer stop()
	ctx := context.Background()

	_, err := treg.CallJSON[json.RawMessage](ctx, client, treg.CallRequest{
		Target: treg.CatalogEndpoint("moz.web.url.metrics"), Method: http.MethodPost,
		Body: treg.JSONBody(map[string][]string{"targets": {"example.com"}}),
	})
	fmt.Println(err)
	if ce, ok := errors.AsType[*treg.CallError](err); ok && ce.Refusal != nil {
		fmt.Println(errors.Is(err, treg.ErrGatewayRefusal), ce.Refusal.Code, ce.Refusal.BalanceMicro.DecimalUSD())
	}
	if errors.Is(err, treg.ErrOutcomeUnknown) {
		fmt.Println("reconcile with Calls().Get before repeating")
	}
	// Output:
	// treg: call: http_status (HTTP 402, gateway): insufficient_balance
	// true insufficient_balance 0.0012
}

func ExampleMicroUSD() {
	m, err := treg.ParseDecimalUSD("0.02")
	if err != nil {
		panic(err)
	}
	fmt.Println(int64(m), m.DecimalUSD(), m)
	// Output:
	// 20000 0.02 $0.02
}
