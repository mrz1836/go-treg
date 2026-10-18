package testutil

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// WaitForGoroutines waits up to five seconds for the number of goroutines to
// fall to at most limit, as goroutines that are ending finish their exit,
// and fails tb if it does not. Use runtime.NumGoroutine before the code
// under test to get limit.
func WaitForGoroutines(tb testing.TB, limit int) {
	tb.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for runtime.NumGoroutine() > limit {
		select {
		case <-deadline.C:
			assert.LessOrEqual(tb, runtime.NumGoroutine(), limit, "goroutines left running")
			return
		case <-tick.C:
		}
	}
}
