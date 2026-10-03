// Package config loads typed, environment-backed configuration and validates it
// before the process starts serving traffic.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	EnvLocal      = "local"
	EnvTest       = "test"
	EnvStaging    = "staging"
	EnvProduction = "production"

	SMSProviderConsole = "console"
	SMSProviderMSG91   = "msg91"

	minSecretLength = 32
)

var (
	phoneE164 = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Config struct {
	App       AppConfig
	HTTP      HTTPConfig
	Postgres  PostgresConfig
	Auth      AuthConfig
	SMS       SMSConfig
	RateLimit RateLimitConfig
	Admin     AdminConfig
}

type AdminConfig struct {
	// TokenSHA256 is the hex SHA-256 of the admin bearer token. Empty disables
	// the /admin/v1 routes entirely.
	TokenSHA256 string `env:"ADMIN_API_TOKEN_SHA256"`
}

// AdminEnabled reports whether admin routes should be served.
func (c *Config) AdminEnabled() bool {
	return c.Admin.TokenSHA256 != ""
}

type AppConfig struct {
	Name     string `env:"APP_NAME" envDefault:"travel-swipe-api"`
	Env      string `env:"APP_ENV" envDefault:"local"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

type HTTPConfig struct {
	Addr              string        `env:"HTTP_ADDR" envDefault:":8080"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"20s"`
	MaxBodyBytes      int64         `env:"HTTP_MAX_BODY_BYTES" envDefault:"1048576"`
	// AdminMaxBodyBytes applies to /admin/v1 instead of MaxBodyBytes (catalogue imports are large).
	AdminMaxBodyBytes int64 `env:"HTTP_ADMIN_MAX_BODY_BYTES" envDefault:"10485760"`
	// TrustedProxies controls which upstreams may set X-Forwarded-For. Empty
	// means the direct peer address is used as the client IP.
	TrustedProxies []string `env:"HTTP_TRUSTED_PROXIES" envSeparator:","`
}

type PostgresConfig struct {
	URL             string        `env:"DATABASE_URL,required"`
	MaxOpenConns    int           `env:"DATABASE_MAX_OPEN_CONNS" envDefault:"20"`
	MaxIdleConns    int           `env:"DATABASE_MAX_IDLE_CONNS" envDefault:"10"`
	ConnMaxLifetime time.Duration `env:"DATABASE_CONN_MAX_LIFETIME" envDefault:"30m"`
	ConnMaxIdleTime time.Duration `env:"DATABASE_CONN_MAX_IDLE_TIME" envDefault:"5m"`
}

type AuthConfig struct {
	JWTSecret       string        `env:"AUTH_JWT_SECRET,required"`
	JWTIssuer       string        `env:"AUTH_JWT_ISSUER" envDefault:"travel-swipe-api"`
	JWTAudience     string        `env:"AUTH_JWT_AUDIENCE" envDefault:"travel-swipe-app"`
	AccessTokenTTL  time.Duration `env:"AUTH_ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTokenTTL time.Duration `env:"AUTH_REFRESH_TOKEN_TTL" envDefault:"720h"`

	// OTPSecret keys the HMAC used to hash OTP codes and refresh tokens at rest.
	// It must differ from JWTSecret so a leak of one does not compromise the other.
	OTPSecret         string        `env:"AUTH_OTP_SECRET,required"`
	OTPLength         int           `env:"AUTH_OTP_LENGTH" envDefault:"6"`
	OTPTTL            time.Duration `env:"AUTH_OTP_TTL" envDefault:"5m"`
	OTPResendCooldown time.Duration `env:"AUTH_OTP_RESEND_COOLDOWN" envDefault:"30s"`
	OTPMaxAttempts    int           `env:"AUTH_OTP_MAX_ATTEMPTS" envDefault:"5"`
	OTPMaxPerPhoneDay int           `env:"AUTH_OTP_MAX_PER_PHONE_PER_DAY" envDefault:"10"`

	// TestPhoneOTPs maps fixed phone numbers to fixed codes ("+919999999999:123456").
	// Forbidden in production; intended for automated tests and app-store review builds
	// running against staging.
	TestPhoneOTPPairs []string          `env:"AUTH_TEST_PHONE_OTPS" envSeparator:","`
	TestPhoneOTPs     map[string]string `env:"-"`
}

type SMSConfig struct {
	Provider        string `env:"SMS_PROVIDER" envDefault:"console"`
	MSG91AuthKey    string `env:"MSG91_AUTH_KEY"`
	MSG91TemplateID string `env:"MSG91_TEMPLATE_ID"`
	MSG91BaseURL    string `env:"MSG91_BASE_URL" envDefault:"https://control.msg91.com/api/v5/flow"`
}

type RateLimitConfig struct {
	Enabled bool `env:"RATE_LIMIT_ENABLED" envDefault:"true"`
	// AuthPerIPPerMinute bounds unauthenticated auth endpoint calls per client IP.
	AuthPerIPPerMinute int `env:"RATE_LIMIT_AUTH_PER_IP_PER_MINUTE" envDefault:"20"`
}

// Load parses the process environment and validates the resulting configuration.
func Load() (*Config, error) {
	return LoadFrom(env.ToMap(os.Environ()))
}

// LoadFrom parses and validates configuration from the given variables only,
// ignoring the process environment.
func LoadFrom(environ map[string]string) (*Config, error) {
	cfg := &Config{}
	if err := env.ParseWithOptions(cfg, env.Options{Environment: environ}); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) IsProduction() bool {
	return c.App.Env == EnvProduction
}

func (c *Config) normalize() error {
	c.App.Env = strings.ToLower(strings.TrimSpace(c.App.Env))
	c.SMS.Provider = strings.ToLower(strings.TrimSpace(c.SMS.Provider))
	c.Admin.TokenSHA256 = strings.ToLower(strings.TrimSpace(c.Admin.TokenSHA256))

	c.Auth.TestPhoneOTPs = make(map[string]string, len(c.Auth.TestPhoneOTPPairs))
	for _, pair := range c.Auth.TestPhoneOTPPairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		phone, code, ok := strings.Cut(pair, ":")
		if !ok || !phoneE164.MatchString(phone) || code == "" {
			return fmt.Errorf("AUTH_TEST_PHONE_OTPS: entry must be <E.164 phone>:<code>")
		}
		c.Auth.TestPhoneOTPs[phone] = code
	}
	return nil
}

