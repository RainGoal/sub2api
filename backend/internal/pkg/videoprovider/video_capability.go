package videoprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type VideoFieldProfile string

const (
	ProfileBBLabuOfficial                  VideoFieldProfile = "bblabu_official"
	ProfileBBLabuRoute                     VideoFieldProfile = "bblabu_route"
	ProfileZhiqiV1                         VideoFieldProfile = "zhiqi_v1"
	VideoBillingPerSecond                                    = "per_second"
	VideoBillingPerVideo                                     = "per_video"
	VideoDurationControlled                                  = "controlled"
	VideoDurationFixed                                       = "fixed"
	VideoDurationUnknown                                     = "unknown"
	VideoMeteringOutputSeconds                               = "output_seconds"
	VideoMeteringOutputAndReferenceSeconds                   = "output_and_reference_seconds"
	VideoMeteringPerVideo                                    = "per_video"
)

// VideoModelCapability is a versionable request and metering contract. It is
// stored with a task so later account edits cannot reinterpret an active job.
type VideoModelCapability struct {
	Model                          string            `json:"model"`
	UpstreamModel                  string            `json:"upstream_model,omitempty"`
	Profile                        VideoFieldProfile `json:"profile"`
	Enabled                        bool              `json:"enabled"`
	DurationMode                   string            `json:"duration_mode"`
	MinDuration                    int               `json:"min_duration"`
	MaxDuration                    int               `json:"max_duration"`
	DefaultDuration                int               `json:"default_duration"`
	AllowedDurations               []int             `json:"allowed_durations,omitempty"`
	Resolutions                    []string          `json:"resolutions"`
	DefaultResolution              string            `json:"default_resolution"`
	ResolutionControl              bool              `json:"resolution_control"`
	Ratios                         []string          `json:"ratios"`
	DefaultRatio                   string            `json:"default_ratio"`
	MaxImages                      int               `json:"max_images"`
	MaxImagesByResolution          map[string]int    `json:"max_images_by_resolution,omitempty"`
	MaxVideos                      int               `json:"max_videos"`
	MaxAudios                      int               `json:"max_audios"`
	MaxTotalAssets                 int               `json:"max_total_assets"`
	Frames                         bool              `json:"frames"`
	AllowFirstOnly                 bool              `json:"allow_first_only"`
	AllowFramesWithReferences      bool              `json:"allow_frames_with_references"`
	GenerateAudio                  bool              `json:"generate_audio"`
	AudioRequiresImage             bool              `json:"audio_requires_image"`
	FramesRequireAutoRatio         bool              `json:"frames_require_auto_ratio"`
	BillingUnit                    string            `json:"billing_unit"`
	BillingPolicy                  BillingPolicy     `json:"billing_policy"`
	MeteringPolicy                 string            `json:"metering_policy"`
	MeteringTrusted                bool              `json:"metering_trusted"`
	MaxReferenceVideoSeconds       int               `json:"max_reference_video_seconds"`
	RequireReferenceVideoDurations bool              `json:"require_reference_video_durations"`
	MaxCombinedVideoSeconds        int               `json:"max_combined_video_seconds"`
	SupportsCancel                 bool              `json:"supports_cancel"`
	ContentMode                    string            `json:"content_mode"`
	// These combinations lack a documented wire contract and stay disabled.
	UnverifiedCombinations []string `json:"unverified_combinations,omitempty"`
}

// ParseVideoModelCapabilities accepts account extra JSON without requiring
// callers to reproduce this contract. Unknown keys are rejected as typos.
func ParseVideoModelCapabilities(raw any) ([]VideoModelCapability, error) {
	if raw == nil {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode video model capabilities: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("video model capabilities exceed 1 MiB")
	}
	var capabilities []VideoModelCapability
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&capabilities); err != nil {
		return nil, fmt.Errorf("invalid video model capabilities: %w", err)
	}
	if err := ValidateVideoModelCapabilities(capabilities); err != nil {
		return nil, err
	}
	return capabilities, nil
}

