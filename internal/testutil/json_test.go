package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestEsc(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `"\u0061\\"`, testutil.Esc(`"~u0061~~"`))
	assert.Equal(t, "plain", testutil.Esc("plain"))
}
