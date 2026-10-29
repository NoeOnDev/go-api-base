package mail

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Templates renders emails from text templates, one folder per language: <locale>/<name>.txt
// (for example en/password_reset.txt and es-MX/password_reset.txt). Each template sets its
// subject with {{define "subject"}}…{{end}}; the rest of the file is the body.
//
// The folders must be exactly the languages of the i18n catalogs, each with the same files as
// the folder of i18n.BaseLocale, so no email is missing in any language.
type Templates struct {
	catalog *i18n.Catalog
	sets    map[string]map[string]*template.Template // locale → file name → template
}

// NewTemplates loads the templates of fsys, whose root has one folder per language.
func NewTemplates(fsys fs.FS, catalog *i18n.Catalog) (*Templates, error) {
	t := &Templates{catalog: catalog, sets: map[string]map[string]*template.Template{}}
	for _, locale := range catalog.Locales() {
		files, err := fs.Glob(fsys, locale+"/*.txt")
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("mail: no templates for %s (expected the folder %s/)", locale, locale)
		}
		t.sets[locale] = map[string]*template.Template{}
		for _, file := range files {
			tmpl, err := template.ParseFS(fsys, file)
			if err != nil {
				return nil, fmt.Errorf("mail: %w", err)
			}
			if tmpl.Lookup("subject") == nil {
				return nil, fmt.Errorf(`mail: %s has no {{define "subject"}}`, file)
			}
			t.sets[locale][path.Base(file)] = tmpl
		}
	}

	// Every language has the same templates as the base one, and there are no folders for
	// languages without a catalog.
	base := sortedNames(t.sets[i18n.BaseLocale])
	for locale, set := range t.sets {
		if names := sortedNames(set); !slices.Equal(names, base) {
			return nil, fmt.Errorf("mail: %s has the templates %v, but %s has %v", locale, names, i18n.BaseLocale, base)
		}
	}
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if _, ok := t.sets[d.Name()]; d.IsDir() && !ok {
			return nil, fmt.Errorf("mail: the folder %s/ is not a language of the i18n catalogs", d.Name())
		}
	}
	return t, nil
}

// Render builds the email name (for example "password_reset.txt") in the available language
// that best fits locale (see i18n.Catalog.Match; empty is the default language).
func (t *Templates) Render(locale, name string, data any) (subject, body string, err error) {
	tmpl, ok := t.sets[t.catalog.Match(locale)][name]
	if !ok {
		return "", "", fmt.Errorf("mail: the template %s does not exist", name)
	}
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "subject", data); err != nil {
		return "", "", fmt.Errorf("mail: subject of %s: %w", name, err)
	}
	subject = strings.TrimSpace(b.String())
	b.Reset()
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", "", fmt.Errorf("mail: body of %s: %w", name, err)
	}
	return subject, strings.TrimSpace(b.String()) + "\n", nil
}

func sortedNames(set map[string]*template.Template) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
