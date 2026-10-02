package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/modules/users"
	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
)

// Handler exposes the auth HTTP API.
type Handler struct {
	svc         *Service
	responder   *httpx.Responder
	publicLimit gin.HandlerFunc
	requireAuth gin.HandlerFunc
}

// NewHandler wires routes. publicLimit throttles unauthenticated endpoints.
func NewHandler(svc *Service, responder *httpx.Responder, publicLimit gin.HandlerFunc) *Handler {
	return &Handler{
		svc:         svc,
		responder:   responder,
		publicLimit: publicLimit,
		requireAuth: RequireAuth(svc, responder),
	}
}

// RequireAuthMiddleware is shared with other modules' protected routes.
func (h *Handler) RequireAuthMiddleware() gin.HandlerFunc {
	return h.requireAuth
}

func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	g := api.Group("/auth")

	public := g.Group("", h.publicLimit)
	public.POST("/otp/request", h.responder.Handle(h.requestOTP))
	public.POST("/otp/verify", h.responder.Handle(h.verifyOTP))
	public.POST("/refresh", h.responder.Handle(h.refresh))

	g.POST("/logout", h.requireAuth, h.responder.Handle(h.logout))
	g.POST("/logout-all", h.requireAuth, h.responder.Handle(h.logoutAll))
}

type requestOTPRequest struct {
	Phone string `json:"phone" binding:"required,e164"`
}

type requestOTPResponse struct {
	ChallengeID string    `json:"challenge_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	ResendAfter time.Time `json:"resend_after"`
}

type verifyOTPRequest struct {
	Phone      string `json:"phone" binding:"required,e164"`
	Code       string `json:"code" binding:"required,numeric,min=4,max=8"`
	DeviceName string `json:"device_name" binding:"omitempty,max=100"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required,max=512"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Phone     string    `json:"phone"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type tokenResponse struct {
	TokenType        string    `json:"token_type"`
	SessionID        string    `json:"session_id"`
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type loginResponse struct {
	tokenResponse
	User      userResponse `json:"user"`
	IsNewUser bool         `json:"is_new_user"`
}

type logoutAllResponse struct {
	RevokedSessions int `json:"revoked_sessions"`
}

func (h *Handler) requestOTP(c *gin.Context) error {
	var req requestOTPRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		return err
	}
	out, err := h.svc.RequestOTP(c.Request.Context(), req.Phone, c.ClientIP())
	if err != nil {
		return err
	}
	c.JSON(http.StatusAccepted, requestOTPResponse{
		ChallengeID: out.ChallengeID.String(),
		ExpiresAt:   out.ExpiresAt,
		ResendAfter: out.ResendAfter,
	})
	return nil
}

func (h *Handler) verifyOTP(c *gin.Context) error {
	var req verifyOTPRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		return err
	}
	res, err := h.svc.VerifyOTP(c.Request.Context(), req.Phone, req.Code, ClientInfo{
		DeviceName: req.DeviceName,
		UserAgent:  c.Request.UserAgent(),
		IP:         c.ClientIP(),
	})
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, loginResponse{
		tokenResponse: toTokenResponse(res.Tokens),
		User:          toUserResponse(res.User),
		IsNewUser:     res.IsNewUser,
	})
	return nil
}

func (h *Handler) refresh(c *gin.Context) error {
	var req refreshRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		return err
	}
	pair, err := h.svc.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toTokenResponse(pair))
	return nil
}

func (h *Handler) logout(c *gin.Context) error {
	p, err := MustPrincipal(c)
	if err != nil {
		return err
	}
	if err := h.svc.Logout(c.Request.Context(), p); err != nil {
		return err
	}
	c.Status(http.StatusNoContent)
	return nil
}

func (h *Handler) logoutAll(c *gin.Context) error {
	p, err := MustPrincipal(c)
	if err != nil {
		return err
	}
	n, err := h.svc.LogoutAll(c.Request.Context(), p)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, logoutAllResponse{RevokedSessions: n})
	return nil
}

func toTokenResponse(p TokenPair) tokenResponse {
	return tokenResponse{
		TokenType:        "Bearer",
		SessionID:        p.SessionID.String(),
		AccessToken:      p.AccessToken,
		AccessExpiresAt:  p.AccessExpiresAt,
		RefreshToken:     p.RefreshToken,
		RefreshExpiresAt: p.RefreshExpiresAt,
	}
}

func toUserResponse(u users.User) userResponse {
	return userResponse{ID: u.ID.String(), Phone: u.PhoneE164, Status: string(u.Status), CreatedAt: u.CreatedAt}
}
