package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	jwtSecret = "jwt-secret-0123456789abcdef0123456789"
	otpSecret = "otp-secret-0123456789abcdef0123456789"
)

// baseEnv returns the minimal valid environment merged with overrides.
func baseEnv(overrides map[string]string) map[string]string {
	m := map[string]string{
		"DATABASE_URL":    "postgres://u:p@localhost:5432/db",
		"AUTH_JWT_SECRET": jwtSecret,
		"AUTH_OTP_SECRET": otpSecret,
	}
	for k, v := range overrides {
		m[k] = v
	}
	return m
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(baseEnv(nil))
	require.NoError(t, err)
	require.Equal(t, EnvLocal, cfg.App.Env)
	require.Equal(t, ":8080", cfg.HTTP.Addr)
	require.Equal(t, 6, cfg.Auth.OTPLength)
	require.Equal(t, SMSProviderConsole, cfg.SMS.Provider)
	require.Empty(t, cfg.Auth.TestPhoneOTPs)
}

func TestLoadIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:9999")
	t.Setenv("AUTH_TEST_PHONE_OTPS", "+919999999999:123456")

	cfg, err := LoadFrom(baseEnv(nil))
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTP.Addr)
	require.Empty(t, cfg.Auth.TestPhoneOTPs)
}

func TestLoadReadsProcessEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("AUTH_JWT_SECRET", jwtSecret)
	t.Setenv("AUTH_OTP_SECRET", otpSecret)
	t.Setenv("HTTP_ADDR", "127.0.0.1:9999")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9999", cfg.HTTP.Addr)
}

func TestLoadRequiresSecretsAndDatabase(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(map[string]string{})
	require.Error(t, err)
}

func TestValidateRejectsShortAndSharedSecrets(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{"AUTH_JWT_SECRET": "short"}))
	require.ErrorContains(t, err, "AUTH_JWT_SECRET must be at least")

	_, err = LoadFrom(baseEnv(map[string]string{"AUTH_OTP_SECRET": jwtSecret}))
	require.ErrorContains(t, err, "must differ")
}

func TestParsesTestPhoneOTPs(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(baseEnv(map[string]string{
		"AUTH_TEST_PHONE_OTPS": "+919999999999:123456, +919888888888:654321",
	}))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"+919999999999": "123456",
		"+919888888888": "654321",
	}, cfg.Auth.TestPhoneOTPs)
}

func TestRejectsMalformedTestPhoneOTPs(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{"AUTH_TEST_PHONE_OTPS": "9999999999:123456"}))
	require.ErrorContains(t, err, "AUTH_TEST_PHONE_OTPS")
}

func TestRejectsTestPhoneCodeOfWrongLength(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{"AUTH_TEST_PHONE_OTPS": "+919999999999:1234"}))
	require.ErrorContains(t, err, "must be 6 digits")
}

func TestProductionGuards(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{
		"APP_ENV":              "production",
		"AUTH_TEST_PHONE_OTPS": "+919999999999:123456",
		"RATE_LIMIT_ENABLED":   "false",
	}))
	require.Error(t, err)
	msg := err.Error()
	require.True(t, strings.Contains(msg, "SMS_PROVIDER=console is not allowed in production"), msg)
	require.True(t, strings.Contains(msg, "AUTH_TEST_PHONE_OTPS is not allowed in production"), msg)
	require.True(t, strings.Contains(msg, "RATE_LIMIT_ENABLED=false is not allowed in production"), msg)
}

func TestProductionWithMSG91IsValid(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(baseEnv(map[string]string{
		"APP_ENV":           "production",
		"SMS_PROVIDER":      "msg91",
		"MSG91_AUTH_KEY":    "key",
		"MSG91_TEMPLATE_ID": "tmpl",
	}))
	require.NoError(t, err)
	require.True(t, cfg.IsProduction())
}

func TestMSG91RequiresCredentials(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{"SMS_PROVIDER": "msg91"}))
	require.ErrorContains(t, err, "MSG91_AUTH_KEY and MSG91_TEMPLATE_ID are required")
}

func TestAdminToken(t *testing.T) {
	t.Parallel()

	cfg, err := LoadFrom(baseEnv(nil))
	require.NoError(t, err)
	require.False(t, cfg.AdminEnabled(), "admin routes are off unless a token hash is configured")

	hash := strings.Repeat("AB", 32)
	cfg, err = LoadFrom(baseEnv(map[string]string{"ADMIN_API_TOKEN_SHA256": hash}))
	require.NoError(t, err)
	require.True(t, cfg.AdminEnabled())
	require.Equal(t, strings.ToLower(hash), cfg.Admin.TokenSHA256)

	_, err = LoadFrom(baseEnv(map[string]string{"ADMIN_API_TOKEN_SHA256": "not-a-hash"}))
	require.ErrorContains(t, err, "ADMIN_API_TOKEN_SHA256 must be 64 hex characters")
}

func TestRejectsUnknownEnv(t *testing.T) {
	t.Parallel()

	_, err := LoadFrom(baseEnv(map[string]string{"APP_ENV": "prod"}))
	require.ErrorContains(t, err, "APP_ENV must be one of")
}
