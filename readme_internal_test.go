package treg

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readmeSources are the compiled files whose code the README's Go blocks must
// quote: the package's examples and the example programs.
func readmeSources() []string {
	return []string{
		"example_test.go", "examples/offline/main.go", "examples/quickstart/main.go",
		"examples/extension/urlmetrics/urlmetrics.go", "examples/extension/fishtts/fishtts.go",
	}
}

// readmeQuoted maps each Markdown file whose Go blocks must be quoted
// verbatim from compiled code to the files they quote. The integration
// guide's blocks are fragments, so guide_fragments_test.go holds them inside
// code that compiles.
func readmeQuoted() map[string][]string {
	return map[string][]string{
		"README.md": readmeSources(),
		"docs/integration-guide.md": {
			"guide_fragments_test.go", "examples/extension/urlmetrics/urlmetrics.go",
			"examples/extension/fishtts/fishtts.go",
		},
	}
}

// readmeDocs are the Markdown files whose links and placeholders are checked.
func readmeDocs() []string {
	return []string{"README.md", "docs/coverage.md", "docs/integration-guide.md", "testdata/synthetic/README.md"}
}

func readmeFile(tb testing.TB, name string) string {
	tb.Helper()
	data, err := os.ReadFile(filepath.FromSlash(name))
	require.NoError(tb, err)
	return string(data)
}

// readmeNormalize collapses every run of whitespace to one space.
func readmeNormalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// readmeFences returns the content of every fenced code block in md whose
// info string is lang.
func readmeFences(md, lang string) []string {
	re := regexp.MustCompile("(?s)```" + regexp.QuoteMeta(lang) + "\n(.*?)\n```")
	matches := re.FindAllStringSubmatch(md, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

func TestReadmeGoBlocksAreCompiled(t *testing.T) {
	t.Parallel()
	for doc, quoted := range readmeQuoted() {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()
			var sources strings.Builder
			for _, name := range quoted {
				sources.WriteString(readmeNormalize(readmeFile(t, name)))
				sources.WriteString("\n")
			}
			compiled := sources.String()
			blocks := readmeFences(readmeFile(t, doc), "go")
			require.NotEmpty(t, blocks)
			for i, block := range blocks {
				assert.Contains(t, compiled, readmeNormalize(block), "%s Go block %d is not quoted verbatim from %v:\n%s",
					doc, i+1, quoted, block)
			}
		})
	}
}

func TestReadmeHasNoPlaceholders(t *testing.T) {
	t.Parallel()
	for _, doc := range readmeDocs() {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()
			text := readmeFile(t, doc)
			for _, marker := range []string{"TODO", "TBD", "FIXME", "XXX", "lorem", "go-template"} {
				assert.NotContains(t, text, marker, doc)
			}
		})
	}
}

func TestReadmeRelativeLinksResolve(t *testing.T) {
	t.Parallel()
	link := regexp.MustCompile(`\]\(([^)#\s]+)(#[^)]*)?\)|href="([^"#]+)"`)
	for _, doc := range readmeDocs() {
		t.Run(doc, func(t *testing.T) {
			t.Parallel()
			prose := regexp.MustCompile("(?s)```.*?```").ReplaceAllString(readmeFile(t, doc), "")
			for _, m := range link.FindAllStringSubmatch(prose, -1) {
				target := m[1]
				if target == "" {
					target = m[3]
				}
				if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				path := filepath.Join(filepath.Dir(doc), filepath.FromSlash(target))
				_, err := os.Stat(path)
				assert.NoError(t, err, "%s links to %s", doc, target)
			}
		})
	}
}

func TestReadmeLimitsMatchCode(t *testing.T) {
	t.Parallel()
	readme := readmeFile(t, "README.md")
	mib := func(n int64) string { return strconv.FormatInt(n>>20, 10) + " MiB" }
	for _, want := range []string{
		strconv.Itoa(int(DefaultTimeout.Seconds())) + " s",
		strconv.Itoa(int(DefaultStreamTimeout.Minutes())) + " min",
		mib(DefaultMaxRequestBytes), mib(DefaultMaxResponseBytes),
		strconv.FormatInt(DefaultMaxStreamBytes>>30, 10) + " GiB",
		mib(typedResponseCap), strconv.Itoa(maxRefusalBytes>>10) + " KiB",
		strconv.Itoa(maxHeaderCount) + " values", strconv.Itoa(maxHeaderValueBytes>>10) + " KiB each",
		strconv.Itoa(maxHeaderTotalBytes>>10) + " KiB total", mib(maxFindLineBytes) + " per line",
		mib(maxFindTotalBytes) + " total", strconv.Itoa(int(maxTimeout.Minutes())) + " min",
		strconv.Itoa(int(maxStreamTimeout.Hours())) + " h", "4 GiB", mib(maxResponseBytesLimit), "64 GiB",
	} {
		assert.Contains(t, readme, want)
	}
	assert.Equal(t, int64(4<<30), int64(maxRequestBytesLimit))
	assert.Equal(t, int64(64<<30), int64(maxStreamBytesLimit))
	assert.Equal(t, 64<<10, maxResponseHeaderBytes)
}
