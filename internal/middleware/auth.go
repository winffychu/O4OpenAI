package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/o4openai/internal/model"
)

// ============================================================
// Authentication middleware - validates API keys
// ============================================================

// APIKeyConfig holds the valid API keys
type APIKeyConfig struct {
	Keys       map[string]string // key -> user description
	HeaderName string            // typically "Authorization"
}

// NewAPIKeyConfig creates a new API key config
func NewAPIKeyConfig() *APIKeyConfig {
	return &APIKeyConfig{
		Keys:       make(map[string]string),
		HeaderName: "Authorization",
	}
}

// AuthMiddleware validates the Bearer token in the Authorization header
func AuthMiddleware(config *APIKeyConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(config.Keys) == 0 {
			// No keys configured, skip auth
			c.Next()
			return
		}

		authHeader := c.GetHeader(config.HeaderName)
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.ErrorDetail{
					Message: "Missing Authorization header",
					Type:    "invalid_request_error",
					Code:    "missing_api_key",
				},
			})
			c.Abort()
			return
		}

		// Extract Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.ErrorDetail{
					Message: "Invalid Authorization header format. Expected: Bearer <api_key>",
					Type:    "invalid_request_error",
					Code:    "invalid_api_key",
				},
			})
			c.Abort()
			return
		}

		apiKey := parts[1]
		if _, valid := config.Keys[apiKey]; !valid {
			c.JSON(http.StatusUnauthorized, model.ErrorResponse{
				Error: model.ErrorDetail{
					Message: "Invalid API key",
					Type:    "authentication_error",
					Code:    "invalid_api_key",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
