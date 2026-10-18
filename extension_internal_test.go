package treg

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/mrz1836/go-treg"

// fileImports returns the import paths of every .go file in dir, optionally
// including tests.
func fileImports(t *testing.T, dir string, tests bool) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || (!tests && strings.HasSuffix(name, "_test.go")) {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			out[name] = append(out[name], p)
		}
	}
	return out
}

// isStdlib reports whether an import path belongs to the standard library.
func isStdlib(p string) bool { return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") }

func TestRootNeverImportsExamples(t *testing.T) {
	t.Parallel()
	for file, imports := range fileImports(t, ".", true) {
		for _, p := range imports {
			assert.False(t, strings.HasPrefix(p, modulePath+"/examples"), "%s imports %s", file, p)
		}
	}
	for file, imports := range fileImports(t, ".", false) {
		for _, p := range imports {
			assert.True(t, isStdlib(p), "library file %s imports %s: the runtime is stdlib-only", file, p)
		}
	}
}

func TestExtensionPackagesAreExternal(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"examples/extension/urlmetrics", "examples/extension/fishtts"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			src := fileImports(t, dir, false)
			require.NotEmpty(t, src, "the package has source files")
			usesTreg := false
			for file, imports := range src {
				for _, p := range imports {
					usesTreg = usesTreg || p == modulePath
					assert.True(t, p == modulePath || isStdlib(p),
						"%s/%s imports %s: only the standard library and treg are allowed", dir, file, p)
				}
			}
			assert.True(t, usesTreg, "the package builds on treg")
			for file, imports := range fileImports(t, dir, true) {
				for _, p := range imports {
					assert.NotEqual(t, modulePath+"/internal/testutil", p, "%s tests like an outside package", file)
				}
			}
		})
	}
}
