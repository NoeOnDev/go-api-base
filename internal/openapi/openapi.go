// Package openapi has the contract of the API: openapi.yaml, a description of every route, its
// parameters and its responses that programs can read. The frontend generates its types from it,
// and the tests check every response against it (see docs/openapi.md).
package openapi

import (
	_ "embed"
	"net/http"
)

// Spec is the OpenAPI document, as it is written in openapi.yaml.
//
//go:embed openapi.yaml
var Spec []byte

// Handler answers GET /openapi.yaml with the document. It is public, like the routes it describes
// are known to whoever uses the frontend.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		// It changes with each deploy, not between them.
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(Spec)
	})
}
