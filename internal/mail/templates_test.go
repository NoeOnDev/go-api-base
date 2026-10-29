package mail

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

func tmplFile(subject, body string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{{define "subject"}}` + subject + "{{end -}}\n" + body)}
}

func TestTemplatesRender(t *testing.T) {
	files := fstest.MapFS{
		"en/hello.txt":    tmplFile("Hello {{.Name}}", "Hi {{.Name}},\n\nWelcome.\n"),
		"es-MX/hello.txt": tmplFile("Hola {{.Name}}", "Hola {{.Name}}:\n\nBienvenida.\n"),
	}
	tmpl, err := NewTemplates(files, i18n.Default())
	if err != nil {
		t.Fatal(err)
	}

	for locale, want := range map[string][2]string{
		"":      {"Hello Ana", "Hi Ana,\n\nWelcome.\n"},
		"es-MX": {"Hola Ana", "Hola Ana:\n\nBienvenida.\n"},
		"es-AR": {"Hola Ana", "Hola Ana:\n\nBienvenida.\n"}, // no folder of its own: the closest
		"fr":    {"Hello Ana", "Hi Ana,\n\nWelcome.\n"},
	} {
		subject, body, err := tmpl.Render(locale, "hello.txt", map[string]string{"Name": "Ana"})
		if err != nil || subject != want[0] || body != want[1] {
			t.Errorf("Render(%q) = %q, %q, %v", locale, subject, body, err)
		}
	}
	if _, _, err := tmpl.Render("en", "missing.txt", nil); err == nil {
		t.Error("a missing template should fail")
	}
}

func TestTemplatesMustMatchTheCatalogs(t *testing.T) {
	ok := tmplFile("S", "B")
	for name, tc := range map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"missing language": {fstest.MapFS{"en/a.txt": ok}, "no templates for es-MX"},
		"missing file": {fstest.MapFS{
			"en/a.txt": ok, "en/b.txt": ok,
			"es-MX/a.txt": ok,
		}, "es-MX has the templates [a.txt]"},
		"language without catalog": {fstest.MapFS{
			"en/a.txt": ok, "es-MX/a.txt": ok, "fr/a.txt": ok,
		}, "fr/ is not a language"},
		"no subject": {fstest.MapFS{
			"en/a.txt":    {Data: []byte("just a body")},
			"es-MX/a.txt": ok,
		}, `has no {{define "subject"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewTemplates(tc.files, i18n.Default())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
