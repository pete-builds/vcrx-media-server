// Package apispec embeds the OpenAPI contract so the server can serve it and the
// drift test can compare it with the registered routes.
package apispec

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
