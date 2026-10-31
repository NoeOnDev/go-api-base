// Package i18n translates the text that end users see: API error messages, validation
// messages, emails and notifications. Everything else (code, comments, logs, internal errors)
// is written in English and is not translated.
//
// Each language is a JSON catalog in locales/ (en.json, es-MX.json…), embedded in the binary.
// The available languages are the files that exist, and every catalog must have exactly the
// keys of en.json. Adding a language is adding a file. See docs/i18n.md.
package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"
)

// BaseLocale is the reference catalog: every other catalog must have exactly its keys, and it
// is the last fallback for a missing text.
const BaseLocale = "en"

// maxPreferredLen caps the Accept-Language value that is parsed. Real browsers send a few dozen
// bytes; a longer value is ignored instead of spending time on it.
const maxPreferredLen = 512

//go:embed locales/*.json
var localeFiles embed.FS

// Message is a text to translate: a catalog key and its arguments. It is built where the text
// is decided (for example, in a validation rule) and translated when the response is written,
// in the language of that request.
type Message struct {
	Key string
	// Args are key/value pairs, like slog: "max", 100 fills {max} in the text.
	Args []any
}

// M builds a Message: M("validation.too_long", "max", 100).
func M(key string, args ...any) Message { return Message{Key: key, Args: args} }

// IsZero reports whether the message is empty, for example a validation rule that passed.
func (m Message) IsZero() bool { return m.Key == "" }

// entry is a catalog text: a plain string, or an object with the plural forms "one" and "other".
type entry struct {
	Text       string
	One, Other string
}

