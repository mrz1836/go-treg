package testutil_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/go-treg/internal/testutil"
)

func TestRepeatBodyRepeatsItsLine(t *testing.T) {
	t.Parallel()
	b := testutil.NewRepeatBody("ab\n")
	p := make([]byte, 7)
	n, err := b.Read(p)
	require.NoError(t, err)
	assert.Equal(t, len(p), n, "a read fills its buffer")
	assert.Equal(t, "ab\nab\na", string(p))
	n, err = b.Read(p[:4])
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "b\nab", string(p[:n]), "the next read goes on where the last one stopped")

	require.NoError(t, b.Close())
	require.NoError(t, b.Close())
	assert.Equal(t, 2, b.Closes(), "every close is counted")
	n, err = b.Read(p[:3])
	require.NoError(t, err, "a closed RepeatBody still reads: it has no end")
	assert.Equal(t, "\nab", string(p[:n]))
}

func TestRepeatBodyEmptyLine(t *testing.T) {
	t.Parallel()
	n, err := testutil.NewRepeatBody("").Read(make([]byte, 4))
	assert.Zero(t, n)
	require.ErrorIs(t, err, io.EOF, "an empty line makes an empty body")
}

func TestReadCloserBoundsAndCloses(t *testing.T) {
	t.Parallel()
	b := testutil.NewRepeatBody("xyz\n")
	var body io.ReadCloser = testutil.ReadCloser{Reader: io.LimitReader(b, 10), Closer: b}
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "xyz\nxyz\nxy", string(got), "the reader bounds the body")
	require.NoError(t, body.Close())
	assert.Equal(t, 1, b.Closes(), "the close reaches the closer")
}
