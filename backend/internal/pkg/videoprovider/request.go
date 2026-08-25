package videoprovider

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var supportedAspectRatios = map[string]struct{}{
	"21:9": {}, "16:9": {}, "4:3": {}, "1:1": {}, "3:4": {}, "9:16": {}, "auto": {},
}

func ParseCreateRequest(body []byte) (*CreateRequest, RequestInfo, error) {
	var raw map[string]any
	if len(body) == 0 || json.Unmarshal(body, &raw) != nil || raw == nil {
		return nil, RequestInfo{}, fmt.Errorf("request body must be valid JSON")
	}
	requestedModel := textField(raw, "model")
	if requestedModel == "" {
		return nil, RequestInfo{}, fmt.Errorf("model is required")
	}
	model, known := CanonicalModel(requestedModel)
	if !known {
		model = strings.ToLower(requestedModel)
	}
	prompt := textField(raw, "prompt")
	if prompt == "" {
		return nil, RequestInfo{}, fmt.Errorf("prompt is required")
	}
	duration := intField(raw, "duration")
	resolution := canonicalResolution(textField(raw, "resolution"))
	if spec, ok := LookupModel(model); ok {
		if duration == 0 {
			duration = spec.MinDuration
		}
		if resolution == "" {
			resolution = spec.Resolutions[0]
		}
		if duration < spec.MinDuration || duration > spec.MaxDuration {
			return nil, RequestInfo{}, fmt.Errorf("duration for %s must be between %d and %d seconds", spec.ID, spec.MinDuration, spec.MaxDuration)
		}
		if !containsFold(spec.Resolutions, resolution) {
			return nil, RequestInfo{}, fmt.Errorf("resolution %q is not supported by %s", resolution, spec.ID)
		}
	} else if duration <= 0 || resolution == "" {
		return nil, RequestInfo{}, fmt.Errorf("duration and resolution are required for unregistered video model %q", requestedModel)
	}

	aspectRatio := firstTextField(raw, "aspect_ratio", "ratio")
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}
	if _, ok := supportedAspectRatios[strings.ToLower(aspectRatio)]; !ok {
		return nil, RequestInfo{}, fmt.Errorf("aspect ratio %q is not supported", aspectRatio)
	}
	if strings.EqualFold(aspectRatio, "auto") {
		aspectRatio = "auto"
	}

	request := &CreateRequest{
		RequestedModel:  requestedModel,
		Model:           model,
		Prompt:          prompt,
		AspectRatio:     aspectRatio,
		Duration:        duration,
		Resolution:      resolution,
		Audio:           boolField(raw, "audio"),
		StartFrameURL:   firstTextField(raw, "start_frame_url", "image_url", "first_image"),
		EndFrameURL:     firstTextField(raw, "end_frame_url", "last_image"),
		ReferenceImages: stringSliceField(raw, "referenceImages"),
		ReferenceVideos: stringSliceField(raw, "referenceVideos"),
		ReferenceAudios: stringSliceField(raw, "referenceAudios"),
		Raw:             raw,
	}
	if guidances, ok := raw["guidances"].(map[string]any); ok {
		request.Guidances = guidances
		request.ReferenceImages = append(request.ReferenceImages, guidanceURLs(guidances, "image_reference", "image")...)
		request.ReferenceVideos = append(request.ReferenceVideos, guidanceURLs(guidances, "video_reference_base", "video")...)
		request.ReferenceAudios = append(request.ReferenceAudios, guidanceURLs(guidances, "audio_reference", "audio")...)
	}
	for _, rawURL := range request.referenceURLs() {
		if err := validatePublicHTTPSURL(rawURL); err != nil {
			return nil, RequestInfo{}, err
		}
	}
	return request, RequestInfo{
		Model: model, RequestedModel: requestedModel, DurationSeconds: duration,
		Resolution: resolution, ReferenceVideoCount: len(request.ReferenceVideos), Audio: request.Audio,
	}, nil
}

func (r *CreateRequest) referenceURLs() []string {
	urls := make([]string, 0, 2+len(r.ReferenceImages)+len(r.ReferenceVideos)+len(r.ReferenceAudios))
	if r.StartFrameURL != "" {
		urls = append(urls, r.StartFrameURL)
	}
	if r.EndFrameURL != "" {
		urls = append(urls, r.EndFrameURL)
	}
	urls = append(urls, r.ReferenceImages...)
	urls = append(urls, r.ReferenceVideos...)
	urls = append(urls, r.ReferenceAudios...)
	return urls
}

func validateKnownModelRequest(request *CreateRequest, supported map[string]string) (ModelSpec, string, error) {
	if request == nil {
		return ModelSpec{}, "", fmt.Errorf("video request is nil")
	}
	canonical, ok := CanonicalModel(request.Model)
	if !ok {
		return ModelSpec{}, "", fmt.Errorf("unsupported video model %q", request.RequestedModel)
	}
	upstreamModel, ok := supported[canonical]
	if !ok {
		return ModelSpec{}, "", fmt.Errorf("video model %q is not supported by this provider", request.RequestedModel)
	}
	spec, _ := LookupModel(canonical)
	if request.Duration < spec.MinDuration || request.Duration > spec.MaxDuration {
		return ModelSpec{}, "", fmt.Errorf("duration for %s must be between %d and %d seconds", canonical, spec.MinDuration, spec.MaxDuration)
	}
	if !containsFold(spec.Resolutions, request.Resolution) {
		return ModelSpec{}, "", fmt.Errorf("resolution %q is not supported by %s", request.Resolution, canonical)
	}
	return spec, upstreamModel, nil
}

func canonicalResolution(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validatePublicHTTPSURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return fmt.Errorf("reference asset URLs must be public HTTPS URLs")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("reference asset URLs must not target local hosts")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("reference asset URLs must not target private IP addresses")
	}
	return nil
}

func textField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func firstTextField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := textField(values, key); value != "" {
			return value
		}
	}
	return ""
}

func intField(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func boolField(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func stringSliceField(values map[string]any, key string) []string {
	items, _ := values[key].([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func guidanceURLs(guidances map[string]any, collection, mediaKey string) []string {
	items, _ := guidances[collection].([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		entry, _ := item.(map[string]any)
		media, _ := entry[mediaKey].(map[string]any)
		if value := textField(media, "url"); value != "" {
			result = append(result, value)
		}
	}
	return result
}
