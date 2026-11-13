package videoprovider

import (
	"fmt"
	"slices"
	"strings"
)

// VideoCapabilityLimits narrows a public offering without changing provider or
// metering contracts. Nil fields inherit upstream values; zero disables assets.
type VideoCapabilityLimits struct {
	MinDuration               *int     `json:"min_duration,omitempty"`
	MaxDuration               *int     `json:"max_duration,omitempty"`
	DefaultDuration           *int     `json:"default_duration,omitempty"`
	AllowedDurations          []int    `json:"allowed_durations,omitempty"`
	Resolutions               []string `json:"resolutions,omitempty"`
	DefaultResolution         string   `json:"default_resolution,omitempty"`
	Ratios                    []string `json:"ratios,omitempty"`
	DefaultRatio              string   `json:"default_ratio,omitempty"`
	MaxImages                 *int     `json:"max_images,omitempty"`
	MaxVideos                 *int     `json:"max_videos,omitempty"`
	MaxAudios                 *int     `json:"max_audios,omitempty"`
	MaxTotalAssets            *int     `json:"max_total_assets,omitempty"`
	MaxReferenceVideoSeconds  *int     `json:"max_reference_video_seconds,omitempty"`
	MaxCombinedVideoSeconds   *int     `json:"max_combined_video_seconds,omitempty"`
	Frames                    *bool    `json:"frames,omitempty"`
	AllowFirstOnly            *bool    `json:"allow_first_only,omitempty"`
	AllowFramesWithReferences *bool    `json:"allow_frames_with_references,omitempty"`
	GenerateAudio             *bool    `json:"generate_audio,omitempty"`
	SupportsCancel            *bool    `json:"supports_cancel,omitempty"`
}

func (l *VideoCapabilityLimits) Validate() error {
	if l == nil {
		return nil
	}
	for _, n := range []*int{l.MinDuration, l.MaxDuration, l.DefaultDuration, l.MaxImages, l.MaxVideos, l.MaxAudios, l.MaxTotalAssets, l.MaxReferenceVideoSeconds, l.MaxCombinedVideoSeconds} {
		if n != nil && (*n < 0 || *n > 3600) {
			return fmt.Errorf("video capability limits must be integers between 0 and 3600")
		}
	}
	for _, n := range []*int{l.MinDuration, l.MaxDuration, l.DefaultDuration} {
		if n != nil && *n == 0 {
			return fmt.Errorf("video duration limits must be positive")
		}
	}
	for _, n := range []*int{l.MaxReferenceVideoSeconds, l.MaxCombinedVideoSeconds} {
		if n != nil && *n == 0 {
			return fmt.Errorf("video duration caps must be positive")
		}
	}
	if l.MaxTotalAssets != nil && *l.MaxTotalAssets == 0 {
		return fmt.Errorf("total asset limit must be positive; disable individual asset types instead")
	}
	if l.MinDuration != nil && l.MaxDuration != nil && *l.MinDuration > *l.MaxDuration {
		return fmt.Errorf("video duration bounds are reversed")
	}
	if l.DefaultDuration != nil && ((l.MinDuration != nil && *l.DefaultDuration < *l.MinDuration) || (l.MaxDuration != nil && *l.DefaultDuration > *l.MaxDuration) || (len(l.AllowedDurations) > 0 && !slices.Contains(l.AllowedDurations, *l.DefaultDuration))) {
		return fmt.Errorf("video default duration is outside configured limits")
	}
	seen := map[int]bool{}
	for _, n := range l.AllowedDurations {
		if n <= 0 || n > 3600 || seen[n] || (l.MinDuration != nil && n < *l.MinDuration) || (l.MaxDuration != nil && n > *l.MaxDuration) {
			return fmt.Errorf("invalid allowed video durations")
		}
		seen[n] = true
	}
	for _, values := range [][]string{l.Resolutions, l.Ratios} {
		seen := map[string]bool{}
		for _, value := range values {
			key := strings.ToLower(value)
			if value == "" || value != strings.TrimSpace(value) || len(value) > 32 || strings.ContainsAny(value, "\r\n\x00") || seen[key] {
				return fmt.Errorf("invalid video parameter options")
			}
			seen[key] = true
		}
	}
	if l.DefaultResolution != "" && len(l.Resolutions) > 0 && !containsFold(l.Resolutions, l.DefaultResolution) {
		return fmt.Errorf("video default resolution is not allowed")
	}
	if l.DefaultRatio != "" && len(l.Ratios) > 0 && !containsFold(l.Ratios, l.DefaultRatio) {
		return fmt.Errorf("video default ratio is not allowed")
	}
	return nil
}

