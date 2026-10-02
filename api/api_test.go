package api_test

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/adi6859/travel-swipe-backend/api"
	"github.com/adi6859/travel-swipe-backend/internal/config"
	"github.com/adi6859/travel-swipe-backend/internal/modules/auth"
	"github.com/adi6859/travel-swipe-backend/internal/modules/travelprofile"
	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/internal/server"
)

type noopPinger struct{}

func (noopPinger) PingContext(context.Context) error { return nil }

func loadSpec(t *testing.T) map[string]any {
	t.Helper()
	var spec map[string]any
	require.NoError(t, yaml.Unmarshal(api.OpenAPISpec, &spec))
	return spec
}

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	responder := httpx.NewResponder(log)
	authHandler := auth.NewHandler(nil, responder, func(c *gin.Context) { c.Next() })

	r, err := server.NewRouter(server.Deps{
		Config: &config.Config{
			App:  config.AppConfig{Env: config.EnvTest},
			HTTP: config.HTTPConfig{MaxBodyBytes: 1 << 20},
		},
		Logger:    log,
		Responder: responder,
		DB:        noopPinger{},
		Modules: []server.Module{
			authHandler,
			users.NewHandler(nil, responder, authHandler.RequireAuthMiddleware()),
			travelprofile.NewHandler(nil, responder, authHandler.RequireAuthMiddleware()),
		},
	})
	require.NoError(t, err)

	pathParam := regexp.MustCompile(`:(\w+)`)
	var out []string
	for _, rt := range r.Routes() {
		out = append(out, rt.Method+" "+pathParam.ReplaceAllString(rt.Path, "{$1}"))
	}
	sort.Strings(out)
	return out
}

func documentedRoutes(spec map[string]any) []string {
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	var out []string
	for path, item := range spec["paths"].(map[string]any) {
		for method := range item.(map[string]any) {
			if methods[method] {
				out = append(out, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestSpecMatchesRegisteredRoutes(t *testing.T) {
	spec := loadSpec(t)
	require.Equal(t, "3.1.0", spec["openapi"])
	require.Equal(t, registeredRoutes(t), documentedRoutes(spec),
		"api/openapi.yaml and the router disagree; update the spec when adding or removing routes")
}

func TestSpecRefsResolve(t *testing.T) {
	spec := loadSpec(t)

	var refs []string
	var walk func(v any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			for k, child := range n {
				if s, ok := child.(string); ok && k == "$ref" {
					refs = append(refs, s)
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(spec)
	require.NotEmpty(t, refs)

	for _, ref := range refs {
		require.True(t, strings.HasPrefix(ref, "#/"), "external ref %s", ref)
		var node any = spec
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			m, ok := node.(map[string]any)
			require.True(t, ok, "ref %s does not resolve", ref)
			node, ok = m[part]
			require.True(t, ok, "ref %s does not resolve", ref)
		}
	}
}
