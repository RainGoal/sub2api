//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestWithGatewayTokenRequestPricingMarksOnlyExplicitTokenRequests(t *testing.T) {
	ctx, pricingAt := WithGatewayTokenRequestPricing(context.Background())

	got, ok := gatewayTokenRequestPricingAtFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, pricingAt, got)
	require.Equal(t, pricingAt, GatewayTokenRequestPricingAtFromContext(ctx))
	require.True(t, GatewayTokenRequestPricingAtFromContext(context.Background()).IsZero())
}

func TestAPIKeyFallbackPricingUsesTargetGroupAndOriginalInstant(t *testing.T) {
	primary := &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1, ProfitControlEnabled: true, Hydrated: true}
	backup := &Group{ID: 2, Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 2, ProfitControlEnabled: true, Hydrated: true}
	base := context.WithValue(context.Background(), ctxkey.Group, primary)
	openAI := &OpenAIGatewayService{}
	base, openAIInstant := openAI.WithOpenAIRequestPricingContext(base, &primary.ID)
	base, gatewayInstant := WithGatewayTokenRequestPricing(base)
	oldGate := base.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Equal(t, 1.0, oldGate.threshold)

	ctx := WithAPIKeyFallbackGroup(base, backup)
	require.Same(t, backup, ctx.Value(ctxkey.Group))
	require.Same(t, backup, gatewayTokenRequestBillingGroupFromContext(ctx))
	require.Nil(t, ctx.Value(openAIProfitControlGateCtxKey{}))
	require.Equal(t, openAIInstant, OpenAIPricingAtFromContext(ctx))
	require.Equal(t, gatewayInstant, GatewayTokenRequestPricingAtFromContext(ctx))

	openAIContext := openAI.withOpenAIProfitControlGate(ctx, &backup.ID)
	openAIGate := openAIContext.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Equal(t, 2.0, openAIGate.threshold)
	require.Equal(t, openAIInstant, openAIGate.pricingAt)
	gatewayContext := (&GatewayService{}).withGatewayProfitControlGate(ctx, &backup.ID)
	gatewayGate := gatewayContext.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.Equal(t, 2.0, gatewayGate.threshold)
	require.Equal(t, gatewayInstant, gatewayGate.pricingAt)
	require.Same(t, primary, gatewayTokenRequestBillingGroupFromContext(base))
	require.Same(t, oldGate, base.Value(openAIProfitControlGateCtxKey{}))
}
