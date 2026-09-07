package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// These groups inherit existing authentication, auditing and panel limits.
func registerSalesUserRoutes(authenticated *gin.RouterGroup, h *handler.Handlers) {
	if h.Sales == nil {
		return
	}
	sales := authenticated.Group("/sales")
	sales.GET("/me", h.Sales.Me)
	sales.GET("/customers", h.Sales.Customers)
	sales.GET("/ledger", h.Sales.Ledger)
	sales.GET("/settlements", h.Sales.Settlements)
}

func registerSalesAdminRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	if h.Admin == nil || h.Admin.Sales == nil {
		return
	}
	sales, api := admin.Group("/sales"), h.Admin.Sales
	sales.GET("/settings", api.GetSettings)
	sales.PUT("/settings", api.SaveSettings)
	sales.GET("/partners", api.ListPartners)
	sales.POST("/partners", api.CreatePartner)
	sales.PUT("/partners/:id", api.UpdatePartner)
	sales.GET("/partners/:id/overview", api.Overview)
	sales.GET("/customers", api.Customers)
	sales.GET("/ledger", api.Ledger)
	sales.GET("/settlements", api.Settlements)
	sales.POST("/settlements", api.CreateSettlement)
	sales.POST("/settlements/:id/confirm", api.ConfirmSettlement)
	sales.POST("/settlements/:id/pay", api.PaySettlement)
	sales.POST("/adjustments", api.Adjust)
}
