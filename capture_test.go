package treg_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// errTestBoom is a scripted transport failure.
var errTestBoom = errors.New("boom")

const captureBody = "{ \"b\" : 1.50,\n  \"a\":[1E2, -0.0] }\n"

// captureCase is one capture scenario: the answer, built from the entry
// point's fixture, the client and call options, the failure when the call
// fails, and the state of the capture it leaves. A complete capture holds the
// answer's body byte for byte; any other holds no bytes.
type captureCase struct {
	name     string
	step     func(fixture string) testutil.Step
	opts     []treg.Option
	callOpts []treg.CallOption

	// failure is nil when the call succeeds. Its Op is the entry point's,
	// and its OutcomeUnknown is callUnknown for a /call/ and false for a
	// typed read, which never leaves the outcome open.
	failure     *callErrorWant
	callUnknown bool

	state treg.CaptureState
}

// captureCases returns the capture scenarios every entry point meets: which
// option wins, and the calls whose bytes are never captured, a non-2xx
// answer, an oversized body and a transport failure.
func captureCases() []captureCase {
	ok := func(fixture string) testutil.Step { return testutil.Step{Body: fixture} }
	answer := func(step testutil.Step) func(string) testutil.Step {
		return func(string) testutil.Step { return step }
	}
	notFound := answer(testutil.Step{Status: http.StatusNotFound, Body: `{"detail":"nope"}`})
	boom := answer(testutil.Step{Err: errTestBoom})
	clientOn := []treg.Option{treg.WithResponseCapture()}
	on := []treg.CallOption{treg.CaptureResponse(true)}
	return []captureCase{
		{name: "off by default", step: ok, state: treg.CaptureDisabled},
		{name: "client option", step: ok, opts: clientOn, state: treg.CaptureComplete},
		{name: "call option on", step: ok, callOpts: on, state: treg.CaptureComplete},
		{
			name: "call option off overrides the client", step: ok, opts: clientOn,
			callOpts: []treg.CallOption{treg.CaptureResponse(false)}, state: treg.CaptureDisabled,
		},
		{
			name: "nil call option is skipped", step: ok, callOpts: []treg.CallOption{nil, treg.CaptureResponse(true)},
			state: treg.CaptureComplete,
		},
		{
			name: "last call option wins", step: ok,
			callOpts: []treg.CallOption{treg.CaptureResponse(false), nil, treg.CaptureResponse(true)}, state: treg.CaptureComplete,
		},
		{
			name: "error status is unavailable", step: notFound, callOpts: on,
			failure: &callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusNotFound, Marked: new(false)},
			state:   treg.CaptureUnavailable,
		},
		{
			name: "error status without capture", step: notFound,
			failure: &callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusNotFound}, state: treg.CaptureDisabled,
		},
		{
			name:        "non-2xx bytes are never captured",
			step:        answer(testutil.Step{Status: http.StatusInternalServerError, Body: `{"secret":"x"}`}),
			opts:        []treg.Option{treg.WithResponseCapture(), treg.WithMaxResponseBytes(4)},
			failure:     &callErrorWant{Kind: treg.FailureHTTPStatus, Status: http.StatusInternalServerError},
			callUnknown: true, state: treg.CaptureUnavailable,
		},
		{
			// A capture is never truncated: an oversized body fails the call.
			name: "too large is unavailable", step: ok, opts: []treg.Option{treg.WithMaxResponseBytes(16)}, callOpts: on,
			failure: &callErrorWant{Kind: treg.FailureResponseTooLarge, Status: http.StatusOK}, state: treg.CaptureUnavailable,
		},
		{
			name: "too large with the client option", step: answer(testutil.Step{Body: `{"too":"large"}`}),
			opts:    []treg.Option{treg.WithResponseCapture(), treg.WithMaxResponseBytes(4)},
			failure: &callErrorWant{Kind: treg.FailureResponseTooLarge, Status: http.StatusOK}, state: treg.CaptureUnavailable,
		},
		{
			name: "malformed 2xx keeps its bytes", step: func(fixture string) testutil.Step { return testutil.Step{Body: fixture + "{}"} },
			callOpts: on, state: treg.CaptureComplete,
			failure: &callErrorWant{Kind: treg.FailureMalformedResponse, Reason: "trailing_data", Status: http.StatusOK},
		},
		{
			name: "transport failure", step: boom, opts: clientOn,
			failure:     &callErrorWant{Kind: treg.FailureTransport, Reason: "other"},
			callUnknown: true, state: treg.CaptureUnavailable,
		},
		{
			name: "transport failure without capture", step: boom,
			failure:     &callErrorWant{Kind: treg.FailureTransport, Reason: "other"},
			callUnknown: true, state: treg.CaptureDisabled,
		},
	}
}

