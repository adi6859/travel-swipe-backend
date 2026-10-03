package ingest

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/adi6859/travel-swipe-backend/internal/platform/httpx"
	apperrors "github.com/adi6859/travel-swipe-backend/pkg/errors"
)

// AdminHandler exposes source management and imports on /admin/v1. Callers
// mount it behind admin authentication.
type AdminHandler struct {
	svc       *Service
	responder *httpx.Responder
}

func NewAdminHandler(svc *Service, responder *httpx.Responder) *AdminHandler {
	return &AdminHandler{svc: svc, responder: responder}
}

func (h *AdminHandler) RegisterRoutes(admin *gin.RouterGroup) {
	admin.GET("/sources", h.responder.Handle(h.listSources))
	admin.PUT("/sources/:key", h.responder.Handle(h.putSource))
	admin.POST("/sources/:key/imports", h.responder.Handle(h.importBatch))
	admin.GET("/sources/:key/runs", h.responder.Handle(h.listRuns))
}

type sourceRequest struct {
	DisplayName        string     `json:"display_name"`
	BaseURL            string     `json:"base_url"`
	Enabled            *bool      `json:"enabled"`
	AutoPublish        bool       `json:"auto_publish"`
	RightsBasis        string     `json:"rights_basis"`
	ImageAllowed       bool       `json:"image_allowed"`
	DescriptionAllowed bool       `json:"description_allowed"`
	AttributionText    *string    `json:"attribution_text"`
	EvidenceRef        string     `json:"evidence_ref"`
	RightsExpiresAt    *time.Time `json:"rights_expires_at"`
	MissingGraceRuns   int        `json:"missing_grace_runs"`
}

type sourceResponse struct {
	Key                string     `json:"source_key"`
	DisplayName        string     `json:"display_name"`
	BaseURL            string     `json:"base_url"`
	Enabled            bool       `json:"enabled"`
	AutoPublish        bool       `json:"auto_publish"`
	RightsBasis        string     `json:"rights_basis"`
	ImageAllowed       bool       `json:"image_allowed"`
	DescriptionAllowed bool       `json:"description_allowed"`
	AttributionText    *string    `json:"attribution_text"`
	EvidenceRef        string     `json:"evidence_ref"`
	RightsExpiresAt    *time.Time `json:"rights_expires_at"`
	MissingGraceRuns   int        `json:"missing_grace_runs"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type importRequest struct {
	Trigger  string    `json:"trigger"`
	Complete bool      `json:"complete"`
	Listings []Listing `json:"listings"`
}

func (h *AdminHandler) listSources(c *gin.Context) error {
	sources, err := h.svc.ListSources(c.Request.Context())
	if err != nil {
		return err
	}
	out := make([]sourceResponse, 0, len(sources))
	for _, s := range sources {
		out = append(out, toSourceResponse(s))
	}
	c.JSON(http.StatusOK, gin.H{"sources": out})
	return nil
}

func (h *AdminHandler) putSource(c *gin.Context) error {
	var req sourceRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	src, err := h.svc.PutSource(c.Request.Context(), c.Param("key"), SourceInput{
		DisplayName:        req.DisplayName,
		BaseURL:            req.BaseURL,
		Enabled:            enabled,
		AutoPublish:        req.AutoPublish,
		RightsBasis:        RightsBasis(req.RightsBasis),
		ImageAllowed:       req.ImageAllowed,
		DescriptionAllowed: req.DescriptionAllowed,
		AttributionText:    req.AttributionText,
		EvidenceRef:        req.EvidenceRef,
		RightsExpiresAt:    req.RightsExpiresAt,
		MissingGraceRuns:   req.MissingGraceRuns,
	})
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, toSourceResponse(src))
	return nil
}

func (h *AdminHandler) importBatch(c *gin.Context) error {
	var req importRequest
	if err := httpx.DecodeStrictJSON(c, &req); err != nil {
		return err
	}
	trigger := Trigger(req.Trigger)
	if trigger == "" {
		trigger = TriggerManual
	}
	run, err := h.svc.Import(c.Request.Context(), c.Param("key"), Batch{Trigger: trigger, Complete: req.Complete, Listings: req.Listings})
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, run)
	return nil
}

func (h *AdminHandler) listRuns(c *gin.Context) error {
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return apperrors.InvalidFields("request validation failed", map[string]string{"limit": "must be between 1 and 100"})
		}
		limit = n
	}
	runs, err := h.svc.ListRuns(c.Request.Context(), c.Param("key"), limit)
	if err != nil {
		return err
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
	return nil
}

func toSourceResponse(s Source) sourceResponse {
	return sourceResponse{
		Key: s.Key, DisplayName: s.DisplayName, BaseURL: s.BaseURL, Enabled: s.Enabled, AutoPublish: s.AutoPublish,
		RightsBasis: string(s.RightsBasis), ImageAllowed: s.ImageAllowed, DescriptionAllowed: s.DescriptionAllowed,
		AttributionText: s.AttributionText, EvidenceRef: s.EvidenceRef, RightsExpiresAt: s.RightsExpiresAt,
		MissingGraceRuns: s.MissingGraceRuns, UpdatedAt: s.UpdatedAt.UTC(),
	}
}
