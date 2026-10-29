package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// User-facing messages come in the language Accept-Language asks for; without an available one,
// in the default language. The error code does not change with the language.
func TestErrorsInTheRequestLanguage(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	for lang, want := range map[string][2]string{
		"":                        {"The data sent is not valid.", "This field is required."},
		"es-MX":                   {"Los datos enviados no son válidos.", "Este campo es obligatorio."},
		"es-AR,es;q=0.9,en;q=0.8": {"Los datos enviados no son válidos.", "Este campo es obligatorio."},
		"fr-FR,fr;q=0.9":          {"The data sent is not valid.", "This field is required."},
	} {
		w := testutil.DoWithLanguage(t, api, lang, nil, http.MethodPost, "/auth/login", `{}`)
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
		if resp.Error.Code != "validation_failed" || resp.Error.Message != want[0] || resp.Error.Fields["email"] != want[1] {
			t.Errorf("Accept-Language %q: %+v", lang, resp.Error)
		}
		if !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Language") {
			t.Errorf("Accept-Language %q: missing Vary: Accept-Language", lang)
		}
	}

	// Errors that do not go through a handler too, like a route that does not exist.
	w := testutil.DoWithLanguage(t, api, "es-MX", nil, http.MethodGet, "/missing", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Code != "route_not_found" || resp.Error.Message != "La ruta no existe." {
		t.Errorf("route that does not exist: %+v", resp.Error)
	}
}

func TestDefaultLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.DefaultLocale = "es-MX" }))

	w := testutil.Do(t, api, nil, http.MethodGet, "/missing", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Message != "La ruta no existe." {
		t.Errorf("without Accept-Language: %q", resp.Error.Message)
	}
	w = testutil.DoWithLanguage(t, api, "en-US", nil, http.MethodGet, "/missing", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Message != "The route does not exist." {
		t.Errorf("with Accept-Language en-US: %q", resp.Error.Message)
	}
}
