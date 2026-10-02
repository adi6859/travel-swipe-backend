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

func setBaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("AUTH_JWT_SECRET", jwtSecret)
	t.Setenv("AUTH_OTP_SECRET", otpSecret)
}

func TestLoadDefaults(t *testing.T) {
	setBaseEnv(t)

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, EnvLocal, cfg.App.Env)
	require.Equal(t, ":8080", cfg.HTTP.Addr)
	require.Equal(t, 6, cfg.Auth.OTPLength)
	require.Equal(t, SMSProviderConsole, cfg.SMS.Provider)
	require.Empty(t, cfg.Auth.TestPhoneOTPs)
}

func TestLoadRequiresSecretsAndDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("AUTH_JWT_SECRET", "")
	t.Setenv("AUTH_OTP_SECRET", "")

	_, err := Load()
	require.Error(t, err)
}

func TestValidateRejectsShortAndSharedSecrets(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AUTH_JWT_SECRET", "short")
	_, err := Load()
	require.ErrorContains(t, err, "AUTH_JWT_SECRET must be at least")

	setBaseEnv(t)
	t.Setenv("AUTH_OTP_SECRET", jwtSecret)
	_, err = Load()
	require.ErrorContains(t, err, "must differ")
}

func TestParsesTestPhoneOTPs(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AUTH_TEST_PHONE_OTPS", "+919999999999:123456, +919888888888:654321")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"+919999999999": "123456",
		"+919888888888": "654321",
	}, cfg.Auth.TestPhoneOTPs)
}

func TestRejectsMalformedTestPhoneOTPs(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AUTH_TEST_PHONE_OTPS", "9999999999:123456")

	_, err := Load()
	require.ErrorContains(t, err, "AUTH_TEST_PHONE_OTPS")
}

func TestRejectsTestPhoneCodeOfWrongLength(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AUTH_TEST_PHONE_OTPS", "+919999999999:1234")

	_, err := Load()
	require.ErrorContains(t, err, "must be 6 digits")
}

func TestProductionGuards(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_TEST_PHONE_OTPS", "+919999999999:123456")
	t.Setenv("RATE_LIMIT_ENABLED", "false")

	_, err := Load()
	require.Error(t, err)
	msg := err.Error()
	require.True(t, strings.Contains(msg, "SMS_PROVIDER=console is not allowed in production"), msg)
	require.True(t, strings.Contains(msg, "AUTH_TEST_PHONE_OTPS is not allowed in production"), msg)
	require.True(t, strings.Contains(msg, "RATE_LIMIT_ENABLED=false is not allowed in production"), msg)
}

func TestProductionWithMSG91IsValid(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SMS_PROVIDER", "msg91")
	t.Setenv("MSG91_AUTH_KEY", "key")
	t.Setenv("MSG91_TEMPLATE_ID", "tmpl")

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.IsProduction())
}

func TestMSG91RequiresCredentials(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("SMS_PROVIDER", "msg91")

	_, err := Load()
	require.ErrorContains(t, err, "MSG91_AUTH_KEY and MSG91_TEMPLATE_ID are required")
}

func TestRejectsUnknownEnv(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("APP_ENV", "prod")

	_, err := Load()
	require.ErrorContains(t, err, "APP_ENV must be one of")
}
