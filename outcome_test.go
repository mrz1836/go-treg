package treg_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// errTestRefused is a scripted transport failure.
var errTestRefused = errors.New("refused")

func TestOutcomeTransportErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		err     error
		kind    treg.FailureKind
		reason  string
		unknown bool
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "treg.test"}, treg.FailureTransport, "dns", false},
		{"dial", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, treg.FailureTransport, "dial", false},
		{"proxy", &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errTestRefused}, treg.FailureTransport, "proxy", false},
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, treg.FailureTransport, "reset", true},
		{"eof", io.ErrUnexpectedEOF, treg.FailureTransport, "closed", true},
		{"other", fmt.Errorf("strange failure with https://secret.example/?k=v: %w", errTestRefused), treg.FailureTransport, "other", true},
		{"net timeout", testutil.TimeoutError{Msg: "i/o timeout at secret.example"}, treg.FailureTimeout, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(testutil.Step{Err: tt.err}))
			_, err := c.CallBytes(t.Context(), getRequest())
			// An injected transport gives no WroteHeaders evidence, so only the
			// failure's class decides OutcomeUnknown.
			requireCallError(t, err, callErrorWant{Kind: tt.kind, Reason: tt.reason, OutcomeUnknown: tt.unknown})
			assert.NotContains(t, err.Error(), "secret.example")
			cause := errors.Unwrap(err)
			require.Error(t, cause, "the CallError wraps a fixed cause")
			assert.NotContains(t, cause.Error(), "secret.example")

			typed := newCatalog(t, testutil.NewScript(testutil.Step{Err: tt.err}))
			_, err = typed.Platforms(t.Context(), treg.PlatformsRequest{})
			requireCallError(t, err, callErrorWant{}) // typed reads never set OutcomeUnknown
		})
	}
}

func TestOutcomeAfterHeadersWritten(t *testing.T) {
	t.Parallel()
	drop := testutil.NewDropServer(t, testutil.Step{Body: `{}`}, func(int) bool { return true })
	c := newClient(t, drop)
	_, err := c.CallBytes(t.Context(), treg.CallRequest{
		Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodPost,
		Body: treg.JSONBody(map[string]int{"n": 1}),
	})
	ce := testutil.RequireErrorAs[*treg.CallError](t, err)
	assert.Equal(t, treg.FailureTransport, ce.Kind)
	assert.True(t, ce.OutcomeUnknown, "the server read the request before the connection dropped")
	assert.Equal(t, 1, drop.Hits(), "exactly one attempt")
}

func TestOutcomeTimeoutAndCancel(t *testing.T) {
	t.Parallel()
	block := testutil.Step{Respond: testutil.BlockUntilDone}
	tests := []struct {
		name string
		run  func(t *testing.T) error
		want callErrorWant
		// timedOut is what CallError.Timeout reports.
		timedOut bool
	}{
		{"client timeout", func(t *testing.T) error {
			t.Helper()
			c := newClient(t, testutil.NewScript(block), treg.WithTimeout(10*time.Millisecond))
			_, err := c.CallBytes(t.Context(), getRequest())
			return err
		}, callErrorWant{Kind: treg.FailureTimeout, Is: context.DeadlineExceeded, OutcomeUnknown: true}, true},
		{"caller cancel", func(t *testing.T) error {
			t.Helper()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := newClient(t, testutil.NewScript(testutil.Step{Respond: func(r *http.Request) (*http.Response, error) {
				cancel()
				return testutil.BlockUntilDone(r)
			}}))
			_, err := c.CallBytes(ctx, getRequest())
			return err
		}, callErrorWant{Kind: treg.FailureCanceled, OutcomeUnknown: true}, false},
		{"caller deadline", func(t *testing.T) error {
			t.Helper()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			_, err := newClient(t, testutil.NewScript(block)).CallBytes(ctx, getRequest())
			return err
		}, callErrorWant{Kind: treg.FailureDeadlineExceeded, OutcomeUnknown: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				err := tt.run(t)
				ce := requireCallError(t, err, tt.want)
				assert.Equal(t, tt.timedOut, ce.Timeout())
				if tt.want.Kind != treg.FailureTimeout {
					require.NotErrorIs(t, err, treg.ErrTimeout, "the caller's own context is not the SDK's timeout")
				}
			})
		})
	}
}

func TestOutcomeReceivedStatuses(t *testing.T) {
	t.Parallel()
	partial := testutil.NewBody(`{"partial":`)
	partial.End = io.ErrUnexpectedEOF
	tests := []struct {
		name    string
		step    testutil.Step
		unknown bool
	}{
		{"2xx read failure", testutil.Step{Respond: testutil.TransportWith(http.StatusOK, nil, -1, partial)}, false},
		{"404", testutil.Step{Status: 404, Body: `{"detail":"x"}`}, false},
		{"429", testutil.Step{Status: 429, Body: `{"detail":{"error":"share_busy"}}`}, false},
		{"500", testutil.Step{Status: 500, Body: `{}`}, true},
		{"502 relay failure", testutil.Step{Status: 502, Body: `{"detail":{"error":"connect_failed"}}`}, true},
		{"503 saturated", testutil.Step{Status: 503, Body: `{"detail":"busy","treg_saturated":true}`}, false},
		{"503 paused", testutil.Step{Status: 503, Body: `{"detail":{"error":"provider_paused"}}`}, false},
		{"503 capacity", testutil.Step{Status: 503, Body: `{"detail":{"error":"provider_capacity_unavailable"}}`}, false},
		{"503 unproven", testutil.Step{Status: 503, Body: `{"detail":"Service Unavailable"}`}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(t, testutil.NewScript(tt.step))
			_, err := treg.CallJSON[any](t.Context(), c, getRequest())
			requireCallError(t, err, callErrorWant{OutcomeUnknown: tt.unknown})
		})
	}
}

func TestOutcomeStreamReadFailure(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status  int
		unknown bool
	}{
		{http.StatusOK, false},
		{http.StatusInternalServerError, true},
	} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			t.Parallel()
			body := testutil.NewBody("abc")
			body.End = io.ErrUnexpectedEOF
			c := newClient(t, testutil.TransportWith(tt.status, nil, -1, body))
			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			got, err := io.ReadAll(resp)
			assert.Equal(t, "abc", string(got))
			ce := testutil.RequireErrorAs[*treg.CallError](t, err)
			assert.Equal(t, treg.FailureTransport, ce.Kind)
			assert.Equal(t, tt.unknown, ce.OutcomeUnknown)
			require.NoError(t, resp.Close())
		})
	}
}
