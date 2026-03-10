package middleware

import (
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CORS 返回配置好的跨域中间件。
// allowedOrigins 支持多源，用逗号分隔，例如:
//
//	"http://localhost:5173,https://yourapp.com"
func CORS(allowedOrigins string) gin.HandlerFunc {
	origins := splitTrim(allowedOrigins, ",")

	return cors.New(cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{
			"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS",
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Authorization",
			"X-Requested-With",
			"Accept",
		},
		ExposeHeaders: []string{
			"Content-Length",
			"X-Request-Id",
		},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
