package videoprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseCreateRequestWithCapabilities parses only the JSON contract documented
// for Video Tasks JSON v1. It never changes the legacy Seedance/FFLink parser.
func ParseCreateRequestWithCapabilities(body []byte, capabilities []VideoModelCapability) (*CreateRequest, RequestInfo, error) {
	if err := ValidateVideoModelCapabilities(capabilities); err != nil {
		return nil, RequestInfo{}, err
	}
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return nil, RequestInfo{}, fmt.Errorf("request body must be a JSON object")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, RequestInfo{}, fmt.Errorf("request body must contain one JSON object")
	}
	allowed := strings.Fields("model prompt duration resolution size aspect_ratio ratio audio generate_audio start_frame_url end_frame_url image_url first_image last_image first_image_url last_image_url referenceImages referenceVideos referenceAudios reference_images reference_image_urls reference_videos reference_audios reference_video_durations n")
	for key := range raw {
		if !containsFoldExact(allowed, key) {
			return nil, RequestInfo{}, fmt.Errorf("unsupported video field %q", key)
		}
	}
	model, err := strictVideoText(raw, "model")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	capability, known := LookupVideoModelCapability(model, capabilities)
	if !known || !capability.Enabled {
		return nil, RequestInfo{}, fmt.Errorf("video model %q has no enabled capability", model)
	}
	if err := ValidateVideoModelCapabilities([]VideoModelCapability{capability}); err != nil {
		return nil, RequestInfo{}, err
	}
	prompt, err := strictVideoText(raw, "prompt")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	if prompt == "" {
		return nil, RequestInfo{}, fmt.Errorf("prompt is required")
	}
	request := &CreateRequest{RequestedModel: model, Model: capability.Model, Prompt: prompt, Duration: capability.DefaultDuration, Resolution: strings.ToLower(capability.DefaultResolution), AspectRatio: capability.DefaultRatio, Capability: &capability, Raw: raw}
	if value, exists := raw["duration"]; exists {
		request.Duration, err = strictVideoInteger(value, "duration")
		if err != nil {
			return nil, RequestInfo{}, err
		}
		if capability.DurationMode == VideoDurationUnknown {
			return nil, RequestInfo{}, fmt.Errorf("duration is not configurable for %s", model)
		}
	}
	if value, err := strictVideoText(raw, "resolution", "size"); err != nil {
		return nil, RequestInfo{}, err
	} else if value != "" {
		request.Resolution = strings.ToLower(value)
	}
	if value, err := strictVideoText(raw, "aspect_ratio", "ratio"); err != nil {
		return nil, RequestInfo{}, err
	} else if value != "" {
		request.AspectRatio = value
	}
	request.Audio, err = strictVideoBoolean(raw, "audio", "generate_audio")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	request.StartFrameURL, err = strictVideoText(raw, "start_frame_url", "image_url", "first_image", "first_image_url")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	request.EndFrameURL, err = strictVideoText(raw, "end_frame_url", "last_image", "last_image_url")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	request.ReferenceImages, err = strictVideoStrings(raw, "referenceImages", "reference_images", "reference_image_urls")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	request.ReferenceVideos, err = strictVideoStrings(raw, "referenceVideos", "reference_videos")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	request.ReferenceAudios, err = strictVideoStrings(raw, "referenceAudios", "reference_audios")
	if err != nil {
		return nil, RequestInfo{}, err
	}
	if value, exists := raw["n"]; exists {
		n, err := strictVideoInteger(value, "n")
		if err != nil || n != 1 {
			return nil, RequestInfo{}, fmt.Errorf("n must be 1")
		}
	}
	if value, exists := raw["reference_video_durations"]; exists {
		items, ok := value.([]any)
		if !ok {
			return nil, RequestInfo{}, fmt.Errorf("reference_video_durations must be an integer array")
		}
		for _, item := range items {
			seconds, err := strictVideoInteger(item, "reference_video_durations")
			if err != nil || seconds <= 0 {
				return nil, RequestInfo{}, fmt.Errorf("reference video durations must be positive integer seconds")
			}
			request.ReferenceVideoDurations = append(request.ReferenceVideoDurations, seconds)
		}
	}
	if err := validateVideoCapabilityRequest(request, capability); err != nil {
		return nil, RequestInfo{}, err
	}
	return request, RequestInfo{Model: capability.Model, RequestedModel: model, DurationSeconds: request.Duration, Resolution: request.Resolution, ReferenceVideoCount: len(request.ReferenceVideos), Audio: request.Audio, Capability: &capability, BillingUnit: capability.BillingUnit}, nil
}

func containsFoldExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func strictVideoInteger(value any, name string) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	parsed, err := strconv.ParseInt(string(number), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return int(parsed), nil
}

func strictVideoText(raw map[string]any, names ...string) (string, error) {
	var result string
	found := false
	for _, name := range names {
		value, exists := raw[name]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", name)
		}
		text = strings.TrimSpace(text)
		if found && result != text {
			return "", fmt.Errorf("conflicting aliases for %s", names[0])
		}
		result, found = text, true
	}
	return result, nil
}

func strictVideoBoolean(raw map[string]any, names ...string) (bool, error) {
	var result bool
	found := false
	for _, name := range names {
		value, exists := raw[name]
		if !exists {
			continue
		}
		flag, ok := value.(bool)
		if !ok {
			return false, fmt.Errorf("%s must be a boolean", name)
		}
		if found && result != flag {
			return false, fmt.Errorf("conflicting aliases for %s", names[0])
		}
		result, found = flag, true
	}
	return result, nil
}

