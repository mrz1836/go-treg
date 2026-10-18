package testutil_test

import (
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/go-treg/internal/testutil"
)

// TestWaitForGoroutinesWaitsForAnExit ends a goroutine and waits for the
// count to fall back.
//
//nolint:paralleltest // it counts the process's goroutines
func TestWaitForGoroutinesWaitsForAnExit(t *testing.T) {
	before := runtime.NumGoroutine()
	stop := make(chan struct{})
	go func() { <-stop }()
	close(stop)
	testutil.WaitForGoroutines(t, before)
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}

// TestWaitForGoroutinesTimesOut asks for fewer goroutines than a process ever
// runs: after five seconds of the bubble's fake clock, the wait fails the test
// with an assert, which lets the test go on. The outcome does not depend on
// other tests' goroutines, so it runs in parallel.
func TestWaitForGoroutinesTimesOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		rec, returned := record(func(r *recorder) { testutil.WaitForGoroutines(r, 0) })
		assert.True(t, rec.failed, "goroutines left running")
		assert.True(t, returned)
		assert.Equal(t, 5*time.Second, time.Since(start))
	})
}
