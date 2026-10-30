package mail

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

var testBrand = Brand{AppName: "Test App", URL: "https://app.test"}

func tmplFile(subject, body string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{{define "subject"}}` + subject + "{{end -}}\n" + body)}
}

func htmlFile(content string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{{define "content"}}` + content + "{{end}}")}
}

func TestTemplatesRender(t *testing.T) {
	files := fstest.MapFS{
		"en/hello.txt":     tmplFile("Hello {{.Name}}", "Hi {{.Name}},\n\nWelcome.\n"),
		"en/hello.html":    htmlFile("<p>Hi {{.Name}},</p>"),
		"es-MX/hello.txt":  tmplFile("Hola {{.Name}}", "Hola {{.Name}}:\n\nBienvenida.\n"),
		"es-MX/hello.html": htmlFile("<p>Hola {{.Name}}:</p>"),
	}
	tmpl, err := NewTemplates(files, i18n.Default(), testBrand)
	if err != nil {
		t.Fatal(err)
	}

	for locale, want := range map[string][3]string{
		"":      {"Hello Alice", "Hi Alice,\n\nWelcome.\n", "Hi Alice,</p>"},
		"es-MX": {"Hola Alice", "Hola Alice:\n\nBienvenida.\n", "Hola Alice:</p>"},
		"es-AR": {"Hola Alice", "Hola Alice:\n\nBienvenida.\n", "Hola Alice:</p>"}, // no folder of its own: the closest
		"fr":    {"Hello Alice", "Hi Alice,\n\nWelcome.\n", "Hi Alice,</p>"},
	} {
		r, err := tmpl.Render(locale, "hello", map[string]string{"Name": "Alice"})
		if err != nil || r.Subject != want[0] || r.Text != want[1] || !strings.Contains(r.HTML, want[2]) {
			t.Errorf("Render(%q) = %+v, %v", locale, r, err)
		}
	}
	if _, err := tmpl.Render("en", "missing", nil); err == nil {
		t.Error("a missing template should fail")
	}
}

func TestTemplatesHTML(t *testing.T) {
	files := fstest.MapFS{
		"en/reset.txt": tmplFile("Reset", "{{.Link}}"),
		"en/reset.html": {Data: []byte(`{{define "preheader"}}Choose a new password{{end}}` +
			`{{define "content"}}<h1>Hi {{.Name}},</h1><p>Use this button:</p>` +
			`{{template "button" (button .Link "Reset")}}{{template "link_help" .Link}}{{end}}`)},
		"es-MX/reset.txt":  tmplFile("Recupera", "{{.Link}}"),
		"es-MX/reset.html": htmlFile("<p>{{.Name}}</p>"),
	}
	tmpl, err := NewTemplates(files, i18n.Default(), testBrand)
	if err != nil {
		t.Fatal(err)
	}

	link := "https://app.test/reset-password?token=abc&x=1"
	r, err := tmpl.Render("en", "reset", map[string]string{"Name": "<b>Alice</b>", "Link": link})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<html xmlns="http://www.w3.org/1999/xhtml" lang="en"`,
		"<title>Reset</title>",
		`>Test App</a>`,                                            // the layout's brand
		"Choose a new password</span>",                             // the preheader
		"Hi &lt;b&gt;Alice&lt;/b&gt;,</h1>",                        // the data is escaped
		`<h1 style="margin-top: 0;`,                                // the inline styles of the content
		`<p style="font-size: 16px;`,                               // the inline styles of the content
		`href="https://app.test/reset-password?token=abc&amp;x=1"`, // the button and the link
		"<!--[if mso]>",                                            // Outlook's conditional comment survives
		"If the button doesn&#39;t work",                           // the layout's text, from the catalog
	} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("the HTML does not contain %q", want)
		}
	}
	if strings.Contains(r.HTML, "<b>Alice") {
		t.Error("the user's data was not escaped")
	}
}

func TestTemplatesMustMatchTheCatalogs(t *testing.T) {
	txt, html := tmplFile("S", "B"), htmlFile("<p>B</p>")
	for name, tc := range map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"missing language": {fstest.MapFS{"en/a.txt": txt, "en/a.html": html}, "no templates for es-MX"},
		"missing file": {fstest.MapFS{
			"en/a.txt": txt, "en/a.html": html, "en/b.txt": txt, "en/b.html": html,
			"es-MX/a.txt": txt, "es-MX/a.html": html,
		}, "es-MX has the emails [a]"},
		"language without catalog": {fstest.MapFS{
			"en/a.txt": txt, "en/a.html": html, "es-MX/a.txt": txt, "es-MX/a.html": html, "fr/a.txt": txt,
		}, "fr/ is not a language"},
		"no subject": {fstest.MapFS{
			"en/a.txt": {Data: []byte("just a body")}, "en/a.html": html,
			"es-MX/a.txt": txt, "es-MX/a.html": html,
		}, `has no {{define "subject"}}`},
		"no HTML": {fstest.MapFS{
			"en/a.txt": txt, "es-MX/a.txt": txt, "es-MX/a.html": html,
		}, "has no HTML version"},
		"no text": {fstest.MapFS{
			"en/a.txt": txt, "en/a.html": html, "en/b.html": html,
			"es-MX/a.txt": txt, "es-MX/a.html": html,
		}, "has no plain-text version"},
		"no content": {fstest.MapFS{
			"en/a.txt": txt, "en/a.html": {Data: []byte("<p>outside a block</p>")},
			"es-MX/a.txt": txt, "es-MX/a.html": html,
		}, `has no {{define "content"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewTemplates(tc.files, i18n.Default(), testBrand)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
