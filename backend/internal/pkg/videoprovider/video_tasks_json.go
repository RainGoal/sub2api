package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type videoTasksJSONDriver struct{}

func (videoTasksJSONDriver) ID() ID                                      { return ProviderVideoTasksJSONV1 }
func (videoTasksJSONDriver) DisplayName() string                         { return "Video Tasks JSON v1" }
func (videoTasksJSONDriver) DefaultBaseURL() string                      { return "" }
func (videoTasksJSONDriver) BillingPolicy() BillingPolicy                { return BillingOutputOnly }
func (videoTasksJSONDriver) DefaultCapabilityProfile() VideoFieldProfile { return ProfileBBLabuRoute }
func (videoTasksJSONDriver) SupportsReferenceVideoMetering() bool        { return true }
func (videoTasksJSONDriver) ValidateCapability(c VideoModelCapability) error {
	if c.Profile != ProfileBBLabuOfficial && c.Profile != ProfileBBLabuRoute {
		return fmt.Errorf("field profile %q is not supported by video tasks JSON v1", c.Profile)
	}
	return nil
}
func (videoTasksJSONDriver) ModelIDs() []string {
	models := make([]string, 0, len(bblabuVideoCapabilities))
	for _, capability := range bblabuVideoCapabilities {
		models = append(models, capability.Model)
	}
	return models
}
func (videoTasksJSONDriver) SupportsModel(model string) bool {
	c, ok := LookupVideoModelCapability(model, nil)
	return ok && c.Enabled
}
func (videoTasksJSONDriver) ParseModels(body []byte) ([]string, error) {
	return parseOpenAIModelIDs(body, nil, true)
}

// NormalizeVideoTasksBaseURL requires an explicit HTTPS API location and adds
// /v1 only to bare origins. Custom versioned API prefixes are preserved.
func NormalizeVideoTasksBaseURL(baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(baseURL, "#") {
		return "", fmt.Errorf("video tasks base URL must be an explicit HTTPS API base URL")
	}
	if strings.ContainsAny(parsed.Path, "\\\r\n") || strings.Contains(parsed.Path, "//") || parsed.RawPath != "" {
		return "", fmt.Errorf("invalid video tasks API path")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid video tasks API path")
		}
	}
	parsed.Scheme = "https"
	if parsed.Path == "" {
		parsed.Path = "/v1"
	}
	if strings.HasSuffix(parsed.Path, "/videos") || strings.HasSuffix(parsed.Path, "/models") {
		return "", fmt.Errorf("video tasks base URL must not include an endpoint")
	}
	return parsed.String(), nil
}

func (videoTasksJSONDriver) BuildRequest(ctx context.Context, params RequestParams) (*http.Request, error) {
	baseURL, err := NormalizeVideoTasksBaseURL(params.BaseURL)
	if err != nil {
		return nil, err
	}
	path, method, accept := "/videos", http.MethodGet, "application/json"
	var body []byte
	switch params.Operation {
	case OperationCreate:
		method = http.MethodPost
		body, err = encodeVideoTasksCreate(params.CreateRequest, params.Capability)
	case OperationModels:
		path = "/models"
	case OperationStatus, OperationContent, OperationCancel:
		var escaped string
		escaped, err = escapedTaskID(params.TaskID)
		if err != nil {
			return nil, err
		}
		path += "/" + escaped
		if params.Operation == OperationContent {
			path += "/content"
			accept = "*/*"
		}
		if params.Operation == OperationCancel {
			if params.Capability == nil || !params.Capability.Enabled || !params.Capability.SupportsCancel {
				return nil, fmt.Errorf("%w: configured video model does not support cancellation", ErrVideoTaskCancellationUnsupported)
			}
			if err := ValidateCapabilityProvider(ProviderVideoTasksJSONV1, *params.Capability); err != nil {
				return nil, err
			}
			method = http.MethodDelete
		}
	default:
		return nil, fmt.Errorf("unsupported video operation %q", params.Operation)
	}
	if err != nil {
		return nil, err
	}
	return newBearerRequest(ctx, method, baseURL+path, params.APIKey, accept, body)
}

func encodeVideoTasksCreate(request *CreateRequest, explicit *VideoModelCapability) ([]byte, error) {
	if request == nil {
		return nil, fmt.Errorf("video request is nil")
	}
	capability := explicit
	if capability == nil {
		capability = request.Capability
	}
	if capability == nil {
		return nil, fmt.Errorf("video tasks request requires a frozen model capability")
	}
	c := cloneVideoCapability(*capability)
	if err := ValidateCapabilityProvider(ProviderVideoTasksJSONV1, c); err != nil {
		return nil, err
	}
	if err := validateVideoCapabilityRequest(request, c); err != nil {
		return nil, err
	}
	// Deliberately construct an allowlist; Raw can contain upstream secrets or
	// unsupported parameters and is never cloned into a new-protocol request.
	payload := map[string]any{"model": videoCapabilityUpstreamModel(c), "prompt": request.Prompt}
	if c.DurationMode != VideoDurationUnknown {
		payload["duration"] = request.Duration
	}
	if c.ResolutionControl {
		payload["resolution"] = videoCapabilityValue(c.Resolutions, request.Resolution)
	}
	if c.GenerateAudio {
		payload["generate_audio"] = request.Audio
	}
	ratio, images, videos, audios, first, last := "aspect_ratio", "reference_image_urls", "reference_videos", "reference_audios", "first_image_url", "last_image_url"
	if c.Profile == ProfileBBLabuOfficial {
		ratio, images, videos, audios, first, last = "ratio", "referenceImages", "referenceVideos", "referenceAudios", "first_image", "last_image"
	}
	if request.AspectRatio != "" {
		payload[ratio] = videoCapabilityValue(c.Ratios, request.AspectRatio)
	}
	setOptionalStrings(payload, images, request.ReferenceImages)
	setOptionalStrings(payload, videos, request.ReferenceVideos)
	setOptionalStrings(payload, audios, request.ReferenceAudios)
	setOptionalString(payload, first, request.StartFrameURL)
	setOptionalString(payload, last, request.EndFrameURL)
	return json.Marshal(payload)
}

func videoCapabilityValue(values []string, value string) string {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return candidate
		}
	}
	return value
}

func videoCapabilityUpstreamModel(c VideoModelCapability) string {
	if c.UpstreamModel != "" {
		return c.UpstreamModel
	}
	return c.Model
}
