// Package api embeds the OpenAPI contract for the HTTP API.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPISpec []byte