// Apply preserves one complete supplier contract. Empty intersections remove
// that variant; they must never fall back to unrestricted upstream values.
func (l *VideoCapabilityLimits) Apply(upstream VideoModelCapability) (VideoModelCapability, error) {
	c := cloneVideoCapability(upstream)
	if l == nil {
		return c, nil
	}
	if err := l.Validate(); err != nil {
		return c, err
	}
	if c.DurationMode == VideoDurationUnknown {
		if l.MinDuration != nil || l.MaxDuration != nil || l.DefaultDuration != nil || len(l.AllowedDurations) > 0 {
			return c, fmt.Errorf("duration is not configurable")
		}
	} else {
		if l.MinDuration != nil {
			c.MinDuration = max(c.MinDuration, *l.MinDuration)
		}
		if l.MaxDuration != nil {
			c.MaxDuration = min(c.MaxDuration, *l.MaxDuration)
		}
		if c.MinDuration > c.MaxDuration {
			return c, fmt.Errorf("no supported video duration")
		}
		allowed := c.AllowedDurations
		if len(l.AllowedDurations) > 0 {
			allowed = l.AllowedDurations
		}
		c.AllowedDurations = nil
		for _, n := range allowed {
			if n >= c.MinDuration && n <= c.MaxDuration && (len(upstream.AllowedDurations) == 0 || slices.Contains(upstream.AllowedDurations, n)) {
				c.AllowedDurations = append(c.AllowedDurations, n)
			}
		}
		if len(allowed) > 0 && len(c.AllowedDurations) == 0 {
			return c, fmt.Errorf("no supported video duration")
		}
		c.DefaultDuration = max(c.MinDuration, min(c.MaxDuration, c.DefaultDuration))
		if len(c.AllowedDurations) > 0 && !slices.Contains(c.AllowedDurations, c.DefaultDuration) {
			c.DefaultDuration = c.AllowedDurations[0]
		}
		if l.DefaultDuration != nil {
			c.DefaultDuration = *l.DefaultDuration
		}
	}
	var err error
	c.Resolutions, c.DefaultResolution, err = intersectVideoOptions(c.Resolutions, c.DefaultResolution, l.Resolutions, l.DefaultResolution)
	if err != nil {
		return c, err
	}
	if !c.ResolutionControl && !strings.EqualFold(c.DefaultResolution, upstream.DefaultResolution) {
		return c, fmt.Errorf("resolution is fixed by upstream")
	}
	c.Ratios, c.DefaultRatio, err = intersectVideoOptions(c.Ratios, c.DefaultRatio, l.Ratios, l.DefaultRatio)
	if err != nil {
		return c, err
	}
	for _, pair := range []struct {
		value     *int
		limit     *int
		unbounded bool
	}{
		{&c.MaxImages, l.MaxImages, false}, {&c.MaxVideos, l.MaxVideos, false}, {&c.MaxAudios, l.MaxAudios, false}, {&c.MaxTotalAssets, l.MaxTotalAssets, true},
		{&c.MaxReferenceVideoSeconds, l.MaxReferenceVideoSeconds, true}, {&c.MaxCombinedVideoSeconds, l.MaxCombinedVideoSeconds, true},
	} {
		if pair.limit != nil {
			if pair.unbounded && *pair.limit == 0 {
				return c, fmt.Errorf("video duration caps must be positive")
			}
			if pair.unbounded && *pair.value == 0 {
				*pair.value = *pair.limit
			} else {
				*pair.value = min(*pair.value, *pair.limit)
			}
		}
	}
	for resolution, count := range c.MaxImagesByResolution {
		if !containsFold(c.Resolutions, resolution) {
			delete(c.MaxImagesByResolution, resolution)
		} else {
			c.MaxImagesByResolution[resolution] = min(count, c.MaxImages)
		}
	}
	if c.MaxVideos > 0 && (l.MaxReferenceVideoSeconds != nil || l.MaxCombinedVideoSeconds != nil) {
		c.RequireReferenceVideoDurations = true
	}
	for _, pair := range []struct {
		value *bool
		limit *bool
	}{
		{&c.Frames, l.Frames}, {&c.AllowFirstOnly, l.AllowFirstOnly}, {&c.AllowFramesWithReferences, l.AllowFramesWithReferences}, {&c.GenerateAudio, l.GenerateAudio}, {&c.SupportsCancel, l.SupportsCancel},
	} {
		if pair.limit != nil {
			if *pair.limit && !*pair.value {
				return c, fmt.Errorf("required video feature is unsupported")
			}
			*pair.value = *pair.value && *pair.limit
		}
	}
	if c.FramesRequireAutoRatio && !containsFold(c.Ratios, "auto") {
		c.Frames, c.AllowFirstOnly = false, false
	}
	if !c.Frames {
		c.AllowFirstOnly, c.AllowFramesWithReferences, c.FramesRequireAutoRatio = false, false, false
	}
	if err := validateVideoModelCapability(c); err != nil {
		return c, err
	}
	return c, nil
}

func intersectVideoOptions(upstream []string, defaultValue string, limits []string, requestedDefault string) ([]string, string, error) {
	values := slices.Clone(upstream)
	if len(limits) > 0 {
		values = nil
		for _, value := range limits {
			if containsFold(upstream, value) {
				values = append(values, value)
			}
		}
		if len(values) == 0 {
			return nil, "", fmt.Errorf("no supported video parameter option")
		}
	}
	if len(values) > 0 && !containsFold(values, defaultValue) {
		defaultValue = values[0]
	}
	if requestedDefault != "" {
		if !containsFold(values, requestedDefault) {
			return nil, "", fmt.Errorf("video default is unsupported")
		}
		defaultValue = requestedDefault
	}
	return values, defaultValue, nil
}
