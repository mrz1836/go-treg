package treg_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveGuard proves the live smoke tests stay out of every default run:
// the file carries the "live" build tag, mage-x's auto-discovered tag runs
// exclude it, and the linter still compiles it.
func TestLiveGuard(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("live_internal_test.go")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(src), "//go:build live\n"), "the live tests carry the live build tag")
	assert.Contains(t, string(src), `os.Getenv("TREG_LIVE") != "1"`, "and skip without an explicit opt-in")

	env, err := os.ReadFile(".github/env/90-project.env")
	require.NoError(t, err)
	var exclude string
	for line := range strings.SplitSeq(string(env), "\n") {
		if v, ok := strings.CutPrefix(line, "MAGE_X_AUTO_DISCOVER_BUILD_TAGS_EXCLUDE="); ok {
			exclude = v
		}
	}
	assert.Contains(t, strings.Split(exclude, ","), "live", "magex never auto-runs the live tag")

	var lint struct {
		Run struct {
			BuildTags []string `json:"build-tags"`
		} `json:"run"`
	}
	data, err := os.ReadFile(".golangci.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &lint))
	assert.Contains(t, lint.Run.BuildTags, "live", "the linter still checks the live file")
}
