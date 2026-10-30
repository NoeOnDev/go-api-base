package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Templates renders emails from templates, one folder per language. Each email has two files,
// <locale>/<name>.txt and <locale>/<name>.html (for example en/password_reset.txt and
// en/password_reset.html):
//
//   - The .txt is the plain-text version, and sets the subject with {{define "subject"}}…{{end}};
//     the rest of the file is the body.
//   - The .html defines "content" and, optionally, "preheader" (the line the inbox shows next to
//     the subject). It goes inside the shared layout (layout/layout.html) and can use its blocks
//     (layout/blocks.html). Its <h1> and <p> get their styles inline (see contentStyles).
//
// The folders must be exactly the languages of the i18n catalogs, each with the same emails as
// the folder of i18n.BaseLocale, so no email is missing in any language.
type Templates struct {
	catalog *i18n.Catalog
	brand   Brand
	sets    map[string]map[string]email       // locale → email name → its templates
	layouts map[string]*htmltemplate.Template // locale → layout
}

// email are the two versions of an email in one language.
type email struct {
	text *template.Template
	html *htmltemplate.Template
}

// Brand is what the layout shows of the application: its name, linked to its URL.
type Brand struct {
	AppName string
	URL     string
}

// Rendered is a built email: its subject and its body, in plain text and in HTML.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

//go:embed layout/layout.html layout/blocks.html
var layoutFiles embed.FS

// contentStyles gives the <h1> and <p> of the emails the inline styles of the layout. Some email
// clients ignore the <style> of the head, so every element carries its styles; this way the
// emails are written with plain tags. It runs on the HTML already built, where the data was
// escaped: a "<p>" typed by a user arrives as "&lt;p&gt;" and is not touched.
var contentStyles = strings.NewReplacer(
	"<h1>", `<h1 style="margin-top: 0; color: #333333; font-size: 22px; font-weight: bold; text-align: left;" align="left">`,
	"<p>", `<p style="font-size: 16px; line-height: 1.625; color: #51545E; margin: .4em 0 1.1875em;">`,
)

