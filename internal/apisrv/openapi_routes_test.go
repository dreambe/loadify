package apisrv

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestOpenAPICoversRoutes guards against the hand-maintained OpenAPI spec
// drifting from the real routes: every /api/v1 path on the router must appear
// in openapi.yaml. Cheap insurance against the drift we hit before.
func TestOpenAPICoversRoutes(t *testing.T) {
	srv := newTestServer(newFakeMeta(), &fakeCoord{})
	spec, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	specStr := string(spec)

	seen := map[string]bool{}
	walkFn := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if strings.HasPrefix(route, "/api/v1/") {
			seen[route] = true
		}
		return nil
	}
	if err := chi.Walk(srv.Handler().(*chi.Mux), walkFn); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	var missing []string
	for route := range seen {
		// openapi.yaml uses the same chi-style {param} path syntax.
		if !strings.Contains(specStr, "\n  "+route+":") {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Errorf("routes missing from openapi.yaml (keep the spec in sync): %v", missing)
	}
}
