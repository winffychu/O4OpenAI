package middleware

import (
	"github.com/gin-gonic/gin"
)

// ============================================================
// CORS middleware
// ============================================================

// CORSMiddleware adds CORS headers for browser clients
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Requested-With, OpenAI-Organization, OpenAI-Project, x-api-key, anthropic-version")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
