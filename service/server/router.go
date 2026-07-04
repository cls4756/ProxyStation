package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/ProxyStation/proxystation/core/engine"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func NewRouter(guiDir string) *gin.Engine {
	r := gin.Default()

	// 初始化日志回调
	engine.LogCallback = addLog

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	r.POST("/api/auth/login", login)
	r.POST("/api/auth/logout", logout)
	r.GET("/api/auth/me", authMe)
	r.Use(AuthRequired())

	api := r.Group("/api")
	{
		// 节点管理
		api.GET("/servers", getServers)
		api.GET("/servers/search", searchServers)
		api.POST("/servers", importServers)
		api.DELETE("/servers", deleteServers)
		api.PUT("/servers/:index", editServer)
		api.GET("/server/link", getServerLink)
		api.POST("/server/copy-to-group", copyServerToGroup)
		// 统一导入入口（自动判断节点/订阅）
		api.POST("/import", importAuto)

		// 订阅管理
		api.GET("/subscriptions", getSubscriptions)
		api.POST("/subscriptions", addSubscription)
		api.PUT("/subscriptions/:index", updateSubscription)
		api.DELETE("/subscriptions/:index", deleteSubscription)
		api.POST("/subscriptions/:index/update", updateSubscriptionNodes)

		// 分组管理
		api.GET("/groups", getGroups)
		api.POST("/groups", createGroup)
		api.PUT("/groups/:index", updateGroup)
		api.DELETE("/groups/:index", deleteGroup)
		api.POST("/groups/:index/servers", addServerToGroup)
		api.DELETE("/groups/:index/servers", removeServerFromGroup)
		api.POST("/groups/cleanup-orphan", cleanupOrphanGroups)

		// 出站管理
		api.GET("/outbounds", getOutbounds)
		api.POST("/outbounds", createOutbound)
		api.PUT("/outbounds/:name", updateOutbound)
		api.DELETE("/outbounds/:name", deleteOutbound)
		api.POST("/outbounds/:name/connect", connectOutbound)
		api.POST("/outbounds/:name/disconnect", disconnectOutbound)
		api.GET("/cfgoodnet/ca/download", downloadCfGoodNetCA)
		api.POST("/cfgoodnet/ca/import", importCfGoodNetCAToSystem)

		// 测速
		api.POST("/ping", pingNodes)
		api.POST("/groups/:index/ping", pingGroup)

		// 设置
		api.GET("/setting", getSetting)
		api.PUT("/setting", setSetting)
		api.POST("/auth/change-password", changePassword)

		// 代理控制
		api.GET("/status", getStatus)
		api.POST("/start", startProxy)
		api.POST("/stop", stopProxy)
		api.GET("/kernels", getKernelStatus)
		api.GET("/kernels/:kernel/download", downloadKernel)
		api.GET("/kernels/:kernel/check-update", checkKernelUpdate)
		api.GET("/rule-sets/download", downloadRuleSets)
		api.GET("/rule-sets/info", getRuleSetInfos)

		// 数据文件下载
		api.GET("/data/:type/download", downloadData)

		// 日志流
		api.GET("/logs", getLogs)
		api.GET("/logs/stream", getLogsStream)

		// 路由规则
		api.GET("/routing-rules", getRoutingRules)
		api.PUT("/routing-rules", setRoutingRules)
		api.GET("/routing-rules/export", exportRoutingRules)
		api.POST("/routing-rules/import", importRoutingRules)

		// 自定义入站
		api.GET("/custom-inbounds", getCustomInbounds)
		api.PUT("/custom-inbounds", setCustomInbounds)
		api.GET("/builtin-proxies/status", getBuiltinProxyStatuses)
		api.POST("/builtin-proxies/:id/restart", restartBuiltinProxy)
	}

	// 前端静态文件服务
	if guiDir != "" {
		if _, err := os.Stat(guiDir); err == nil {
			r.Static("/assets", filepath.Join(guiDir, "assets"))
			r.StaticFile("/favicon.ico", filepath.Join(guiDir, "favicon.ico"))
			// SPA fallback：所有非 /api 路由都返回 index.html
			r.NoRoute(func(c *gin.Context) {
				indexPath := filepath.Join(guiDir, "index.html")
				if _, err := os.Stat(indexPath); err == nil {
					c.File(indexPath)
					return
				}
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			})
		} else {
			r.NoRoute(func(c *gin.Context) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			})
		}
	}

	return r
}
