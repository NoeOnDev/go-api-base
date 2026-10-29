// Package docs holds the guides of the template. This test checks that the documentation does
// not point to what no longer exists: the links between documents (including sections, #anchor)
// and the mentions of docs/*.md in the code and the documents. That way the index of AGENTS.md
// and the cross-references do not break when a file or a section title is renamed.
package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// root is the root of the repository, seen from docs/.
const root = ".."

var (
	// mdLink is a Markdown link: [text](target).
	mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	// docMention is a mention of a guide, like `docs/auth.md` in a comment.
	docMention = regexp.MustCompile(`docs/[\w-]+\.md`)
	heading    = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
)

func TestLinks(t *testing.T) {
	anchors := map[string][]string{} // .md file → anchors of its headings
	for _, file := range markdownFiles(t) {
		for _, line := range outsideCode(read(t, file)) {
			for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				path, anchor, _ := strings.Cut(target, "#")
				resolved := file
				if path != "" {
					resolved = filepath.Join(filepath.Dir(file), path)
				}
				if _, err := os.Stat(resolved); err != nil {
					t.Errorf("%s: the link %q points to a file that does not exist", rel(file), target)
					continue
				}
				if anchor == "" || !strings.HasSuffix(resolved, ".md") {
					continue
				}
				if _, ok := anchors[resolved]; !ok {
					anchors[resolved] = headingAnchors(read(t, resolved))
				}
				if !slices.Contains(anchors[resolved], anchor) {
					t.Errorf("%s: the link %q points to a section that does not exist in %s", rel(file), target, rel(resolved))
				}
			}
		}
	}
}

func TestDocMentions(t *testing.T) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "bin") {
			return filepath.SkipDir
		}
		if d.IsDir() || !(strings.HasSuffix(path, ".go") || strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".sql")) {
			return nil
		}
		for _, m := range docMention.FindAllString(read(t, path), -1) {
			if _, err := os.Stat(filepath.Join(root, m)); err != nil {
				t.Errorf("%s mentions %s, which does not exist", rel(path), m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// markdownFiles are the documents that are checked: the ones at the root and in docs/.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{"*.md", "docs/*.md"} {
		m, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	return files
}

// headingAnchors returns the anchors of the headings, as GitHub builds them.
func headingAnchors(text string) []string {
	var anchors []string
	seen := map[string]int{}
	for _, line := range outsideCode(text) {
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		a := anchor(m[1])
		if n := seen[a]; n > 0 {
			anchors = append(anchors, a+"-"+strconv.Itoa(n)) // repeated headings get -1, -2…
		} else {
			anchors = append(anchors, a)
		}
		seen[a]++
	}
	return anchors
}

// anchor turns a heading into its anchor, as GitHub does: lowercase, without punctuation
// (except - and _) and with hyphens instead of spaces. It keeps accents and ñ.
func anchor(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// outsideCode returns the lines that are not inside a code block (```).
func outsideCode(text string) []string {
	var lines []string
	inCode := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if !inCode {
			lines = append(lines, line)
		}
	}
	return lines
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rel(path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return r
}
