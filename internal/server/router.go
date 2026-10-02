// Package server assembles the Gin engine and registers module routes.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/config"
	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/internal/platform/middleware"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

// Pinger reports database readiness.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Module registers its routes on the versioned API group.
type Module interface {
	RegisterRoutes(api *gin.RouterGroup)
}

type Deps struct {
	Config    *config.Config
	Logger    *slog.Logger
	Responder *httpx.Responder
	DB        Pinger
	Modules   []Module
}

func NewRouter(d Deps) (*gin.Engine, error) {
	if d.Config.App.Env == config.EnvLocal {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	httpx.SetupValidator()

	r := gin.New()
	r.HandleMethodNotAllowed = true
	if err := r.SetTrustedProxies(d.Config.HTTP.TrustedProxies); err != nil {
		return nil, err
	}

	r.Use(
		middleware.RequestID(),
		middleware.Recovery(d.Logger, d.Responder),
		middleware.RequestLogger(d.Logger),
		middleware.SecurityHeaders(),
		middleware.BodyLimit(d.Config.HTTP.MaxBodyBytes),
	)

	r.NoRoute(d.Responder.Handle(func(*gin.Context) error {
		return apperrors.NotFound("route not found")
	}))
	r.NoMethod(func(c *gin.Context) {
		d.Responder.AbortWithStatus(c, http.StatusMethodNotAllowed, apperrors.Invalid("method not allowed"))
	})

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", d.Responder.Handle(func(c *gin.Context) error {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.PingContext(ctx); err != nil {
			return apperrors.Unavailable("database unavailable", err)
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
		return nil
	}))

	api := r.Group("/api/v1")
	for _, m := range d.Modules {
		m.RegisterRoutes(api)
	}
	return r, nil
}
