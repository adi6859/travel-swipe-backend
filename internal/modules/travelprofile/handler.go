package travelprofile

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	"github.com/adi6859/travel-swipe-backend/pkg/ctxutil"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
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
	api.GET("/travel-profile/options", h.requireAuth, h.responder.Handle(h.options))

	me := api.Group("/users/me/travel-profile", h.requireAuth)
	me.GET("", h.responder.Handle(h.get))
	me.PUT("", h.responder.Handle(h.replace))
}

type replaceRequest struct {
	TravelStyles []string `json:"travel_styles"`
	Interests    []string `json:"interests"`
	Languages    []string `json:"languages"`
	BudgetBand   *string  `json:"budget_band"`
	GroupSize    *string  `json:"group_size"`
	Pace         *string  `json:"pace"`
	Smoking      *string  `json:"smoking"`
	Drinking     *string  `json:"drinking"`
	Diet         *string  `json:"diet"`
}

type profileResponse struct {
	TravelStyles []string   `json:"travel_styles"`
	Interests    []string   `json:"interests"`
	Languages    []string   `json:"languages"`
	BudgetBand   *string    `json:"budget_band"`
	GroupSize    *string    `json:"group_size"`
	Pace         *string    `json:"pace"`
	Smoking      *string    `json:"smoking"`
	Drinking     *string    `json:"drinking"`
	Diet         *string    `json:"diet"`
	Complete     bool       `json:"complete"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

func (h *Handler) options(c *gin.Context) error {
	out, err := h.svc.Options(c.Request.Context())
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, out)
	return nil
}

func (h *Handler) get(c *gin.Context) error {
	userID, err := callerID(c)
	if err != nil {
		return err
	}
	p, err := h.svc.Get(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toResponse(p))
	return nil
}

func (h *Handler) replace(c *gin.Context) error {
	userID, err := callerID(c)
	if err != nil {
		return err
	}
	var req replaceRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	p, err := h.svc.Replace(c.Request.Context(), userID, Input(req))
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toResponse(p))
	return nil
}

func callerID(c *gin.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(ctxutil.UserID(c.Request.Context()))
	if err != nil {
		return uuid.Nil, apperrors.Unauthorized("authentication required")
	}
	return id, nil
}

func toResponse(p Profile) profileResponse {
	out := profileResponse{
		TravelStyles: nonNil(p.TravelStyles),
		Interests:    nonNil(p.Interests),
		Languages:    nonNil(p.Languages),
		BudgetBand:   p.BudgetBand,
		GroupSize:    p.GroupSize,
		Pace:         p.Pace,
		Smoking:      p.Smoking,
		Drinking:     p.Drinking,
		Diet:         p.Diet,
		Complete:     p.Complete(),
	}
	if p.UpdatedAt != nil {
		at := p.UpdatedAt.UTC()
		out.UpdatedAt = &at
	}
	return out
}
