package i18n

import (
	"context"
	"net/http"
)

type ctxKey struct{}

// WithTranslator returns a copy of ctx that carries t.
func WithTranslator(ctx context.Context, t Translator) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

// FromContext returns the translator of the request (see Middleware). Without one, it returns
// the default language of the Default catalog.
func FromContext(ctx context.Context) Translator {
	if t, ok := ctx.Value(ctxKey{}).(Translator); ok {
		return t
	}
	return Default().Translator("")
}

// Middleware chooses the language of each request from its Accept-Language header and stores
// the translator in the context, for FromContext. It also adds Vary: Accept-Language, because
// the same URL can answer in different languages.
func (c *Catalog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Language")
		t := c.Translator(r.Header.Get("Accept-Language"))
		next.ServeHTTP(w, r.WithContext(WithTranslator(r.Context(), t)))
	})
}
