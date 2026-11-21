package openapi

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The document must be a valid OpenAPI one, and have exactly the routes of the code. What each
// route answers is checked by the tests of each resource, against this same document (see
// testutil.Do).

func load(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(Spec)
	if err != nil {
		t.Fatalf("openapi.yaml does not load: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml is not a valid OpenAPI document: %v", err)
	}
	return doc
}

// A route as the code registers it: mux.Handle("GET /notes/{id}", …), handle("POST /auth/login", …).
var registered = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/[^" ]*)"`)

// routesOfTheCode reads the routes from the Go files of the API, tests aside.
func routesOfTheCode(t *testing.T) []string {
	t.Helper()
	var routes []string
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			code, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range registered.FindAllStringSubmatch(string(code), -1) {
				routes = append(routes, m[1]+" "+m[2])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(routes)
	return slices.Compact(routes)
}

func TestDocumentHasTheRoutesOfTheCode(t *testing.T) {
	doc := load(t)
	var documented []string
	ids := map[string]string{}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			route := method + " " + path
			documented = append(documented, route)
			switch {
			case op.OperationID == "":
				t.Errorf("%s has no operationId: the frontend's generator names its function after it", route)
			case ids[op.OperationID] != "":
				t.Errorf("%s and %s have the same operationId, %q", route, ids[op.OperationID], op.OperationID)
			}
			ids[op.OperationID] = route
			if op.Summary == "" {
				t.Errorf("%s has no summary", route)
			}
			if op.Responses.Default() == nil {
				t.Errorf("%s has no default response: every route can answer 400, 429 and 500", route)
			}
		}
	}
	slices.Sort(documented)

	code := routesOfTheCode(t)
	for _, route := range code {
		if !slices.Contains(documented, route) {
			t.Errorf("%s is a route of the code, and openapi.yaml does not have it", route)
		}
	}
	for _, route := range documented {
		if !slices.Contains(code, route) {
			t.Errorf("openapi.yaml has %s, which is not a route of the code", route)
		}
	}
}