// Validate enforces invariants that would otherwise surface as insecure or
// broken runtime behavior.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	switch c.App.Env {
	case EnvLocal, EnvTest, EnvStaging, EnvProduction:
	default:
		add("APP_ENV must be one of local, test, staging, production")
	}

	if len(c.Auth.JWTSecret) < minSecretLength {
		add("AUTH_JWT_SECRET must be at least %d characters", minSecretLength)
	}
	if len(c.Auth.OTPSecret) < minSecretLength {
		add("AUTH_OTP_SECRET must be at least %d characters", minSecretLength)
	}
	if c.Auth.JWTSecret != "" && c.Auth.JWTSecret == c.Auth.OTPSecret {
		add("AUTH_JWT_SECRET and AUTH_OTP_SECRET must differ")
	}
	if c.Auth.AccessTokenTTL <= 0 || c.Auth.AccessTokenTTL > time.Hour {
		add("AUTH_ACCESS_TOKEN_TTL must be between 1ns and 1h")
	}
	if c.Auth.RefreshTokenTTL <= c.Auth.AccessTokenTTL {
		add("AUTH_REFRESH_TOKEN_TTL must exceed AUTH_ACCESS_TOKEN_TTL")
	}
	if c.Auth.OTPLength < 4 || c.Auth.OTPLength > 8 {
		add("AUTH_OTP_LENGTH must be between 4 and 8")
	}
	for phone, code := range c.Auth.TestPhoneOTPs {
		if len(code) != c.Auth.OTPLength || strings.Trim(code, "0123456789") != "" {
			add("AUTH_TEST_PHONE_OTPS: code for %s must be %d digits", phone, c.Auth.OTPLength)
		}
	}
	if c.Auth.OTPTTL <= 0 {
		add("AUTH_OTP_TTL must be positive")
	}
	if c.Auth.OTPResendCooldown < 0 {
		add("AUTH_OTP_RESEND_COOLDOWN must not be negative")
	}
	if c.Auth.OTPMaxAttempts <= 0 {
		add("AUTH_OTP_MAX_ATTEMPTS must be positive")
	}
	if c.Auth.OTPMaxPerPhoneDay <= 0 {
		add("AUTH_OTP_MAX_PER_PHONE_PER_DAY must be positive")
	}

	if c.Admin.TokenSHA256 != "" && !sha256Hex.MatchString(c.Admin.TokenSHA256) {
		add("ADMIN_API_TOKEN_SHA256 must be 64 hex characters (the SHA-256 of the admin token)")
	}
	if c.HTTP.MaxBodyBytes <= 0 || c.HTTP.AdminMaxBodyBytes <= 0 {
		add("HTTP_MAX_BODY_BYTES and HTTP_ADMIN_MAX_BODY_BYTES must be positive")
	}

	switch c.SMS.Provider {
	case SMSProviderConsole:
	case SMSProviderMSG91:
		if c.SMS.MSG91AuthKey == "" || c.SMS.MSG91TemplateID == "" {
			add("MSG91_AUTH_KEY and MSG91_TEMPLATE_ID are required when SMS_PROVIDER=msg91")
		}
	default:
		add("SMS_PROVIDER must be console or msg91")
	}

	if c.IsProduction() {
		if c.SMS.Provider == SMSProviderConsole {
			add("SMS_PROVIDER=console is not allowed in production")
		}
		if len(c.Auth.TestPhoneOTPs) > 0 {
			add("AUTH_TEST_PHONE_OTPS is not allowed in production")
		}
		if !c.RateLimit.Enabled {
			add("RATE_LIMIT_ENABLED=false is not allowed in production")
		}
	}

	return errors.Join(errs...)
}
