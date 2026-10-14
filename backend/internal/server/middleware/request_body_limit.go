package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestBodyLimit 使用 MaxBytesReader 限制请求体大小。
func RequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Preserve the server's explicit empty-body sentinel so downstream
		// observers need not infer completeness from an untrusted Content-Length.
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