func ValidateVideoModelCapabilities(capabilities []VideoModelCapability) error {
	if len(capabilities) > 512 {
		return fmt.Errorf("at most 512 video model capabilities are allowed")
	}
	seen := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		key := strings.ToLower(strings.TrimSpace(capability.Model))
		if key == "" || len(capability.Model) > 128 || capability.Model != strings.TrimSpace(capability.Model) || strings.ContainsAny(key, "\r\n\x00") {
			return fmt.Errorf("invalid video capability model")
		}
		if seen[key] {
			return fmt.Errorf("duplicate video capability model %q", capability.Model)
		}
		seen[key] = true
		if capability.UpstreamModel != "" && (len(capability.UpstreamModel) > 128 || capability.UpstreamModel != strings.TrimSpace(capability.UpstreamModel) || strings.ContainsAny(capability.UpstreamModel, "\r\n\x00")) {
			return fmt.Errorf("invalid video capability upstream model")
		}
		if err := validateVideoModelCapability(capability); err != nil {
			return fmt.Errorf("video model %q: %w", capability.Model, err)
		}
	}
	return nil
}

func validateVideoModelCapability(c VideoModelCapability) error {
	if c.Profile != ProfileBBLabuOfficial && c.Profile != ProfileBBLabuRoute && c.Profile != ProfileZhiqiV1 {
		return fmt.Errorf("unsupported field profile %q", c.Profile)
	}
	switch c.DurationMode {
	case VideoDurationControlled, VideoDurationFixed:
		if c.MinDuration <= 0 || c.MaxDuration < c.MinDuration || c.MaxDuration > 3600 || c.DefaultDuration < c.MinDuration || c.DefaultDuration > c.MaxDuration {
			return fmt.Errorf("invalid duration bounds or default")
		}
		if c.DurationMode == VideoDurationFixed && c.MinDuration != c.MaxDuration {
			return fmt.Errorf("fixed duration must have equal bounds")
		}
	case VideoDurationUnknown:
		if c.MinDuration != 0 || c.MaxDuration != 0 || c.DefaultDuration != 0 || len(c.AllowedDurations) != 0 {
			return fmt.Errorf("unknown duration cannot declare numeric defaults")
		}
	default:
		return fmt.Errorf("unsupported duration mode %q", c.DurationMode)
	}
	for _, duration := range c.AllowedDurations {
		if duration < c.MinDuration || duration > c.MaxDuration {
			return fmt.Errorf("allowed duration is outside bounds")
		}
	}
	if len(c.AllowedDurations) > 0 && !containsInt(c.AllowedDurations, c.DefaultDuration) {
		return fmt.Errorf("default duration is not allowed")
	}
	if len(c.Resolutions) > 0 && !containsFold(c.Resolutions, c.DefaultResolution) {
		return fmt.Errorf("default resolution is not allowed")
	}
	if c.ResolutionControl && len(c.Resolutions) == 0 {
		return fmt.Errorf("resolution control requires allowed resolutions")
	}
	if len(c.Ratios) > 0 && !containsFold(c.Ratios, c.DefaultRatio) {
		return fmt.Errorf("default ratio is not allowed")
	}
	return validateVideoCapabilityMetering(c)
}

