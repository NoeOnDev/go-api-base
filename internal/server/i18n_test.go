package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Los mensajes al usuario salen en el idioma que pide Accept-Language; sin uno disponible, en el
// idioma por defecto. El código del error no cambia con el idioma.
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
			t.Errorf("Accept-Language %q: falta Vary: Accept-Language", lang)
		}
	}

	// También los errores que no pasan por un handler, como una ruta inexistente.
	w := testutil.DoWithLanguage(t, api, "es-MX", nil, http.MethodGet, "/no-existe", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Code != "route_not_found" || resp.Error.Message != "La ruta no existe." {
		t.Errorf("ruta inexistente: %+v", resp.Error)
	}
}

func TestDefaultLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.DefaultLocale = "es-MX" }))

	w := testutil.Do(t, api, nil, http.MethodGet, "/no-existe", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Message != "La ruta no existe." {
		t.Errorf("sin Accept-Language: %q", resp.Error.Message)
	}
	w = testutil.DoWithLanguage(t, api, "en-US", nil, http.MethodGet, "/no-existe", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Message != "The route does not exist." {
		t.Errorf("con Accept-Language en-US: %q", resp.Error.Message)
	}
}
