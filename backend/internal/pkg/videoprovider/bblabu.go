package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const DefaultBBLabuBaseURL = "https://api.bblabu.ai/v1"

var bblabuModels = map[string]string{
	ModelSeedance20: "Seedance-2.0",
	ModelSeedance25: "Seedance-2.5",
}

type bblabuDriver struct{}

func (bblabuDriver) ID() ID                       { return ProviderBBLabuV1 }
func (bblabuDriver) DisplayName() string          { return "bblabu V1" }
func (bblabuDriver) DefaultBaseURL() string       { return DefaultBBLabuBaseURL }
func (bblabuDriver) BillingPolicy() BillingPolicy { return BillingOutputAndReference }
func (bblabuDriver) ModelIDs() []string {
	return []string{bblabuModels[ModelSeedance20], bblabuModels[ModelSeedance25]}
}

func (bblabuDriver) SupportsModel(model string) bool {
	canonical, ok := CanonicalModel(model)
	if !ok {
		return false
	}
	_, ok = bblabuModels[canonical]
	return ok
}

func (driver bblabuDriver) BuildRequest(ctx context.Context, params RequestParams) (*http.Request, error) {
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
	case OperationContent:
		accept = "*/*"
	case OperationStatus, OperationModels:
	default:
		return nil, fmt.Errorf("unsupported video operation %q", params.Operation)
	}
	if err != nil {
		return nil, err
	}
	return newBearerRequest(ctx, method, targetURL, params.APIKey, accept, body)
}

func (bblabuDriver) buildURL(baseURL string, operation Operation, taskID string) (string, error) {
	path := "/videos"
	switch operation {
	case OperationCreate:
	case OperationModels:
		path = "/models"
	case OperationStatus, OperationContent:
		escaped, err := escapedTaskID(taskID)
		if err != nil {
			return "", err
		}
		path += "/" + escaped
		if operation == OperationContent {
			path += "/content"
		}
	case OperationCancel:
		return "", fmt.Errorf("%w: video provider %s does not support task cancellation", ErrVideoTaskCancellationUnsupported, ProviderBBLabuV1)
	default:
		return "", fmt.Errorf("unsupported video operation %q", operation)
	}
	return buildProviderURL(baseURL, path)
}

func (bblabuDriver) encodeCreate(request *CreateRequest) ([]byte, error) {
	spec, upstreamModel, err := validateKnownModelRequest(request, bblabuModels)
	if err != nil {
		return nil, err
	}
	if (request.StartFrameURL != "" || request.EndFrameURL != "") && !strings.EqualFold(request.AspectRatio, "auto") {
		return nil, fmt.Errorf("aspect ratio must be auto when a start or end frame is used by bblabu")
	}
	imageCount := len(request.ReferenceImages)
	if request.StartFrameURL != "" {
		imageCount++
	}
	if request.EndFrameURL != "" {
		imageCount++
	}
	maxImages, maxTotalAssets := spec.MaxImages, spec.MaxTotalAssets
	if spec.ID == ModelSeedance20 {
		maxImages, maxTotalAssets = 9, 12
	}
	if imageCount > maxImages || len(request.ReferenceVideos) > spec.MaxVideos || len(request.ReferenceAudios) > spec.MaxAudios {
		return nil, fmt.Errorf("reference asset count exceeds the %s provider limit", upstreamModel)
	}
	if imageCount+len(request.ReferenceVideos)+len(request.ReferenceAudios) > maxTotalAssets {
		return nil, fmt.Errorf("total reference asset count exceeds the %s provider limit", upstreamModel)
	}
	payload, err := cloneObject(request.Raw)
	if err != nil {
		return nil, fmt.Errorf("clone bblabu request: %w", err)
	}
	for _, key := range []string{"aspect_ratio", "image_url", "start_frame_url", "end_frame_url", "guidances"} {
		delete(payload, key)
	}
	payload["model"] = upstreamModel
	payload["prompt"] = request.Prompt
	payload["duration"] = request.Duration
	payload["resolution"] = bblabuResolution(request.Resolution)
	payload["ratio"] = bblabuRatio(request.AspectRatio)
	setOptionalString(payload, "first_image", request.StartFrameURL)
	setOptionalString(payload, "last_image", request.EndFrameURL)
	setOptionalStrings(payload, "referenceImages", request.ReferenceImages)
	setOptionalStrings(payload, "referenceVideos", request.ReferenceVideos)
	setOptionalStrings(payload, "referenceAudios", request.ReferenceAudios)
	return json.Marshal(payload)
}

func (bblabuDriver) ParseTask(body []byte, fallbackTaskID string) (Task, error) {
	task, payload, err := parseTaskBody(body, fallbackTaskID, "task_id", "id", "data.task_id", "data.id")
	if err != nil {
		return Task{}, err
	}
	task.Status = normalizeBBLabuStatus(firstPathText(payload, "status", "data.status"))
	return task, nil
}

func (driver bblabuDriver) ParseModels(body []byte) ([]string, error) {
	return parseOpenAIModelIDs(body, driver.SupportsModel)
}

func normalizeBBLabuStatus(status string) Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending":
		return StatusPending
	case "in_progress", "running", "processing", "cancel_requested":
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

func bblabuResolution(value string) string {
	if strings.EqualFold(value, "4k") {
		return "4K"
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func bblabuRatio(value string) string {
	if strings.EqualFold(value, "auto") {
		return "Auto"
	}
	return value
}

func setOptionalString(payload map[string]any, key, value string) {
	if strings.TrimSpace(value) == "" {
		delete(payload, key)
		return
	}
	payload[key] = value
}

func setOptionalStrings(payload map[string]any, key string, values []string) {
	if len(values) == 0 {
		delete(payload, key)
		return
	}
	payload[key] = values
}
