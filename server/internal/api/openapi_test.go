package api

import (
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/pete-builds/vcrx-media-server/server/api"
)

// TestOpenAPIMatchesRoutes fails when a route is registered without being in
// server/api/openapi.yaml, or documented without being registered.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	var spec struct {
		OpenAPI string                            `yaml:"openapi"`
		Paths   map[string]map[string]interface{} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.Spec, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("openapi version %q, want 3.1.x", spec.OpenAPI)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "put", "post", "delete", "patch", "head", "options":
				documented[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	registered := map[string]bool{}
	for _, rt := range (&Server{}).Routes() {
		key := rt.Method + " " + rt.Path
		if registered[key] {
			t.Errorf("route registered twice: %s", key)
		}
		registered[key] = true
		if !strings.HasPrefix(rt.Path, "/api/v1/") && rt.Path != "/healthz" && rt.Path != "/readyz" {
			t.Errorf("route %s is outside /api/v1", key)
		}
	}
	var missing, extra []string
	for k := range registered {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !registered[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	for _, k := range missing {
		t.Errorf("registered but not in openapi.yaml: %s", k)
	}
	for _, k := range extra {
		t.Errorf("in openapi.yaml but not registered: %s", k)
	}
}
