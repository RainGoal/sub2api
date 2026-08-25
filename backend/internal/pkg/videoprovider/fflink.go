package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const DefaultFFLinkBaseURL = "https://api.fflink.top/v1"

var fflinkModels = map[string]string{
	ModelSeedance20:     ModelSeedance20,
	ModelSeedance20Fast: ModelSeedance20Fast,
	ModelSeedance20Mini: ModelSeedance20Mini,
	ModelSeedance25:     ModelSeedance25,
}

type fflinkDriver struct{}

func (fflinkDriver) ID() ID                       { return ProviderFFLinkV1 }
func (fflinkDriver) DisplayName() string          { return "fflink V1" }
func (fflinkDriver) DefaultBaseURL() string       { return DefaultFFLinkBaseURL }
func (fflinkDriver) BillingPolicy() BillingPolicy { return BillingOutputOnly }
func (fflinkDriver) ModelIDs() []string {
	return []string{ModelSeedance20, ModelSeedance20Fast, ModelSeedance20Mini, ModelSeedance25}
}

func (fflinkDriver) SupportsModel(model string) bool {
	canonical, ok := CanonicalModel(model)
	if !ok {
		return false
	}
	_, supported := fflinkModels[canonical]
	return supported
}

func (driver fflinkDriver) BuildRequest(ctx context.Context, params RequestParams) (*http.Request, error) {
	targetURL, err := driver.buildURL(params.BaseURL, params.Operation, params.TaskID)
	if err != nil {
		return nil, err
	}
	method := http.MethodGet
	accept := "application/json"
	var body []byte
	switch params.Operation {
	case OperationCreate:
		method = http.MethodPost
		body, err = driver.encodeCreate(params.CreateRequest)
	case OperationCancel:
		method = http.MethodDelete
	case OperationContent:
		accept = "*/*"
	case OperationStatus, OperationModels:
	default:
		return nil, fmt.Errorf("unsupported video operation %q", params.Operation)
	}
	if err != nil {
		return nil, err
	}
	req, err := newBearerRequest(ctx, method, targetURL, params.APIKey, accept, body)
	if err != nil {
		return nil, err
	}
	if params.Operation == OperationCreate {
		req.Header.Set("Prefer", "respond-async")
	}
	return req, nil
}

func (fflinkDriver) buildURL(baseURL string, operation Operation, taskID string) (string, error) {
	path := "/videos/generations"
	switch operation {
	case OperationCreate:
	case OperationModels:
		path = "/models"
	case OperationStatus, OperationContent, OperationCancel:
		escaped, err := escapedTaskID(taskID)
		if err != nil {
			return "", err
		}
		path = "/videos/jobs/" + escaped
		if operation == OperationContent {
			path += "/content"
		}
	default:
		return "", fmt.Errorf("unsupported video operation %q", operation)
	}
	return buildProviderURL(baseURL, path)
}

func (fflinkDriver) encodeCreate(request *CreateRequest) ([]byte, error) {
	spec, upstreamModel, err := validateKnownModelRequest(request, fflinkModels)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(request.RequestedModel, "bytedance/seedance-2.5") {
		upstreamModel = "bytedance/seedance-2.5"
	}
	if len(spec.AllowedDurations) > 0 && !containsInt(spec.AllowedDurations, request.Duration) {
		return nil, fmt.Errorf("duration %d is not supported by %s", request.Duration, upstreamModel)
	}
	if err := validateFFLinkReferences(request, spec); err != nil {
		return nil, err
	}
	payload, err := cloneObject(request.Raw)
	if err != nil {
		return nil, fmt.Errorf("clone fflink request: %w", err)
	}
	for _, key := range []string{"ratio", "first_image", "last_image", "referenceImages", "referenceVideos", "referenceAudios"} {
		delete(payload, key)
	}
	payload["model"] = upstreamModel
	payload["prompt"] = request.Prompt
	payload["duration"] = request.Duration
	payload["resolution"] = strings.ToLower(request.Resolution)
	payload["aspect_ratio"] = request.AspectRatio
	payload["audio"] = request.Audio
	setOptionalString(payload, "start_frame_url", request.StartFrameURL)
	setOptionalString(payload, "end_frame_url", request.EndFrameURL)
	if len(request.Guidances) > 0 {
		payload["guidances"] = request.Guidances
	} else if guidances := buildFFLinkGuidances(request); len(guidances) > 0 {
		payload["guidances"] = guidances
	} else {
		delete(payload, "guidances")
	}
	return json.Marshal(payload)
}

