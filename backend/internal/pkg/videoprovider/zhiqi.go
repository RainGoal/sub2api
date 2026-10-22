package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const DefaultZhiqiBaseURL = "https://zhiqiapi.com/v1"

type zhiqiDriver struct{}

func (zhiqiDriver) ID() ID                                      { return ProviderZhiqiV1 }
func (zhiqiDriver) DisplayName() string                         { return "Zhiqi v1" }
func (zhiqiDriver) DefaultBaseURL() string                      { return DefaultZhiqiBaseURL }
func (zhiqiDriver) BillingPolicy() BillingPolicy                { return BillingOutputOnly }
func (zhiqiDriver) ModelIDs() []string                          { return nil }
func (zhiqiDriver) SupportsModel(string) bool                   { return false }
func (zhiqiDriver) DefaultCapabilityProfile() VideoFieldProfile { return ProfileZhiqiV1 }
func (zhiqiDriver) SupportsReferenceVideoMetering() bool        { return false }
func (zhiqiDriver) ValidateCapability(c VideoModelCapability) error {
	if c.Profile != ProfileZhiqiV1 {
		return fmt.Errorf("field profile %q is not supported by zhiqi v1", c.Profile)
	}
	if c.SupportsCancel {
		return fmt.Errorf("zhiqi v1 does not support task cancellation")
	}
	if c.BillingPolicy == BillingOutputAndReference || c.MeteringPolicy == VideoMeteringOutputAndReferenceSeconds {
		return fmt.Errorf("zhiqi v1 does not provide reference video metering")
	}
	return nil
}

func (zhiqiDriver) ParseModels(body []byte) ([]string, error) {
	return parseOpenAIModelIDs(body, nil, true)
}

func (driver zhiqiDriver) BuildRequest(ctx context.Context, params RequestParams) (*http.Request, error) {
	baseURL := params.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = driver.DefaultBaseURL()
	}
	baseURL, err := NormalizeVideoTasksBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	path, method, accept := "/videos", http.MethodGet, "application/json"
	var body []byte
	switch params.Operation {
	case OperationCreate:
		method = http.MethodPost
		body, err = encodeZhiqiCreate(params.CreateRequest, params.Capability)
	case OperationModels:
		path = "/models"
	case OperationStatus, OperationContent:
		var escaped string
		escaped, err = escapedTaskID(params.TaskID)
		path += "/" + escaped
		if params.Operation == OperationContent {
			path += "/content"
			accept = "*/*"
		}
	case OperationCancel:
		return nil, fmt.Errorf("%w: zhiqi v1 does not support task cancellation", ErrVideoTaskCancellationUnsupported)
	default:
		return nil, fmt.Errorf("unsupported video operation %q", params.Operation)
	}
	if err != nil {
		return nil, err
	}
	return newBearerRequest(ctx, method, baseURL+path, params.APIKey, accept, body)
}

func encodeZhiqiCreate(request *CreateRequest, explicit *VideoModelCapability) ([]byte, error) {
	if request == nil {
		return nil, fmt.Errorf("video request is nil")
	}
	capability := explicit
	if capability == nil {
		capability = request.Capability
	}
	if capability == nil {
		return nil, fmt.Errorf("zhiqi request requires a frozen model capability")
	}
	c := cloneVideoCapability(*capability)
	if err := ValidateCapabilityProvider(ProviderZhiqiV1, c); err != nil {
		return nil, err
	}
	if err := validateVideoCapabilityRequest(request, c); err != nil {
		return nil, err
	}
	// Keep primary references, frames, and reference arrays distinct. The public
	// image_url alias means a first frame, never Zhiqi's input_reference.
	// The public contract supports one output; never inherit a supplier's
	// potentially different output-count default or forward Raw.n.
	payload := map[string]any{"model": videoCapabilityUpstreamModel(c), "prompt": request.Prompt, "n": 1}
	if c.DurationMode != VideoDurationUnknown {
		payload["duration"] = request.Duration
	}
	if c.ResolutionControl {
		payload["resolution"] = videoCapabilityValue(c.Resolutions, request.Resolution)
	}
	if c.GenerateAudio {
		payload["generate_audio"] = request.Audio
	}
	if request.AspectRatio != "" {
		payload["aspect_ratio"] = videoCapabilityValue(c.Ratios, request.AspectRatio)
	}
	setOptionalStrings(payload, "reference_image_urls", request.ReferenceImages)
	setOptionalStrings(payload, "reference_video_urls", request.ReferenceVideos)
	setOptionalStrings(payload, "reference_audio_urls", request.ReferenceAudios)
	setOptionalString(payload, "first_frame_image_url", request.StartFrameURL)
	setOptionalString(payload, "last_frame_image_url", request.EndFrameURL)
	return json.Marshal(payload)
}