// captureRun makes one call through an entry point, on a Client with opts
// whose transport answers step, with callOpts, and returns the capture of
// its result, or its error.
type captureRun func(t *testing.T, step testutil.Step, opts []treg.Option, callOpts ...treg.CallOption) (treg.Capture, error)

// checkCaptureCases runs every case of captureCases, then each of extra,
// through run in a parallel subtest, with the answers built from fixture,
// and checks the capture each call leaves. op is the entry point's
// operation: a failure's Op, and treg.OpCall for a /call/, whose failures
// may leave the outcome open.
func checkCaptureCases(t *testing.T, fixture string, op treg.Operation, run captureRun, extra ...captureCase) {
	t.Helper()
	for _, cc := range append(captureCases(), extra...) {
		t.Run(cc.name, func(t *testing.T) {
			t.Parallel()
			step := cc.step(fixture)
			captured, err := run(t, step, cc.opts, cc.callOpts...)
			if cc.failure == nil {
				require.NoError(t, err)
			} else {
				want := *cc.failure
				want.Op = op
				want.OutcomeUnknown = cc.callUnknown && op == treg.OpCall
				ce := requireCallError(t, err, want)
				captured = ce.Captured()
			}
			assert.Equal(t, cc.state, captured.State())
			if cc.state != treg.CaptureComplete {
				assert.Nil(t, captured.Bytes())
				return
			}
			first := captured.Bytes()
			assertSameBytes(t, step.Body, first, "the exact bytes: whitespace, key order and number spellings")
			first[0] = 'X'
			assertSameBytes(t, step.Body, captured.Bytes(), "Bytes returns a copy")
		})
	}
}

// TestCaptureStates runs the capture cases through CallJSON; the typed reads
// run them in TestCatalogReadsCapture and TestCallsCapture.
func TestCaptureStates(t *testing.T) {
	t.Parallel()
	checkCaptureCases(t, captureBody, treg.OpCall,
		func(t *testing.T, step testutil.Step, opts []treg.Option, callOpts ...treg.CallOption) (treg.Capture, error) {
			t.Helper()
			script := testutil.NewScript(step)
			_, captured, err := outcome(treg.CallJSON[map[string]any](t.Context(), newClient(t, script, opts...),
				getRequest(), callOpts...))
			assert.Equal(t, 1, script.Calls(), "exactly one request")
			return captured, err
		})
}

func TestCaptureIsolation(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{Body: captureBody}), treg.WithResponseCapture())
	res, err := treg.CallJSON[map[string]any](t.Context(), c, getRequest())
	require.NoError(t, err)
	first := res.Captured.Bytes()
	require.NotEmpty(t, first)
	first[0] = 'X'
	assertSameBytes(t, captureBody, res.Captured.Bytes(), "Bytes returns a copy")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if b := res.Captured.Bytes(); assert.Len(t, b, len(captureBody)) {
				b[1] = 'Y'
			}
			assertSameBytes(t, captureBody, res.Captured.Bytes(), "each concurrent caller gets its own copy")
		})
	}
	wg.Wait()
}

func TestCapturePrintsLengthOnly(t *testing.T) {
	t.Parallel()
	c := newClient(t, testutil.NewScript(testutil.Step{Body: `{"pii":"jane.MARKER@example.com"}`}),
		treg.WithResponseCapture())
	res, err := treg.CallJSON[json.RawMessage](t.Context(), c, getRequest())
	require.NoError(t, err)
	testutil.AssertShapesNeverPrint(t, &res.Captured, "MARKER")
	out, err := json.Marshal(res.Captured)
	require.NoError(t, err)
	assert.JSONEq(t, `{"state":"complete","bytes":33}`, string(out))
	assert.Equal(t, "treg.Capture{state: disabled}", treg.Capture{}.String())
	assert.Equal(t, "unavailable", treg.CaptureUnavailable.String())
}
