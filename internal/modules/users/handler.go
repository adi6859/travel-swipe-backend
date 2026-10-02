package users

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
	"github.com/adi6859/travel-swipe-backend/pkg/patch"
)

type Handler struct {
	svc         *Service
	responder   *httpx.Responder
	requireAuth gin.HandlerFunc
}

// NewHandler takes the auth middleware, which must place the caller's user ID
// in the request context (ctxutil.WithUserID).
func NewHandler(svc *Service, responder *httpx.Responder, requireAuth gin.HandlerFunc) *Handler {
	return &Handler{svc: svc, responder: responder, requireAuth: requireAuth}
}

func (h *Handler) RegisterRoutes(api *gin.RouterGroup) {
	me := api.Group("/users/me", h.requireAuth)
	me.GET("", h.responder.Handle(h.getMe))
	me.PATCH("", h.responder.Handle(h.updateMe))
}

type updateMeRequest struct {
	DisplayName patch.Field[string] `json:"display_name"`
	Bio         patch.Field[string] `json:"bio"`
	Avatar      patch.Field[Avatar] `json:"avatar"`
	DateOfBirth patch.Field[string] `json:"date_of_birth"`
	Gender      patch.Field[string] `json:"gender"`
	HomeCity    patch.Field[string] `json:"home_city"`
	CountryCode patch.Field[string] `json:"country_code"`
}

type profileResponse struct {
	DisplayName string    `json:"display_name"`
	Bio         string    `json:"bio"`
	Avatar      *Avatar   `json:"avatar"`
	DateOfBirth *string   `json:"date_of_birth"`
	Gender      *string   `json:"gender"`
	HomeCity    *string   `json:"home_city"`
	CountryCode *string   `json:"country_code"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type meResponse struct {
	ID              string          `json:"id"`
	Phone           string          `json:"phone"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
	LastLoginAt     *time.Time      `json:"last_login_at"`
	ProfileComplete bool            `json:"profile_complete"`
	Profile         profileResponse `json:"profile"`
}

func (h *Handler) getMe(c *gin.Context) error {
	userID, err := callerID(c)
	if err != nil {
		return err
	}
	me, err := h.svc.GetMe(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toMeResponse(me))
	return nil
}

func (h *Handler) updateMe(c *gin.Context) error {
	userID, err := callerID(c)
	if err != nil {
		return err
	}
	var req updateMeRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	me, err := h.svc.UpdateMe(c.Request.Context(), userID, ProfileUpdate(req))
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toMeResponse(me))
	return nil
}

func callerID(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(ctxutil.UserID(c.Request.Context()))
	if err != nil {
		return uuid.Nil, apperrors.Unauthorized("authentication required")
	}
	return id, nil
}

func toMeResponse(me Me) meResponse {
	p := me.Profile
	out := meResponse{
		ID:        me.User.ID.String(),
		Phone:     me.User.PhoneE164,
		Status:    string(me.User.Status),
		CreatedAt: me.User.CreatedAt.UTC(),
		Profile: profileResponse{
			DisplayName: p.DisplayName,
			Bio:         p.Bio,
			Gender:      p.Gender,
			HomeCity:    p.HomeCity,
			CountryCode: p.CountryCode,
			UpdatedAt:   p.UpdatedAt.UTC(),
		},
	}
	if me.User.LastLoginAt != nil {
		at := me.User.LastLoginAt.UTC()
		out.LastLoginAt = &at
	}
	if p.AvatarURL != nil {
		avatar := Avatar{URL: *p.AvatarURL}
		if len(p.AvatarMeta) > 0 {
			_ = json.Unmarshal(p.AvatarMeta, &avatar)
		}
		out.Profile.Avatar = &avatar
	}
	if p.DateOfBirth != nil {
		dob := p.DateOfBirth.Format(dateOfBirthStyle)
		out.Profile.DateOfBirth = &dob
	}
	out.ProfileComplete = p.DisplayName != "" && p.DateOfBirth != nil
	return out
}
