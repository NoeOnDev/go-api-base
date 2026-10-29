// Package docs tiene las guías de la plantilla. Este test comprueba que la documentación no
// apunte a lo que ya no existe: los enlaces entre documentos (incluidas las secciones, #ancla) y
// las menciones a docs/*.md en el código y en los documentos. Así el índice de AGENTS.md y las
// referencias cruzadas no quedan rotos cuando se renombra un archivo o el título de una sección.
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

// root es la raíz del repositorio, vista desde docs/.
const root = ".."

var (
	// mdLink es un enlace de Markdown: [texto](destino).
	mdLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	// docMention es una mención a una guía, como `docs/auth.md` en un comentario.
	docMention = regexp.MustCompile(`docs/[\w-]+\.md`)
	heading    = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
)

func TestLinks(t *testing.T) {
	anchors := map[string][]string{} // archivo .md → anclas de sus títulos
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
					t.Errorf("%s: el enlace %q apunta a un archivo que no existe", rel(file), target)
					continue
				}
				if anchor == "" || !strings.HasSuffix(resolved, ".md") {
					continue
				}
				if _, ok := anchors[resolved]; !ok {
					anchors[resolved] = headingAnchors(read(t, resolved))
				}
				if !slices.Contains(anchors[resolved], anchor) {
					t.Errorf("%s: el enlace %q apunta a una sección que no existe en %s", rel(file), target, rel(resolved))
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
				t.Errorf("%s menciona %s, que no existe", rel(path), m)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// markdownFiles son los documentos que se revisan: los de la raíz y los de docs/.
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

// headingAnchors devuelve las anclas de los títulos, como las arma GitHub.
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
			anchors = append(anchors, a+"-"+strconv.Itoa(n)) // los títulos repetidos llevan -1, -2…
		} else {
			anchors = append(anchors, a)
		}
		seen[a]++
	}
	return anchors
}

// anchor convierte un título en su ancla, como GitHub: minúsculas, sin signos de puntuación
// (salvo - y _) y con guiones en lugar de espacios. Conserva las tildes y la ñ.
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

// outsideCode devuelve las líneas que no están dentro de un bloque de código (```).
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