func strictVideoStrings(raw map[string]any, names ...string) ([]string, error) {
	var result []string
	found := false
	for _, name := range names {
		value, exists := raw[name]
		if !exists {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be a string array", name)
		}
		list := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s must contain nonempty strings", name)
			}
			list = append(list, strings.TrimSpace(text))
		}
		if found && strings.Join(result, "\x00") != strings.Join(list, "\x00") {
			return nil, fmt.Errorf("conflicting aliases for %s", names[0])
		}
		result, found = list, true
	}
	return result, nil
}

func validateVideoCapabilityRequest(r *CreateRequest, c VideoModelCapability) error {
	if r == nil || !c.Enabled || !strings.EqualFold(r.Model, c.Model) || strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("video request does not match an enabled model capability")
	}
	if c.DurationMode == VideoDurationUnknown {
		if r.Duration != 0 {
			return fmt.Errorf("duration is not configurable for %s", c.Model)
		}
	} else if r.Duration < c.MinDuration || r.Duration > c.MaxDuration || len(c.AllowedDurations) > 0 && !containsInt(c.AllowedDurations, r.Duration) {
		return fmt.Errorf("duration is not supported by %s", c.Model)
	}
	if len(c.Resolutions) == 0 {
		if r.Resolution != "" {
			return fmt.Errorf("resolution is not configurable for %s", c.Model)
		}
	} else if !containsFold(c.Resolutions, r.Resolution) || !c.ResolutionControl && !strings.EqualFold(c.DefaultResolution, r.Resolution) {
		return fmt.Errorf("resolution is not supported by %s", c.Model)
	}
	if len(c.Ratios) == 0 {
		if r.AspectRatio != "" {
			return fmt.Errorf("aspect ratio is not configurable for %s", c.Model)
		}
	} else if !containsFold(c.Ratios, r.AspectRatio) {
		return fmt.Errorf("aspect ratio is not supported by %s", c.Model)
	}
	if r.Audio && !c.GenerateAudio {
		return fmt.Errorf("generated audio is not supported by %s", c.Model)
	}
	frames := 0
	if r.StartFrameURL != "" {
		frames++
	}
	if r.EndFrameURL != "" {
		frames++
	}
	if frames > 0 {
		if !c.Frames || r.StartFrameURL == "" || r.EndFrameURL == "" && !c.AllowFirstOnly {
			return fmt.Errorf("unsupported first/last frame combination for %s", c.Model)
		}
		if !c.AllowFramesWithReferences && len(r.ReferenceImages)+len(r.ReferenceVideos)+len(r.ReferenceAudios) > 0 {
			return fmt.Errorf("frames cannot be combined with reference assets for %s", c.Model)
		}
		if c.FramesRequireAutoRatio && !strings.EqualFold(r.AspectRatio, "auto") {
			return fmt.Errorf("frames require auto aspect ratio for %s", c.Model)
		}
	}
	maxImages := c.MaxImages
	for resolution, limit := range c.MaxImagesByResolution {
		if strings.EqualFold(resolution, r.Resolution) {
			maxImages = limit
		}
	}
	if len(r.ReferenceImages) > maxImages || len(r.ReferenceVideos) > c.MaxVideos || len(r.ReferenceAudios) > c.MaxAudios {
		return fmt.Errorf("reference asset count exceeds the %s model limit", c.Model)
	}
	if c.MaxTotalAssets > 0 && frames+len(r.ReferenceImages)+len(r.ReferenceVideos)+len(r.ReferenceAudios) > c.MaxTotalAssets {
		return fmt.Errorf("total reference asset count exceeds the %s model limit", c.Model)
	}
	if c.AudioRequiresImage && len(r.ReferenceAudios) > 0 && len(r.ReferenceImages) == 0 {
		return fmt.Errorf("reference audio requires an image for %s", c.Model)
	}
	for _, combination := range c.UnverifiedCombinations {
		if combination == "reference_videos" && len(r.ReferenceVideos) > 0 {
			return fmt.Errorf("reference videos are disabled for %s until their duration wire contract is verified", c.Model)
		}
	}
	if len(r.ReferenceVideoDurations) > 0 && len(r.ReferenceVideoDurations) != len(r.ReferenceVideos) {
		return fmt.Errorf("reference_video_durations must match reference videos")
	}
	if c.RequireReferenceVideoDurations && len(r.ReferenceVideoDurations) != len(r.ReferenceVideos) {
		return fmt.Errorf("reference_video_durations are required for %s", c.Model)
	}
	seconds := 0
	for _, duration := range r.ReferenceVideoDurations {
		if duration <= 0 || duration > 3600 {
			return fmt.Errorf("reference video duration is invalid")
		}
		seconds += duration
	}
	if c.MaxReferenceVideoSeconds > 0 && seconds > c.MaxReferenceVideoSeconds {
		return fmt.Errorf("reference video duration exceeds the model limit")
	}
	if c.MaxCombinedVideoSeconds > 0 && seconds+r.Duration > c.MaxCombinedVideoSeconds {
		return fmt.Errorf("combined output and reference video duration exceeds the model limit")
	}
	for _, rawURL := range r.referenceURLs() {
		if err := validatePublicHTTPSURL(rawURL); err != nil {
			return err
		}
	}
	return nil
}
