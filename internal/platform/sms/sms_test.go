package sms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMSG91SendsExpectedRequest(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "key-1", r.Header.Get("authkey"))
		var body msg91Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "tmpl-1", body.TemplateID)
		require.Equal(t, []msg91Recipient{{Mobiles: "919876543210", OTP: "123456"}}, body.Recipients)
		_, _ = w.Write([]byte(`{"type":"success","message":"ok"}`))
	}))
	defer srv.Close()

	require.NoError(t, NewMSG91Sender("key-1", "tmpl-1", srv.URL).SendOTP(context.Background(), "+919876543210", "123456"))
}

func TestMSG91RetriesTransientFailures(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"type":"success"}`))
	}))
	defer srv.Close()

	require.NoError(t, NewMSG91Sender("k", "t", srv.URL).SendOTP(context.Background(), "+919876543210", "1"))
	require.EqualValues(t, 3, calls.Load())
}

func TestMSG91DoesNotRetryAPIErrors(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"type":"error","message":"invalid template"}`))
	}))
	defer srv.Close()

	err := NewMSG91Sender("k", "t", srv.URL).SendOTP(context.Background(), "+919876543210", "123456")
	require.ErrorContains(t, err, "invalid template")
	require.NotContains(t, err.Error(), "9876543210", "phone must be masked")
	require.NotContains(t, err.Error(), "123456", "code must never appear in errors")
	require.EqualValues(t, 1, calls.Load())
}
