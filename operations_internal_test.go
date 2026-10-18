package treg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// operationConstants returns the name and value of every Operation constant
// declared in errors.go.
func operationConstants(t *testing.T) map[string]Operation {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	require.NoError(t, err)
	out := map[string]Operation{}
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Operation" {
				continue
			}
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				require.True(t, ok)
				out[name.Name] = Operation(strings.Trim(lit.Value, `"`))
			}
		}
	}
	return out
}

// inventoryRow is one row of docs/operations.tsv.
type inventoryRow struct {
	row, method, path, auth, disposition, operation, note string
}

func readInventory(t *testing.T) []inventoryRow {
	t.Helper()
	data, err := os.ReadFile("docs/operations.tsv")
	require.NoError(t, err)
	var rows []inventoryRow
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "row\t") {
			continue
		}
		f := strings.Split(line, "\t")
		require.Len(t, f, 7, "line %d", i+1)
		rows = append(rows, inventoryRow{f[0], f[1], f[2], f[3], f[4], f[5], f[6]})
	}
	return rows
}

func TestOperationInventory(t *testing.T) {
	t.Parallel()
	consts := operationConstants(t)
	require.Len(t, consts, 24, "OpCall plus 21 typed reads plus 2 supplements")
	byValue := map[Operation]string{}
	for name, v := range consts {
		byValue[v] = name
		assert.True(t, validCode(string(v)), name)
	}
	require.Len(t, byValue, len(consts), "operation values are distinct")

	rows := readInventory(t)
	counts := map[string]int{}
	uses := map[string]int{}
	openAPI := 0
	valid := map[string]bool{
		"I-exec": true, "I-typed": true, "I-stream": true, "L-read": true, "L-write": true, "L-proto": true,
		"D-sec": true, "O-ui": true, "O-cli": true, "O-bill": true, "O-admin": true, "O": true,
	}
	for _, r := range rows {
		assert.True(t, valid[r.disposition], "row %s: disposition %q", r.row, r.disposition)
		counts[r.disposition]++
		if !strings.HasPrefix(r.row, "S") {
			openAPI++
		}
		initial := strings.HasPrefix(r.disposition, "I-")
		if !initial {
			assert.Empty(t, r.operation, "row %s: only initial rows name an operation", r.row)
			continue
		}
		_, ok := consts[r.operation]
		assert.True(t, ok, "row %s: %q is not an Operation constant", r.row, r.operation)
		uses[r.operation]++
	}
	assert.Equal(t, 190, openAPI, "every OpenAPI operation has a row")
	assert.Equal(t, 7, counts["I-exec"])
	assert.Equal(t, 22, counts["I-typed"], "20 OpenAPI reads plus 2 supplements")
	assert.Equal(t, 1, counts["I-stream"])
	assert.Equal(t, 24, counts["L-read"])
	assert.Equal(t, 37, counts["L-write"])
	assert.Equal(t, 3, counts["L-proto"])
	assert.Equal(t, 33, counts["D-sec"])
	assert.Equal(t, 32, counts["O-ui"])
	assert.Equal(t, 3, counts["O-cli"])
	assert.Equal(t, 3, counts["O-bill"])
	assert.Equal(t, 27, counts["O-admin"])
	assert.Equal(t, 2, counts["O"], "the 2 supplements outside the SDK")
	assert.Len(t, rows, 194, "190 OpenAPI operations plus 4 supplements, each counted above")
	for name := range consts {
		want := 1
		if name == "OpCall" {
			want = 7
		}
		assert.Equal(t, want, uses[name], "%s rows", name)
	}
}
