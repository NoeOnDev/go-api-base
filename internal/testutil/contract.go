package testutil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/NoeOnDev/go-api-base/internal/openapi"
)

// contractAPI is the API that NewAPI returns. Do, Upload and the others recognize it and check each
// response against openapi.yaml (see checkContract): a handler in a test of its own, with routes
// the document does not have, is not checked.
type contractAPI struct{ http.Handler }

var contract struct {
	once   sync.Once
	router routers.Router
	err    error
}

// contractRouter loads openapi.yaml once for all the tests of a package.
func contractRouter() (routers.Router, error) {
	contract.once.Do(func() {
		loader := openapi3.NewLoader()
		doc, err := loader.LoadFromData(openapi.Spec)
		if err == nil {
			err = doc.Validate(loader.Context)
		}
		if err != nil {
			contract.err = err
			return
		}
		contract.router, contract.err = gorillamux.NewRouter(doc)
	})
	return contract.router, contract.err
}

// checkContract checks a request the API answered against openapi.yaml: that the route is there,
// that it documents the status it answered, and that the body has the shape it says. When the API
// accepted the request (2xx), the request is checked too: its parameters and its body are what the
// document describes. It is what keeps the document true: a handler that returns a new field, or a
// new error, fails its own tests until the document says so.
//
// A request to a route that does not exist, which some tests send on purpose, is left alone.
func checkContract(t *testing.T, api http.Handler, method, target string, header http.Header, body []byte, w *httptest.ResponseRecorder) {
	t.Helper()
	if _, ok := api.(contractAPI); !ok {
		return
	}
	router, err := contractRouter()
	if err != nil {
		t.Fatalf("openapi.yaml is not valid: %v", err)
	}

	r := httptest.NewRequest(method, target, strings.NewReader(string(body)))
	r.Header = header.Clone()
	route, pathParams, err := router.FindRoute(r)
	if err != nil {
		if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
			return
		}
		t.Errorf("%s %s answered %d, and the route is not in openapi.yaml (%v)", method, r.URL.Path, w.Code, err)
		return
	}

	// The statuses a frontend handles one by one are written in each operation. Its "default"
	// response is for the ones any route can answer (400, 429, 500).
	if listedByItself(w.Code) && route.Operation.Responses.Status(w.Code) == nil {
		t.Errorf("%s %s answered %d, which openapi.yaml does not list for it\nbody: %.300s", method, route.Path, w.Code, w.Body.String())
		return
	}

	ctx := context.Background()
	input := &openapi3filter.RequestValidationInput{
		Request: r, PathParams: pathParams, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, MultiError: true},
	}
	if w.Code >= 200 && w.Code < 300 {
		if err := openapi3filter.ValidateRequest(ctx, input); err != nil {
			t.Errorf("%s %s was accepted (%d), but the request is not what openapi.yaml describes:\n%v", method, route.Path, w.Code, contractError(err))
		}
	}
	response := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input, Status: w.Code, Header: w.Header(),
		Options: &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true},
	}
	response.SetBodyBytes(w.Body.Bytes())
	if err := openapi3filter.ValidateResponse(ctx, response); err != nil {
		t.Errorf("%s %s answered %d, which is not what openapi.yaml describes:\n%v\nbody: %.300s", method, route.Path, w.Code, contractError(err), w.Body.String())
	}
}

// listedByItself reports whether an operation must list the status by itself, instead of leaving
// it to its "default" response.
func listedByItself(status int) bool {
	switch status {
	case http.StatusNotModified, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
		return true
	}
	return status >= 200 && status < 300
}

// contractError leaves the errors of the library in a few lines.
func contractError(err error) string {
	var multi openapi3.MultiError
	if errors.As(err, &multi) {
		lines := make([]string, 0, len(multi))
		for _, e := range multi {
			lines = append(lines, "  - "+firstLine(e.Error()))
		}
		return strings.Join(lines, "\n")
	}
	return "  - " + firstLine(err.Error())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
