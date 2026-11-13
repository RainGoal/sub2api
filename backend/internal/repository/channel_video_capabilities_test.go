package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelVideoCapabilitiesPersistWithPricing(t *testing.T) {
	zero, disabled := 0, false
	price := service.ChannelModelPricing{Platform: service.PlatformSeedance, VideoPricingVersion: 1,
		VideoCapabilities: &videoprovider.VideoCapabilityLimits{MaxVideos: &zero, Frames: &disabled, Resolutions: []string{"720p"}}}
	encoded, err := marshalVideoPricingConfig(&price)
	require.NoError(t, err)
	config, ok := encoded.(string)
	require.True(t, ok)
	var restored service.ChannelModelPricing
	restored.Platform = service.PlatformSeedance
	require.NoError(t, unmarshalVideoPricingConfig(&restored, []byte(config)))
	require.Equal(t, price.VideoCapabilities, restored.VideoCapabilities)
	require.NoError(t, unmarshalVideoPricingConfig(&restored, []byte(`{"version":1,"unit":"per_video"}`)))
	require.Nil(t, restored.VideoCapabilities)
}
