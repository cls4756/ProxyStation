package server

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// noCacheDynamicResponse prevents browsers and reverse proxies from serving stale API data.
func noCacheDynamicResponse() func(*gin.Context) {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/subscribe/inbounds/") {
			c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
			c.Header("Pragma", "no-cache")
			c.Header("Expires", "0")
		}
		c.Next()
	}
}
