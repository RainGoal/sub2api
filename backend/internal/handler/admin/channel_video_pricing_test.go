package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelVideoPricingUnitContractRoundTrip(t *testing.T) {
	var request channelModelPricingRequest
	require.NoError(t, json.Unmarshal([]byte(`{"platform":"seedance","models":["wan-exact"],"billing_mode":"video","video_pricing_version":1,"video_billing_unit":"per_video","video_quantity_policy":"requested_seconds","video_max_billable_seconds":30.5,"per_request_price":0.7}`), &request))
	prices := pricingRequestToService([]channelModelPricingRequest{request}, true)
	require.Len(t, prices, 1)
	require.Equal(t, service.VideoBillingPerVideo, prices[0].VideoBillingUnit)
	require.Equal(t, service.VideoQuantityRequestedSeconds, prices[0].VideoQuantityPolicy)
	response := pricingToResponse(&prices[0])
	require.Equal(t, 1, response.VideoPricingVersion)
	require.Equal(t, service.VideoBillingPerVideo, response.VideoBillingUnit)
	require.Equal(t, 30.5, response.VideoMaxBillableSeconds)
	data, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(data), `"video_billing_unit":"per_video"`)
	legacy, err := json.Marshal(pricingToResponse(&service.ChannelModelPricing{BillingMode: service.BillingModePerRequest}))
	require.NoError(t, err)
	require.NotContains(t, string(legacy), "video_billing_unit")
	require.NotContains(t, string(legacy), "video_pricing_version")
}
