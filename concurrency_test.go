package treg_test

import (
	"context"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestConcurrentSharedClient(t *testing.T) {
	t.Parallel()
	for _, proto := range testutil.Protocols() {
		t.Run(proto.Name(), func(t *testing.T) {
			t.Parallel()
			srv := testutil.NewServerProto(t, proto, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				n := strconv.Itoa(atoi(r.URL.Query().Get("n")))
				w.Header().Set("X-Treg-Cost-Micro", n)
				_, _ = io.WriteString(w, `{"n":"`+n+`"}`)
			})
			c := newClient(t, srv, treg.WithResponseCapture())
			answers := make([]concurrentAnswer, 24)
			var wg sync.WaitGroup
			for i := range answers {
				wg.Go(func() { answers[i] = concurrentCall(t.Context(), c, i) })
			}
			wg.Wait()
			assert.Equal(t, 24, srv.Hits())
			for i, a := range answers {
				assertConcurrentAnswer(t, i, a)
			}
		})
	}
}

func TestConcurrentNoGoroutineLeaks(t *testing.T) { //nolint:paralleltest // it counts the process's goroutines, which parallel tests change
	before := runtime.NumGoroutine()
	for range 40 {
		c := newClient(t, testutil.NewScript(testutil.Step{Body: `{}`}))
		_, err := treg.CallJSON[any](t.Context(), c, getRequest())
		require.NoError(t, err)
		resp, err := newClient(t, testutil.NewScript(testutil.Step{Body: "x"})).Call(t.Context(), getRequest())
		require.NoError(t, err)
		require.NoError(t, resp.Close())
		ctx, cancel := context.WithCancel(t.Context())
		blocking := newClient(t, testutil.NewScript(testutil.Step{Respond: testutil.BlockUntilDone}))
		go cancel()
		_, err = blocking.CallBytes(ctx, getRequest())
		require.ErrorIs(t, err, context.Canceled)
	}
	testutil.WaitForGoroutines(t, before)
}

// concurrentAnswer is what one call of TestConcurrentSharedClient received.
// The calls run on goroutines of their own, where require must not stop the
// test, so the test checks their answers once every call has ended.
type concurrentAnswer struct {
	// n is the call's own number, which it sends and must get back.
	n string
	// json marks a CallJSON, whose value and cost were decoded too.
	json bool
	err  error
	// body is the body received: captured, buffered or streamed.
	body  []byte
	value string
	cost  *treg.MicroUSD
	// readErr and closeErr are those of a Call's stream.
	readErr, closeErr error
}

// concurrentCall makes call i of TestConcurrentSharedClient, by one of three
// methods, and returns what it received.
func concurrentCall(ctx context.Context, c *treg.Client, i int) concurrentAnswer {
	a := concurrentAnswer{n: strconv.Itoa(i + 1)}
	req := treg.CallRequest{Target: treg.CatalogEndpoint("x.y.z"), Method: http.MethodGet, Query: map[string][]string{"n": {a.n}}}
	switch i % 3 {
	case 0:
		a.json = true
		var res *treg.Result[map[string]string]
		if res, a.err = treg.CallJSON[map[string]string](ctx, c, req); a.err == nil {
			a.body, a.value, a.cost = res.Captured.Bytes(), res.Value["n"], res.Meta.CostMicro
		}
	case 1:
		var raw *treg.RawResponse
		if raw, a.err = c.CallBytes(ctx, req); a.err == nil {
			a.body = raw.Body()
		}
	default:
		var resp *treg.Response
		if resp, a.err = c.Call(ctx, req); a.err == nil {
			a.body, a.readErr = io.ReadAll(resp)
			a.closeErr = resp.Close()
		}
	}
	return a
}

// assertConcurrentAnswer checks that call i received its own answer.
func assertConcurrentAnswer(t *testing.T, i int, a concurrentAnswer) {
	t.Helper()
	require.NoError(t, a.err, "call %d", i)
	require.NoError(t, a.readErr, "call %d", i)
	require.NoError(t, a.closeErr, "call %d", i)
	assertSameBytes(t, `{"n":"`+a.n+`"}`, a.body, "call %d", i)
	if a.json {
		assert.Equal(t, a.n, a.value, "call %d", i)
		require.NotNil(t, a.cost, "call %d", i)
		assert.Equal(t, a.n, strconv.FormatInt(int64(*a.cost), 10), "call %d", i)
	}
}

// atoi parses a synthetic decimal query value, 0 when invalid.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
