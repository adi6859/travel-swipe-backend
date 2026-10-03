package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/config"
	"github.com/adi6859/travel-swipe-backend/internal/modules/auth"
	"github.com/adi6859/travel-swipe-backend/internal/modules/catalog"
	"github.com/adi6859/travel-swipe-backend/internal/modules/ingest"
	"github.com/adi6859/travel-swipe-backend/internal/modules/travelprofile"
	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/internal/platform/adminauth"
	"github.com/adi6859/travel-swipe-backend/internal/platform/database"
	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/internal/platform/logger"
	"github.com/adi6859/travel-swipe-backend/internal/platform/ratelimit"
	"github.com/adi6859/travel-swipe-backend/internal/platform/sms"
	"github.com/adi6859/travel-swipe-backend/internal/server"
	"github.com/adi6859/travel-swipe-backend/pkg/clock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.App.Name, cfg.App.Env, cfg.App.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer db.Close()

	responder := httpx.NewResponder(log)
	clk := clock.RealClock{}
	txm := database.NewTxManager(db)

	usersRepo := users.NewRepository(db)
	authSvc := auth.NewService(
		auth.Config{
			OTPLength:         cfg.Auth.OTPLength,
			OTPTTL:            cfg.Auth.OTPTTL,
			OTPResendCooldown: cfg.Auth.OTPResendCooldown,
			OTPMaxAttempts:    cfg.Auth.OTPMaxAttempts,
			OTPMaxPerPhoneDay: cfg.Auth.OTPMaxPerPhoneDay,
			RefreshTokenTTL:   cfg.Auth.RefreshTokenTTL,
			TestPhoneOTPs:     cfg.Auth.TestPhoneOTPs,
		},
		auth.NewPostgresStore(db),
		usersRepo,
		txm,
		auth.NewTokenIssuer(cfg.Auth.JWTSecret, cfg.Auth.JWTIssuer, cfg.Auth.JWTAudience, cfg.Auth.AccessTokenTTL, clk),
		auth.NewSecrets(cfg.Auth.OTPSecret),
		newSMSSender(cfg, log),
		clk,
		log,
	)
	authHandler := auth.NewHandler(authSvc, responder, authRateLimit(cfg, responder))
	usersHandler := users.NewHandler(users.NewService(usersRepo, clk), responder, authHandler.RequireAuthMiddleware())
	travelProfileHandler := travelprofile.NewHandler(
		travelprofile.NewService(travelprofile.NewRepository(db), txm, clk),
		responder,
		authHandler.RequireAuthMiddleware(),
	)

	catalogSvc := catalog.NewService(catalog.NewRepository(db), clk)
	catalogHandler := catalog.NewHandler(catalogSvc, responder, authHandler.RequireAuthMiddleware())

	deps := server.Deps{
		Config:    cfg,
		Logger:    log,
		Responder: responder,
		DB:        db,
		Modules:   []server.Module{authHandler, usersHandler, travelProfileHandler, catalogHandler},
	}
	if cfg.AdminEnabled() {
		adminAuth, err := adminauth.Require(cfg.Admin.TokenSHA256, responder)
		if err != nil {
			return err
		}
		deps.AdminAuth = adminAuth
		deps.AdminModules = []server.Module{
			ingest.NewAdminHandler(ingest.NewService(ingest.NewRepository(db), txm, clk, log), responder),
			catalog.NewAdminHandler(catalogSvc, responder),
		}
	} else {
		log.Info("admin api disabled: ADMIN_API_TOKEN_SHA256 is not set")
	}

	router, err := server.NewRouter(deps)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           router,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func newSMSSender(cfg *config.Config, log *slog.Logger) auth.SMSSender {
	if cfg.SMS.Provider == config.SMSProviderMSG91 {
		return sms.NewMSG91Sender(cfg.SMS.MSG91AuthKey, cfg.SMS.MSG91TemplateID, cfg.SMS.MSG91BaseURL)
	}
	return sms.NewConsoleSender(log)
}

func authRateLimit(cfg *config.Config, responder *httpx.Responder) gin.HandlerFunc {
	if !cfg.RateLimit.Enabled {
		return ratelimit.Disabled()
	}
	return ratelimit.PerIP("auth", ratelimit.New(cfg.RateLimit.AuthPerIPPerMinute, time.Minute), responder)
}
