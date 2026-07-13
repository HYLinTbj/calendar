package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS allows the browser frontend (served from a different origin/port, e.g.
// nginx on :80 or a file:// page) to call the API on :8080. Tokens travel in the
// Authorization header rather than cookies, so a wildcard origin is safe here.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
