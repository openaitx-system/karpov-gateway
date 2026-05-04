package gateway

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

type APIKeyHandler struct {
	svc *auth.Service
}

func NewAPIKeyHandler(svc *auth.Service) *APIKeyHandler {
	return &APIKeyHandler{svc: svc}
}

func (h *APIKeyHandler) Mount(e *gin.Engine) {
	g := e.Group("/v1/auth/api-keys")
	g.GET("/:id", h.get)
	g.PATCH("/:id", h.update)
	g.POST("/:id/enabled", h.setEnabled)
	g.DELETE("/:id", h.revoke)
}

func (h *APIKeyHandler) get(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	rec, err := h.svc.GetAPIKey(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "api key not found")
		return
	}
	OK(c, apiKeyRecordToResponse(rec))
}

type updateAPIKeyBody struct {
	Name           *string  `json:"name"`
	Description    *string  `json:"description"`
	Scopes         []string `json:"scopes"`
	IPAllowlist    []string `json:"ipAllowlist"`
	RateLimitRPM   *int     `json:"rateLimitRpm"`
	RateLimitDaily *int64   `json:"rateLimitDaily"`
	ExpiresAt      *string  `json:"expiresAt"`
}

func (h *APIKeyHandler) update(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	var body updateAPIKeyBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid request body")
		return
	}
	input := auth.UpdateAPIKeyInput{
		Name:           body.Name,
		Description:    body.Description,
		Scopes:         body.Scopes,
		IPAllow:        body.IPAllowlist,
		RateLimitRPM:   body.RateLimitRPM,
		RateLimitDaily: body.RateLimitDaily,
	}
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err == nil {
			input.ExpiresAt = &t
		}
	}
	rec, err := h.svc.UpdateAPIKey(c.Request.Context(), userID, c.Param("id"), input)
	if err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "api key not found or already revoked")
		return
	}
	OK(c, apiKeyRecordToResponse(rec))
}

type setEnabledBody struct {
	Enabled bool `json:"enabled"`
}

func (h *APIKeyHandler) setEnabled(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	var body setEnabledBody
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetAPIKeyEnabled(c.Request.Context(), userID, c.Param("id"), body.Enabled); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "api key not found")
		return
	}
	OK(c, nil)
}

func (h *APIKeyHandler) revoke(c *gin.Context) {
	userID := authUserID(c)
	if userID == "" {
		Fail(c, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
		return
	}
	if err := h.svc.RevokeAPIKey(c.Request.Context(), userID, c.Param("id")); err != nil {
		Fail(c, http.StatusNotFound, CodeNotFound, "api key not found")
		return
	}
	OK(c, nil)
}

func apiKeyRecordToResponse(r *auth.APIKeyRecord) map[string]any {
	now := time.Now().UTC()
	out := map[string]any{
		"id":             r.ID,
		"prefix":         r.Prefix,
		"name":           r.Name,
		"description":    r.Description,
		"scopes":         r.Scopes,
		"ipAllowlist":    r.IPAllow,
		"rateLimitRpm":   r.RateLimitRPM,
		"rateLimitDaily": r.RateLimitDaily,
		"enabled":        r.Enabled,
		"status":         r.Status(now),
		"totalRequests":  r.TotalRequests,
		"createdAt":      r.CreatedAt.Format(time.RFC3339),
	}
	if !r.ExpiresAt.IsZero() {
		out["expiresAt"] = r.ExpiresAt.Format(time.RFC3339)
	}
	if !r.LastUsedAt.IsZero() {
		out["lastUsedAt"] = r.LastUsedAt.Format(time.RFC3339)
	}
	return out
}