func validateVideoCapabilityMetering(c VideoModelCapability) error {
	for _, values := range [][]string{c.Resolutions, c.Ratios} {
		seen := make(map[string]bool, len(values))
		for _, value := range values {
			key := strings.ToLower(strings.TrimSpace(value))
			if key == "" || value != strings.TrimSpace(value) || len(value) > 32 || seen[key] {
				return fmt.Errorf("invalid or duplicate capability option")
			}
			seen[key] = true
		}
	}
	for resolution, limit := range c.MaxImagesByResolution {
		if !containsFold(c.Resolutions, resolution) || limit < 0 || limit > c.MaxImages {
			return fmt.Errorf("invalid resolution-specific image limit")
		}
	}
	for _, limit := range []int{c.MaxImages, c.MaxVideos, c.MaxAudios, c.MaxTotalAssets, c.MaxReferenceVideoSeconds, c.MaxCombinedVideoSeconds} {
		if limit < 0 || limit > 3600 {
			return fmt.Errorf("invalid asset or duration limit")
		}
	}
	if c.AllowFirstOnly && !c.Frames || c.AllowFramesWithReferences && !c.Frames || c.FramesRequireAutoRatio && !c.Frames {
		return fmt.Errorf("frame options require frame support")
	}
	if c.FramesRequireAutoRatio && !containsFold(c.Ratios, "auto") {
		return fmt.Errorf("frame auto ratio is not allowed")
	}
	if c.BillingPolicy != BillingOutputOnly && c.BillingPolicy != BillingOutputAndReference {
		return fmt.Errorf("unsupported billing policy %q", c.BillingPolicy)
	}
	switch c.BillingUnit {
	case VideoBillingPerVideo:
		if c.MeteringPolicy != VideoMeteringPerVideo || c.BillingPolicy != BillingOutputOnly {
			return fmt.Errorf("per-video billing requires per_video metering")
		}
	case VideoBillingPerSecond:
		if c.DurationMode == VideoDurationUnknown {
			return fmt.Errorf("unknown duration cannot use per-second billing")
		}
		if c.MeteringPolicy != VideoMeteringOutputSeconds && c.MeteringPolicy != VideoMeteringOutputAndReferenceSeconds {
			return fmt.Errorf("unsupported second metering policy")
		}
		if c.MeteringPolicy == VideoMeteringOutputAndReferenceSeconds && (c.MaxReferenceVideoSeconds <= 0 || c.BillingPolicy != BillingOutputAndReference) {
			return fmt.Errorf("reference metering requires a finite input limit and matching billing policy")
		}
		if c.MeteringPolicy == VideoMeteringOutputSeconds && c.BillingPolicy != BillingOutputOnly {
			return fmt.Errorf("output metering requires output-only billing policy")
		}
	default:
		return fmt.Errorf("unsupported billing unit %q", c.BillingUnit)
	}
	if c.ContentMode != "content_endpoint" {
		return fmt.Errorf("unsupported content mode %q", c.ContentMode)
	}
	for _, combination := range c.UnverifiedCombinations {
		if combination != "reference_videos" {
			return fmt.Errorf("unknown unverified combination %q", combination)
		}
	}
	return nil
}

// LookupVideoModelCapability keeps overrides account-local. A disabled override
// suppresses a built-in preset without mutating the shared legacy catalog.
func LookupVideoModelCapability(model string, overrides []VideoModelCapability) (VideoModelCapability, bool) {
	for _, capability := range overrides {
		if strings.EqualFold(capability.Model, strings.TrimSpace(model)) {
			return cloneVideoCapability(capability), true
		}
	}
	for _, capability := range bblabuVideoCapabilities {
		if strings.EqualFold(capability.Model, strings.TrimSpace(model)) {
			return cloneVideoCapability(capability), true
		}
	}
	return VideoModelCapability{}, false
}

func cloneVideoCapability(c VideoModelCapability) VideoModelCapability {
	if c.MaxImagesByResolution != nil {
		limits := make(map[string]int, len(c.MaxImagesByResolution))
		for resolution, limit := range c.MaxImagesByResolution {
			limits[resolution] = limit
		}
		c.MaxImagesByResolution = limits
	}
	c.AllowedDurations = append([]int(nil), c.AllowedDurations...)
	c.Resolutions = append([]string(nil), c.Resolutions...)
	c.Ratios = append([]string(nil), c.Ratios...)
	c.UnverifiedCombinations = append([]string(nil), c.UnverifiedCombinations...)
	return c
}

func BBLabuModelCapabilities() []VideoModelCapability {
	result := make([]VideoModelCapability, 0, len(bblabuVideoCapabilities))
	for _, capability := range bblabuVideoCapabilities {
		result = append(result, cloneVideoCapability(capability))
	}
	return result
}