func (e *entry) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &e.Text); err == nil {
		return nil
	}
	var forms struct {
		One   string `json:"one"`
		Other string `json:"other"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&forms); err != nil || forms.One == "" || forms.Other == "" {
		return errors.New(`must be a text or an object with "one" and "other"`)
	}
	e.One, e.Other = forms.One, forms.Other
	return nil
}

func (e entry) plural() bool { return e.Other != "" }

// texts maps each locale to its catalog: key → text.
type texts map[string]map[string]entry

var embedded = sync.OnceValues(func() (texts, error) { return load(localeFiles) })

// load reads every catalog and checks that they all have the keys of BaseLocale, with the same
// shape (plain or plural).
func load(fsys fs.FS) (texts, error) {
	files, err := fs.Glob(fsys, "locales/*.json")
	if err != nil {
		return nil, err
	}
	all := texts{}
	for _, file := range files {
		locale := strings.TrimSuffix(path.Base(file), ".json")
		tag, err := language.Parse(locale)
		if err != nil || tag.String() != locale {
			return nil, fmt.Errorf("%s: the file name must be a language tag, like es-MX", file)
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		var catalog map[string]entry
		if err := json.Unmarshal(data, &catalog); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		all[locale] = catalog
	}

	base, ok := all[BaseLocale]
	if !ok {
		return nil, fmt.Errorf("missing the base catalog locales/%s.json", BaseLocale)
	}
	var problems []error
	for _, locale := range sortedKeys(all) {
		catalog := all[locale]
		for _, key := range sortedKeys(base) {
			e, ok := catalog[key]
			switch {
			case !ok:
				problems = append(problems, fmt.Errorf("%s.json: missing %q", locale, key))
			case e.plural() != base[key].plural():
				problems = append(problems, fmt.Errorf("%s.json: %q must be plural in every catalog or in none", locale, key))
			}
		}
		for _, key := range sortedKeys(catalog) {
			if _, ok := base[key]; !ok {
				problems = append(problems, fmt.Errorf("%s.json: %q is not in %s.json", locale, key, BaseLocale))
			}
		}
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return all, nil
}

// Catalog holds every language and chooses which one a request gets.
type Catalog struct {
	texts         texts
	defaultLocale string
	locales       []string // the default first, then the rest in order
	matcher       language.Matcher
}

// New loads the embedded catalogs. defaultLocale is the language for requests that don't ask
// for an available one (DEFAULT_LOCALE); it must be one of the catalogs.
func New(defaultLocale string) (*Catalog, error) {
	all, err := embedded()
	if err != nil {
		return nil, err
	}
	return newCatalog(all, defaultLocale)
}

func newCatalog(all texts, defaultLocale string) (*Catalog, error) {
	if _, ok := all[defaultLocale]; !ok {
		return nil, fmt.Errorf("unknown locale %q (available: %s)", defaultLocale, strings.Join(sortedKeys(all), ", "))
	}
	locales := []string{defaultLocale}
	for _, l := range sortedKeys(all) {
		if l != defaultLocale {
			locales = append(locales, l)
		}
	}
	// The first tag is the matcher's fallback, so the default goes first.
	tags := make([]language.Tag, len(locales))
	for i, l := range locales {
		tags[i] = language.MustParse(l)
	}
	return &Catalog{texts: all, defaultLocale: defaultLocale, locales: locales, matcher: language.NewMatcher(tags)}, nil
}

// Default is the catalog with BaseLocale as the default language. It is used where no catalog
// was configured, for example in tests that build a handler on their own.
var Default = sync.OnceValue(func() *Catalog {
	c, err := New(BaseLocale)
	if err != nil {
		panic(fmt.Sprintf("i18n: %v", err))
	}
	return c
})

// DefaultLocale is the language for requests that don't ask for an available one.
func (c *Catalog) DefaultLocale() string { return c.defaultLocale }

// Locales are the available languages, the default first.
func (c *Catalog) Locales() []string { return slices.Clone(c.locales) }

// Lookup returns the available locale written as s, accepting any letter case ("es-mx" is
// "es-MX"). Unlike Match, it does not fall back to a close language: it is for validating a
// locale that a user chooses.
func (c *Catalog) Lookup(s string) (string, bool) {
	tag, err := language.Parse(s)
	if err != nil {
		return "", false
	}
	if l := tag.String(); slices.Contains(c.locales, l) {
		return l, true
	}
	return "", false
}

// Match returns the available locale that best fits preferred: an Accept-Language value
// ("es-AR,es;q=0.9,en;q=0.8") or a stored locale ("es-MX"). A language without its own catalog
// gets the closest one ("es-AR" gets "es-MX"); anything else, or an empty value, gets the
// default locale.
func (c *Catalog) Match(preferred string) string {
	if locale, ok := c.Choose(preferred); ok {
		return locale
	}
	return c.defaultLocale
}

// Choose is like Match, but reports false instead of returning the default locale when
// preferred asks for no available language. It tells "asked for English" apart from "asked
// for nothing we have", for example to store a user's language only if they asked for one.
func (c *Catalog) Choose(preferred string) (string, bool) {
	if preferred == "" || len(preferred) > maxPreferredLen {
		return "", false
	}
	desired, _, err := language.ParseAcceptLanguage(preferred)
	if err != nil || len(desired) == 0 {
		return "", false
	}
	_, i, confidence := c.matcher.Match(desired...)
	if confidence == language.No {
		return "", false
	}
	return c.locales[i], true
}

// Translator returns the translator for the locale that best fits preferred (see Match).
func (c *Catalog) Translator(preferred string) Translator {
	return Translator{c: c, locale: c.Match(preferred)}
}

// Translator writes texts in one language.
type Translator struct {
	c      *Catalog
	locale string
}

// Locale is the translator's language, for example "es-MX".
func (t Translator) Locale() string { return t.locale }

// Has reports whether the catalogs have key.
func (t Translator) Has(key string) bool {
	_, ok := t.lookup(key)
	return ok
}

// T returns the text of key with its arguments filled in: T("validation.too_long", "max", 100).
// A missing key returns the key itself, so a mistake shows up instead of an empty text.
func (t Translator) T(key string, args ...any) string {
	e, ok := t.lookup(key)
	if !ok {
		return key
	}
	text := e.Text
	if e.plural() {
		text = e.Other
	}
	return fill(text, args)
}

// Plural returns the form of key for the number n ("one" for 1, "other" for the rest), with
// {count} and the other arguments filled in. Two forms cover English and Spanish; a language
// with more forms (like Polish) would need more.
func (t Translator) Plural(key string, n int, args ...any) string {
	e, ok := t.lookup(key)
	if !ok {
		return key
	}
	text := e.Text
	if e.plural() {
		text = e.Other
		if n == 1 {
			text = e.One
		}
	}
	return fill(text, append([]any{"count", n}, args...))
}

// Message translates m.
func (t Translator) Message(m Message) string { return t.T(m.Key, m.Args...) }

// FormatDateTime writes the instant t for a person, in t's zone and with the zone's name:
// "31/10/2026 00:56 (America/Mexico_City)" in es-MX. Pass it the instant in the reader's zone:
// tr.FormatDateTime(t.In(loc)). The format is the Go layout of format.datetime in the catalogs.
func (t Translator) FormatDateTime(at time.Time) string {
	return at.Format(t.T("format.datetime")) + " (" + at.Location().String() + ")"
}

// FormatDate writes the day of t for a person, without the hour or the zone: "31/10/2026" in es-MX.
// The format is the Go layout of format.date in the catalogs. For a civil.Date d, pass it
// d.Start(time.UTC).
func (t Translator) FormatDate(at time.Time) string {
	return at.Format(t.T("format.date"))
}

// lookup finds key in the translator's language, then in the default language, then in BaseLocale.
func (t Translator) lookup(key string) (entry, bool) {
	for _, locale := range []string{t.locale, t.c.defaultLocale, BaseLocale} {
		if e, ok := t.c.texts[locale][key]; ok {
			return e, true
		}
	}
	return entry{}, false
}

// fill replaces each {name} in text with its value from args (key/value pairs).
func fill(text string, args []any) string {
	if len(args) < 2 || !strings.Contains(text, "{") {
		return text
	}
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, "{"+fmt.Sprint(args[i])+"}", fmt.Sprint(args[i+1]))
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
