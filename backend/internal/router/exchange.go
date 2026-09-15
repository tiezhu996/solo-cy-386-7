package router

import (
	"github.com/gin-gonic/gin"
	"github.com/marketpal/marketpal/internal/handler"
	"github.com/marketpal/marketpal/internal/middleware"
)

// RegisterExchangeRoutes 换物提案模块路由（全部需要登录；参与方/所有者校验在 service 内完成）。
func RegisterExchangeRoutes(api *gin.RouterGroup, h *handler.ExchangeHandler, secret string) {
	g := api.Group("/exchange/proposals", middleware.Auth(secret))
	{
		g.GET("", h.List)
		g.POST("", h.Create)
		g.GET("/:id", h.Detail)
		g.POST("/:id/accept", h.Accept)
		g.POST("/:id/reject", h.Reject)
		g.POST("/:id/counter", h.Counter)
		g.POST("/:id/cancel", h.Cancel)
	}
}
