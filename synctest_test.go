package treg_test

import (
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg"
	"github.com/mrz1836/go-treg/internal/testutil"
)

// The tests below run inside testing/synctest bubbles with in-memory
// transports only (real sockets would block a bubble), so the default
// deadlines elapse on the bubble's fake clock, deterministically.

func TestSynctestDefaultTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newClient(t, testutil.NewScript(testutil.Step{Respond: testutil.BlockUntilDone}))
		start := time.Now()
		_, err := c.CallBytes(t.Context(), getRequest())
		require.ErrorIs(t, err, treg.ErrTimeout)
		assert.Equal(t, treg.DefaultTimeout, time.Since(start), "a buffered call ends exactly at DefaultTimeout")
	})
}

func TestSynctestBodyReadCountsTowardTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBody(`{"a":1}`)
		body.Step = 1
		body.Gap = 30 * time.Second // seven bytes, seven gaps: past a 3-minute timeout
		c := newClient(t, testutil.TransportWith(http.StatusOK, nil, -1, body), treg.WithTimeout(3*time.Minute))
		start := time.Now()
		_, err := treg.CallJSON[any](t.Context(), c, getRequest())
		require.ErrorIs(t, err, treg.ErrTimeout)
		assert.Equal(t, 3*time.Minute, time.Since(start))
	})
}

func TestSynctestStreamDeadlines(t *testing.T) {
	t.Parallel()
	t.Run("header wait", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			c := newClient(t, testutil.NewScript(testutil.Step{Respond: testutil.BlockUntilDone}))
			start := time.Now()
			_, err := c.Call(t.Context(), getRequest())
			require.ErrorIs(t, err, treg.ErrTimeout)
			assert.Equal(t, treg.DefaultTimeout, time.Since(start), "the header wait is min(Timeout, StreamTimeout)")
		})
	})
	t.Run("stream life", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			body := testutil.NewBlockingBody("audio")
			c := newClient(t, testutil.TransportWith(http.StatusOK, audioHeader(), -1, body))
			start := time.Now()
			resp, err := c.Call(t.Context(), getRequest())
			require.NoError(t, err)
			got, err := io.ReadAll(resp)
			assert.Equal(t, "audio", string(got))
			require.ErrorIs(t, err, treg.ErrTimeout)
			assert.Equal(t, treg.DefaultStreamTimeout, time.Since(start), "a stream lives at most DefaultStreamTimeout")
			require.NoError(t, resp.Close())
			synctest.Wait()
			assert.Positive(t, body.Closes())
		})
	})
}

func TestSynctestInjectedTimeoutCap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		body := testutil.NewBlockingBody("x")
		hc := &http.Client{Transport: testutil.TransportWith(http.StatusOK, audioHeader(), -1, body), Timeout: 90 * time.Second}
		c, err := treg.NewClient(testToken, treg.WithBaseURL(testBaseURL), treg.WithHTTPClient(hc))
		require.NoError(t, err)
		start := time.Now()
		resp, err := c.Call(t.Context(), getRequest())
		require.NoError(t, err)
		_, err = io.ReadAll(resp)
		require.ErrorIs(t, err, treg.ErrTimeout)
		assert.Equal(t, 90*time.Second, time.Since(start), "a non-zero hc.Timeout caps the stream life")
		require.NoError(t, resp.Close())
	})
}