// NewTemplates loads the templates of fsys, whose root has one folder per language. brand is
// what the layout shows on top and in the footer.
func NewTemplates(fsys fs.FS, catalog *i18n.Catalog, brand Brand) (*Templates, error) {
	t := &Templates{
		catalog: catalog,
		brand:   brand,
		sets:    map[string]map[string]email{},
		layouts: map[string]*htmltemplate.Template{},
	}
	for _, locale := range catalog.Locales() {
		funcs := htmltemplate.FuncMap{
			"t":             catalog.Translator(locale).T,
			"button":        newButton,
			"outlookStyles": outlookStyles,
		}
		layout, err := htmltemplate.New("layout.html").Funcs(funcs).ParseFS(layoutFiles, "layout/layout.html")
		if err != nil {
			return nil, fmt.Errorf("mail: %w", err)
		}
		blocks, err := htmltemplate.New("blocks.html").Funcs(funcs).ParseFS(layoutFiles, "layout/blocks.html")
		if err != nil {
			return nil, fmt.Errorf("mail: %w", err)
		}
		t.layouts[locale] = layout

		set, err := loadLocale(fsys, locale, blocks)
		if err != nil {
			return nil, err
		}
		t.sets[locale] = set
	}

	// Every language has the same emails as the base one, and there are no folders for
	// languages without a catalog.
	base := sortedNames(t.sets[i18n.BaseLocale])
	for locale, set := range t.sets {
		if names := sortedNames(set); !slices.Equal(names, base) {
			return nil, fmt.Errorf("mail: %s has the emails %v, but %s has %v", locale, names, i18n.BaseLocale, base)
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

// loadLocale loads the emails of a language folder: each .txt with its .html.
func loadLocale(fsys fs.FS, locale string, blocks *htmltemplate.Template) (map[string]email, error) {
	texts, err := fs.Glob(fsys, locale+"/*.txt")
	if err != nil {
		return nil, err
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("mail: no templates for %s (expected the folder %s/)", locale, locale)
	}
	htmls, err := fs.Glob(fsys, locale+"/*.html")
	if err != nil {
		return nil, err
	}

	set := map[string]email{}
	for _, file := range texts {
		text, err := template.ParseFS(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("mail: %w", err)
		}
		if text.Lookup("subject") == nil {
			return nil, fmt.Errorf(`mail: %s has no {{define "subject"}}`, file)
		}

		name := strings.TrimSuffix(path.Base(file), ".txt")
		htmlFile := locale + "/" + name + ".html"
		if !slices.Contains(htmls, htmlFile) {
			return nil, fmt.Errorf("mail: %s has no HTML version (%s)", file, htmlFile)
		}
		html, err := htmltemplate.Must(blocks.Clone()).ParseFS(fsys, htmlFile)
		if err != nil {
			return nil, fmt.Errorf("mail: %w", err)
		}
		if html.Lookup("content") == nil {
			return nil, fmt.Errorf(`mail: %s has no {{define "content"}}`, htmlFile)
		}
		set[name] = email{text: text, html: html}
	}
	for _, file := range htmls {
		if _, ok := set[strings.TrimSuffix(path.Base(file), ".html")]; !ok {
			return nil, fmt.Errorf("mail: %s has no plain-text version (.txt)", file)
		}
	}
	return set, nil
}

// Render builds the email name (for example "password_reset") in the available language that
// best fits locale (see i18n.Catalog.Match; empty is the default language).
func (t *Templates) Render(locale, name string, data any) (Rendered, error) {
	locale = t.catalog.Match(locale)
	e, ok := t.sets[locale][name]
	if !ok {
		return Rendered{}, fmt.Errorf("mail: the email %s does not exist", name)
	}

	var b bytes.Buffer
	if err := e.text.ExecuteTemplate(&b, "subject", data); err != nil {
		return Rendered{}, fmt.Errorf("mail: subject of %s: %w", name, err)
	}
	r := Rendered{Subject: strings.TrimSpace(b.String())}
	b.Reset()
	if err := e.text.ExecuteTemplate(&b, name+".txt", data); err != nil {
		return Rendered{}, fmt.Errorf("mail: body of %s: %w", name, err)
	}
	r.Text = strings.TrimSpace(b.String()) + "\n"

	var preheader, content bytes.Buffer
	if e.html.Lookup("preheader") != nil {
		if err := e.html.ExecuteTemplate(&preheader, "preheader", data); err != nil {
			return Rendered{}, fmt.Errorf("mail: preheader of %s: %w", name, err)
		}
	}
	if err := e.html.ExecuteTemplate(&content, "content", data); err != nil {
		return Rendered{}, fmt.Errorf("mail: HTML of %s: %w", name, err)
	}
	b.Reset()
	err := t.layouts[locale].Execute(&b, layoutData{
		Lang:    locale,
		Subject: r.Subject,
		AppName: t.brand.AppName,
		URL:     t.brand.URL,
		// Both were built by html/template, which already escaped the data.
		Preheader: htmltemplate.HTML(strings.TrimSpace(preheader.String())),
		Content:   htmltemplate.HTML(contentStyles.Replace(strings.TrimSpace(content.String()))),
	})
	if err != nil {
		return Rendered{}, fmt.Errorf("mail: layout of %s: %w", name, err)
	}
	r.HTML = b.String()
	return r, nil
}

// layoutData is what layout/layout.html receives.
type layoutData struct {
	Lang      string
	Subject   string
	AppName   string
	URL       string
	Preheader htmltemplate.HTML
	Content   htmltemplate.HTML
}

// buttonData is what the "button" block receives (see layout/blocks.html).
type buttonData struct {
	URL   string
	Label string
}

// newButton is the "button" function of the templates: {{template "button" (button .Link "Label")}}.
func newButton(url, label string) buttonData {
	return buttonData{URL: url, Label: label}
}

// outlookStyles is the font fallback for Outlook on Windows, which does not follow the font list
// and would show Times New Roman. It goes in a conditional comment, which html/template removes
// from the templates, so a function returns it. It is constant: it never carries data.
func outlookStyles() htmltemplate.HTML {
	return `<!--[if mso]><style type="text/css">.f-fallback { font-family: Arial, sans-serif; }</style><![endif]-->`
}

func sortedNames(set map[string]email) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
