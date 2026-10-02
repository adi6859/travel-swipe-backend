// Package sms delivers OTP codes.
package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/adi6859/travel-swipe-backend/internal/platform/logger"
)

// ConsoleSender logs codes instead of sending them. Config validation forbids
// it in production; it exists so local and test environments need no SMS account.
type ConsoleSender struct {
	logger *slog.Logger
}

func NewConsoleSender(log *slog.Logger) *ConsoleSender {
	return &ConsoleSender{logger: log}
}

func (s *ConsoleSender) SendOTP(ctx context.Context, phoneE164, code string) error {
	s.logger.WarnContext(ctx, "DEV SMS (not delivered)", "phone", logger.MaskPhone(phoneE164), "otp", code)
	return nil
}

// MSG91Sender delivers OTPs through the MSG91 Flow API (adapted from Krozd).
type MSG91Sender struct {
	authKey    string
	templateID string
	baseURL    string
	client     *http.Client
	maxRetries int
}

func NewMSG91Sender(authKey, templateID, baseURL string) *MSG91Sender {
	return &MSG91Sender{
		authKey:    authKey,
		templateID: templateID,
		baseURL:    baseURL,
		client:     &http.Client{Timeout: 10 * time.Second},
		maxRetries: 2,
	}
}

type msg91Request struct {
	TemplateID string           `json:"template_id"`
	ShortURL   string           `json:"short_url"`
	Recipients []msg91Recipient `json:"recipients"`
}

type msg91Recipient struct {
	Mobiles string `json:"mobiles"`
	OTP     string `json:"number"`
}

type msg91Response struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (s *MSG91Sender) SendOTP(ctx context.Context, phoneE164, code string) error {
	body, err := json.Marshal(msg91Request{
		TemplateID: s.templateID,
		ShortURL:   "0",
		Recipients: []msg91Recipient{{Mobiles: strings.TrimPrefix(phoneE164, "+"), OTP: code}},
	})
	if err != nil {
		return err
	}
	masked := logger.MaskPhone(phoneE164)

	var lastErr error
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}
		retry, err := s.send(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return fmt.Errorf("msg91: send to %s failed: %w", masked, lastErr)
}

func (s *MSG91Sender) send(ctx context.Context, body []byte) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authkey", s.authKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return true, fmt.Errorf("status %d", resp.StatusCode)
	case resp.StatusCode >= 300:
		return false, fmt.Errorf("status %d", resp.StatusCode)
	}

	var out msg91Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("decode response: %w", err)
	}
	if out.Type != "success" {
		return false, fmt.Errorf("api error: %s", out.Message)
	}
	return false, nil
}