func (fflinkDriver) ParseTask(body []byte, fallbackTaskID string) (Task, error) {
	task, payload, err := parseTaskBody(body, fallbackTaskID, "job_id", "id", "data.job_id", "data.id")
	if err != nil {
		return Task{}, err
	}
	task.Status = normalizeFFLinkStatus(firstPathText(payload, "status", "data.status"))
	return task, nil
}

func (driver fflinkDriver) ParseModels(body []byte) ([]string, error) {
	return parseOpenAIModelIDs(body, driver.SupportsModel)
}

func normalizeFFLinkStatus(status string) Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "queued":
		return StatusPending
	case "running", "in_progress", "processing":
		return StatusRunning
	case "settling":
		return StatusSettling
	case "completed", "succeeded", "success":
		return StatusCompleted
	case "failed", "error":
		return StatusFailed
	case "canceled", "cancelled":
		return StatusCanceled
	default:
		return Status(strings.ToLower(strings.TrimSpace(status)))
	}
}

func validateFFLinkReferences(request *CreateRequest, spec ModelSpec) error {
	imageCount := len(request.ReferenceImages)
	maxVideos, maxAudios := spec.MaxVideos, spec.MaxAudios
	maxTotalAssets := 0
	if spec.ID == ModelSeedance20Mini {
		if request.StartFrameURL != "" {
			imageCount++
		}
		if request.EndFrameURL != "" {
			imageCount++
		}
		maxTotalAssets = spec.MaxTotalAssets
	}
	if spec.ID == ModelSeedance25 {
		maxVideos, maxAudios = 3, 3
	}
	if imageCount > spec.MaxImages || len(request.ReferenceVideos) > maxVideos || len(request.ReferenceAudios) > maxAudios {
		return fmt.Errorf("reference asset count exceeds the %s provider limit", spec.ID)
	}
	if maxTotalAssets > 0 && imageCount+len(request.ReferenceVideos)+len(request.ReferenceAudios) > maxTotalAssets {
		return fmt.Errorf("total reference asset count exceeds the %s provider limit", spec.ID)
	}
	if request.Audio && request.Resolution == "1080p" && request.Model == ModelSeedance20 && request.Duration > 10 {
		return fmt.Errorf("seedance-2.0 1080p with generated audio supports at most 10 seconds")
	}
	if request.Audio && request.Resolution == "720p" && request.Model == ModelSeedance25 && request.Duration > 15 {
		return fmt.Errorf("seedance-2.5 720p with generated audio supports at most 15 seconds")
	}
	if len(request.ReferenceAudios) > 0 && len(request.ReferenceImages) == 0 && len(request.ReferenceVideos) == 0 {
		return fmt.Errorf("reference audio requires at least one reference image or video")
	}
	return nil
}

func buildFFLinkGuidances(request *CreateRequest) map[string]any {
	guidances := make(map[string]any)
	if values := fflinkGuidanceItems(request.ReferenceImages, "image"); len(values) > 0 {
		guidances["image_reference"] = values
	}
	if values := fflinkGuidanceItems(request.ReferenceVideos, "video"); len(values) > 0 {
		guidances["video_reference_base"] = values
	}
	if values := fflinkGuidanceItems(request.ReferenceAudios, "audio"); len(values) > 0 {
		guidances["audio_reference"] = values
	}
	return guidances
}

func fflinkGuidanceItems(urls []string, mediaKey string) []any {
	items := make([]any, 0, len(urls))
	for index, rawURL := range urls {
		item := map[string]any{
			mediaKey: map[string]any{"url": rawURL, "type": "UPLOADED"},
		}
		if mediaKey == "image" {
			item["strength"] = "MID"
			item["order"] = index
		}
		items = append(items, item)
	}
	return items
}
