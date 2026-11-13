package videoprovider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoCapabilityLimitsIntersectWithoutMutatingUpstream(t *testing.T) {
	c, ok := LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	c.ResolutionControl = true
	c.Resolutions, c.DefaultResolution = []string{"720p", "1080p"}, "720p"
	c.MinDuration, c.MaxDuration, c.DefaultDuration = 4, 15, 10
	c.AllowedDurations = []int{5, 10, 15}
	c.MaxImages = 3
	c.MaxImagesByResolution = map[string]int{"720p": 3, "1080p": 1}
	maxDuration, maxImages := 12, 2
	limits := &VideoCapabilityLimits{MaxDuration: &maxDuration, AllowedDurations: []int{5, 8, 10}, Resolutions: []string{"1080p"}, MaxImages: &maxImages}
	effective, err := limits.Apply(c)
	require.NoError(t, err)
	require.Equal(t, []int{5, 10}, effective.AllowedDurations)
	require.Equal(t, "1080p", effective.DefaultResolution)
	require.Equal(t, map[string]int{"1080p": 1}, effective.MaxImagesByResolution)
	require.Equal(t, 15, c.MaxDuration)
	require.Len(t, c.MaxImagesByResolution, 2)
	unchanged, err := (*VideoCapabilityLimits)(nil).Apply(c)
	require.NoError(t, err)
	require.Equal(t, c, unchanged)
}

func TestVideoCapabilityLimitsCannotInventOptionsOrFeatures(t *testing.T) {
	c, ok := LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	c.SupportsCancel = false
	yes, zero := true, 0
	for _, limits := range []*VideoCapabilityLimits{
		{Resolutions: []string{"not-supported"}}, {SupportsCancel: &yes},
		{MinDuration: &zero}, {AllowedDurations: []int{5, 5}},
		{Resolutions: []string{"720p"}, DefaultResolution: "1080p"},
	} {
		_, err := limits.Apply(c)
		require.Error(t, err)
	}
	maxImages := 100
	effective, err := (&VideoCapabilityLimits{MaxImages: &maxImages}).Apply(c)
	require.NoError(t, err)
	require.Equal(t, c.MaxImages, effective.MaxImages)
}

func TestVideoCapabilityLimitsBoundUnboundedAssetsAndReferenceDurations(t *testing.T) {
	c, ok := LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	c.MaxTotalAssets, c.MaxVideos = 0, 2
	total, seconds := 2, 8
	effective, err := (&VideoCapabilityLimits{MaxTotalAssets: &total, MaxReferenceVideoSeconds: &seconds}).Apply(c)
	require.NoError(t, err)
	require.Equal(t, 2, effective.MaxTotalAssets)
	require.True(t, effective.RequireReferenceVideoDurations)
}
