package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedCatalogsLoad(t *testing.T) {
	c, err := New(BaseLocale)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Locales(); len(got) < 2 || got[0] != BaseLocale {
		t.Errorf("locales = %v, want the default first and at least es-MX", got)
	}
	if _, err := New("fr"); err == nil {
		t.Error("an unknown default locale should fail")
	}
}

func TestMatch(t *testing.T) {
	c, err := New("en")
	if err != nil {
		t.Fatal(err)
	}
	for preferred, want := range map[string]string{
		"":                           "en",
		"es-MX":                      "es-MX",
		"es-mx":                      "es-MX",
		"es":                         "es-MX",
		"es-AR,es;q=0.9,en;q=0.8":    "es-MX", // no es-AR catalog: the closest Spanish
		"es-ES":                      "es-MX",
		"fr-FR,fr;q=0.9":             "en", // no French at all: the default
		"fr-FR,fr;q=0.9,es;q=0.5":    "es-MX",
		"en-US,en;q=0.9,es-MX;q=0.8": "en",
		"not a language":             "en",
		strings.Repeat("es,", 300):   "en", // too long: ignored
	} {
		if got := c.Match(preferred); got != want {
			t.Errorf("Match(%.40q) = %q, want %q", preferred, got, want)
		}
	}

	if _, ok := c.Choose("fr-FR,fr;q=0.9"); ok {
		t.Error("Choose should report false when no available language fits")
	}
	if l, ok := c.Choose("es-AR"); !ok || l != "es-MX" {
		t.Errorf("Choose(es-AR) = %q, %v", l, ok)
	}

	// With another default, a request without a known language gets that one.
	mx, err := New("es-MX")
	if err != nil {
		t.Fatal(err)
	}
	if got := mx.Match("fr"); got != "es-MX" {
		t.Errorf("with DEFAULT_LOCALE=es-MX, Match(fr) = %q", got)
	}
	if got := mx.Match("en-GB"); got != "en" {
		t.Errorf("with DEFAULT_LOCALE=es-MX, Match(en-GB) = %q", got)
	}
}

func TestLookup(t *testing.T) {
	c := Default()
	if l, ok := c.Lookup("es-mx"); !ok || l != "es-MX" {
		t.Errorf("Lookup(es-mx) = %q, %v", l, ok)
	}
	for _, s := range []string{"es-AR", "fr", "", "español"} {
		if _, ok := c.Lookup(s); ok {
			t.Errorf("Lookup(%q) should fail: only exact catalogs count", s)
		}
	}
}

func TestTranslate(t *testing.T) {
	es := Default().Translator("es-MX")
	if got := es.T("validation.too_long", "max", 100); got != "Debe tener como máximo 100 caracteres." {
		t.Errorf("T = %q", got)
	}
	if got := es.Message(M("validation.required")); got != "Este campo es obligatorio." {
		t.Errorf("Message = %q", got)
	}
	for n, want := range map[int]string{1: "1 hora", 24: "24 horas"} {
		if got := es.Plural("duration.hours", n); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
	if got := es.T("does.not.exist"); got != "does.not.exist" {
		t.Errorf("a missing key should return the key, got %q", got)
	}
	if !es.Has("errors.email_taken") || es.Has("notifications.task.assigned") {
		t.Error("Has does not match the catalog")
	}
	if !M("").IsZero() || M("validation.required").IsZero() {
		t.Error("IsZero")
	}
}

func TestTranslateDatesAndTimes(t *testing.T) {
	args := []any{"branch", "Clinic", "date", "2026-11-05", "time", "14:30"}
	for locale, want := range map[string]string{
		"en":    "Reminder: you have an appointment at Clinic on Nov 5, 2026 at 2:30 PM.",
		"es-MX": "Recordatorio: tienes cita en Clinic el 05/11/2026 a las 14:30.",
	} {
		if got := Default().Translator(locale).T("notifications.appointment_reminder", args...); got != want {
			t.Errorf("%s: T = %q, want %q", locale, got, want)
		}
	}

	// A value in another format goes as it is.
	got := Default().Translator("es-MX").T("notifications.appointment_reminder", "branch", "Clinic", "date", "soon", "time", "2pm")
	if want := "Recordatorio: tienes cita en Clinic el soon a las 2pm."; got != want {
		t.Errorf("T = %q, want %q", got, want)
	}
}

func TestMiddleware(t *testing.T) {
	var got string
	h := Default().Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = FromContext(r.Context()).Locale()
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "es-AR,es;q=0.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got != "es-MX" {
		t.Errorf("locale = %q", got)
	}
	if w.Header().Get("Vary") != "Accept-Language" {
		t.Errorf("Vary = %q", w.Header().Get("Vary"))
	}

	// Without the middleware, the default language.
	if l := FromContext(t.Context()).Locale(); l != BaseLocale {
		t.Errorf("without middleware: %q", l)
	}
}

func TestLoadRejectsInconsistentCatalogs(t *testing.T) {
	base := `{"a": "A", "n": {"one": "1 x", "other": "{count} x"}}`
	for name, tc := range map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"missing key": {fstest.MapFS{
			"locales/en.json":    {Data: []byte(base)},
			"locales/es-MX.json": {Data: []byte(`{"n": {"one": "1 x", "other": "{count} x"}}`)},
		}, `es-MX.json: missing "a"`},
		"extra key": {fstest.MapFS{
			"locales/en.json":    {Data: []byte(base)},
			"locales/es-MX.json": {Data: []byte(`{"a": "A", "b": "B", "n": {"one": "1", "other": "2"}}`)},
		}, `"b" is not in en.json`},
		"plural mismatch": {fstest.MapFS{
			"locales/en.json":    {Data: []byte(base)},
			"locales/es-MX.json": {Data: []byte(`{"a": "A", "n": "x"}`)},
		}, `"n" must be plural`},
		"bad plural": {fstest.MapFS{
			"locales/en.json": {Data: []byte(`{"n": {"one": "1"}}`)},
		}, `"one" and "other"`},
		"bad file name": {fstest.MapFS{
			"locales/en.json":    {Data: []byte(base)},
			"locales/es_mx.json": {Data: []byte(base)},
		}, "language tag"},
		"no base": {fstest.MapFS{
			"locales/es-MX.json": {Data: []byte(base)},
		}, "missing the base catalog"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
