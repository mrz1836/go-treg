// Command quickstart reads Treg's live public catalog, and with a token your
// own usage. It is read-only: it never calls /call/, so it executes and
// bills nothing. It makes no request unless you pass -live.
//
//	go run ./examples/quickstart -live -query "url metrics"
//	TREG_TOKEN=... go run ./examples/quickstart -live
//
// TREG_TOKEN is read here, by the example, never by the SDK: go-treg reads no
// environment variables.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mrz1836/go-treg"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(realMain(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// realMain parses args, reads TREG_TOKEN through getenv and, with -live,
// runs the walkthrough against https://treg.to. What the program reports
// goes to stdout; errors, and the flag package's messages and -h help, go
// to stderr. It returns the exit code: 0 on success, 1 when a step fails
// and 2 for bad arguments.
func realMain(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	live := flags.Bool("live", false, "make read-only requests to https://treg.to")
	query := flags.String("query", "url metrics", "catalog search text")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "quickstart: unexpected arguments; see -h")
		return exitUsage
	}
	if !*live {
		_, _ = fmt.Fprintln(stdout, "dry run: pass -live to search the public catalog (and read your usage if TREG_TOKEN is set)")
		return exitOK
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := run(ctx, stdout, *query, getenv("TREG_TOKEN")); err != nil {
		_, _ = fmt.Fprintln(stderr, "quickstart:", err)
		return exitError
	}
	return exitOK
}

// run searches the catalog, shows the first result's descriptor and, with a
// token, the caller's usage today, writing what it reads to out; failures
// are returned, never written. opts configure both clients, such as a local
// test server's WithBaseURL and WithHTTPClient.
//
// Each error is labeled with the step that failed. A *treg.CallError
// already prints "(HTTP n)" when Treg answered, and nothing about a status
// when it did not (a transport failure, a timeout), so no status is added.
func run(ctx context.Context, out io.Writer, query, token string, opts ...treg.Option) error {
	say := func(a ...any) { _, _ = fmt.Fprintln(out, a...) }
	cat, err := treg.NewCatalogClient(opts...)
	if err != nil {
		return fmt.Errorf("catalog client: %w", err)
	}
	found, err := cat.Search(ctx, treg.SearchRequest{Query: query, Limit: 5})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	say("results:", found.Value.Count, "of", found.Value.Total)
	for _, ep := range found.Value.Results {
		cost, _ := ep.Cost.Get()
		usd, _ := cost.USD.Get()
		say(" ", ep.ID, ep.Kind, cost.Type, usd)
	}
	if len(found.Value.Results) == 0 {
		return nil
	}
	desc, err := cat.Endpoint(ctx, treg.EndpointRequest{ID: found.Value.Results[0].ID})
	if err != nil {
		return fmt.Errorf("endpoint: %w", err)
	}
	verified, _ := desc.Value.Endpoint.Verified.Get()
	say("first:", desc.Value.Endpoint.Method, desc.Value.Endpoint.Path, "verified", verified)

	if token == "" {
		say("usage: skipped (set TREG_TOKEN to read your own usage)")
		return nil
	}
	client, err := treg.NewClient(token, opts...)
	if err != nil {
		return fmt.Errorf("client: %w", err)
	}
	usage, err := client.Account().Usage(ctx, treg.MemberUsageRequest{})
	if err != nil {
		return fmt.Errorf("usage: %w", err)
	}
	say("usage today:", usage.Value.UsedToday, "cap:", usage.Value.Cap)
	return nil
}
