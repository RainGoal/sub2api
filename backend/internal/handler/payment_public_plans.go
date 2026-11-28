package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type publicSubscriptionPlan struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	GroupPlatform   string   `json:"group_platform"`
	Price           float64  `json:"price"`
	OriginalPrice   *float64 `json:"original_price,omitempty"`
	Currency        string   `json:"currency,omitempty"`
	ValidityDays    int      `json:"validity_days"`
	ValidityUnit    string   `json:"validity_unit"`
	Features        []string `json:"features"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
	SortOrder       int      `json:"sort_order"`
}

type publicPlansResponse struct {
	Plans                    []publicSubscriptionPlan    `json:"plans"`
	SubscriptionUSDToCNYRate float64                     `json:"subscription_usd_to_cny_rate"`
	HasCNYPaymentMethod      bool                        `json:"has_cny_payment_method"`
	RechargeBonusTiers       []service.RechargeBonusTier `json:"recharge_bonus_tiers"`
	RechargeBonusMode        string                      `json:"recharge_bonus_mode"`
}

// GetPublicPlans returns the subscription plans available to unauthenticated visitors.
// GET /api/v1/payment/public/plans
func (h *PaymentHandler) GetPublicPlans(c *gin.Context) {
	ctx := c.Request.Context()
	result := publicPlansResponse{
		Plans:              []publicSubscriptionPlan{},
		RechargeBonusTiers: []service.RechargeBonusTier{},
		RechargeBonusMode:  service.RechargeBonusModeBonus,
	}
	if !h.configService.IsPaymentEnabled(ctx) {
		response.Success(c, result)
		return
	}
	cfg, err := h.configService.GetPaymentConfig(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !cfg.Enabled {
		response.Success(c, result)
		return
	}
	plans, err := h.configService.ListPublicPlansForSale(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	limits, err := h.configService.GetAvailableMethodLimits(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	for _, method := range limits.Methods {
		if method.Currency == payment.DefaultPaymentCurrency {
			result.HasCNYPaymentMethod = true
			break
		}
	}
	groupInfo := h.configService.GetGroupInfoMap(ctx, plans)
	result.Plans = make([]publicSubscriptionPlan, 0, len(plans))
	for _, plan := range plans {
		group := groupInfo[plan.GroupID]
		result.Plans = append(result.Plans, publicSubscriptionPlan{
			ID:              plan.ID,
			Name:            plan.Name,
			Description:     plan.Description,
			GroupPlatform:   group.Platform,
			Price:           plan.Price,
			OriginalPrice:   plan.OriginalPrice,
			Currency:        plan.Currency,
			ValidityDays:    plan.ValidityDays,
			ValidityUnit:    plan.ValidityUnit,
			Features:        parseFeatures(plan.Features),
			DailyLimitUSD:   group.DailyLimitUSD,
			WeeklyLimitUSD:  group.WeeklyLimitUSD,
			MonthlyLimitUSD: group.MonthlyLimitUSD,
			SortOrder:       plan.SortOrder,
		})
	}
	result.SubscriptionUSDToCNYRate = cfg.SubscriptionUSDToCNYRate
	result.RechargeBonusTiers = cfg.RechargeBonusTiers
	result.RechargeBonusMode = cfg.RechargeBonusMode
	response.Success(c, result)
}
