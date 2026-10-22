package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/httpapi"
)

func (h *Handler) registerPasswordResetRoutes(group *gin.RouterGroup) {
	cfg := h.service.security
	// Reuse the IP limiter, preserving its socket-address trust boundary and cleanup.
	limiter := httpapi.NewRateLimiter(config.RateLimitConfig{Enabled: cfg.PasswordResetIPRequestsPerHour > 0,
		RequestsPerMinute: 60, Burst: cfg.PasswordResetIPRequestsPerHour}, h.service.clk)
	// The specialized constructor uses hourly refill; global limit remains separate.
	if limiter != nil {
		limiter.SetHourlyRate(cfg.PasswordResetIPRequestsPerHour)
	}
	group.POST("/password-reset/request", httpapi.RateLimitMiddleware(limiter), h.requestPasswordReset)
	group.POST("/password-reset/confirm", h.confirmPasswordReset)
}
func (h *Handler) requestPasswordReset(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpapi.BindJSON(c, &in); err != nil {
		httpapi.Abort(c, err)
		return
	}
	if err := h.service.RequestPasswordReset(c.Request.Context(), in.Email); err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondAccepted(c, gin.H{"status": "accepted"})
}
func (h *Handler) confirmPasswordReset(c *gin.Context) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := httpapi.BindJSON(c, &in); err != nil {
		httpapi.Abort(c, err)
		return
	}
	if err := h.service.ConfirmPasswordReset(c.Request.Context(), in.Token, in.NewPassword); err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondNoContent(c)
}
