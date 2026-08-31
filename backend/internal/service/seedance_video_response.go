package service

import (
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

const (
	seedanceVideoResponseObject = "video"

	SeedanceVideoResponseStatusQueued     = "queued"
	SeedanceVideoResponseStatusInProgress = "in_progress"
	SeedanceVideoResponseStatusCompleted  = "completed"
	SeedanceVideoResponseStatusFailed     = "failed"
	SeedanceVideoResponseStatusCanceled   = "canceled"

	SeedanceVideoResponseErrorCodeGenerationFailed        = "video_generation_failed"
	SeedanceVideoResponseErrorMessageGenerationFailed     = "Video generation failed"
	SeedanceVideoResponseErrorCodeCancellationFailed      = "video_cancellation_failed"
	SeedanceVideoResponseErrorMessageCancellationFailed   = "Video cancellation failed"
	SeedanceVideoResponseErrorCodeCancellationConflict    = "video_cancellation_conflict"
	SeedanceVideoResponseErrorMessageCancellationConflict = "Video cancellation was not accepted because the task is already completed"
)

// SeedanceVideoResponse is the provider-neutral success envelope exposed by
// the Seedance video endpoints. ContentURL and Error are pointers on purpose:
// both fields are part of the stable schema and must serialize as null when
// they do not apply.
type SeedanceVideoResponse struct {
	ID         string                      `json:"id"`
	Object     string                      `json:"object"`
	Status     string                      `json:"status"`
	Model      string                      `json:"model"`
	Resolution string                      `json:"resolution"`
	Duration   int                         `json:"duration"`
	ContentURL *string                     `json:"content_url"`
	Error      *SeedanceVideoResponseError `json:"error"`
}

// SeedanceVideoResponseError contains a sanitized task-level failure. HTTP
// transport and authentication failures continue to use the existing error
// envelope and are not represented by this field.
type SeedanceVideoResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SeedanceVideoResponseMeta carries local request/task metadata that should
// take precedence over values echoed by a provider.
type SeedanceVideoResponseMeta struct {
	ID         string
	Model      string
	Resolution string
	Duration   int
	// SettlementStatus is an internal accounting snapshot. When present, a
	// provider completion is not exposed as public completed until the local
	// billing transaction has reached the settled state.
	SettlementStatus string
	ContentURL       string
	ErrorCode        string
	ErrorMessage     string
}

// NormalizeSeedanceVideoStatus maps provider-specific task states to the
// stable public state set. Unknown or missing states are treated as a safe
// non-terminal state; they must never be reported as a successful completion.
func NormalizeSeedanceVideoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending":
		return SeedanceVideoResponseStatusQueued
	case "running", "in_progress", "processing", "settling":
		return SeedanceVideoResponseStatusInProgress
	case "completed", "succeeded", "success":
		return SeedanceVideoResponseStatusCompleted
	case "failed", "error":
		return SeedanceVideoResponseStatusFailed
	case "canceled", "cancelled":
		return SeedanceVideoResponseStatusCanceled
	default:
		return SeedanceVideoResponseStatusInProgress
	}
}

// BuildSeedanceVideoResponse converts an internal forward result into the
// provider-neutral response. Local metadata wins when present, allowing the
// API to return the original request snapshot even when a provider omits
// model, resolution, or duration in its status response.
func BuildSeedanceVideoResponse(result *SeedanceVideoForwardResult, meta SeedanceVideoResponseMeta) SeedanceVideoResponse {
	response := SeedanceVideoResponse{
		Object: seedanceVideoResponseObject,
		Status: NormalizeSeedanceVideoStatus(""),
	}

	var forward *OpenAIForwardResult
	if result != nil {
		response.Status = NormalizeSeedanceVideoStatus(result.TaskStatus)
		forward = result.OpenAIForwardResult
	}

	if forward != nil {
		response.ID = strings.TrimSpace(forward.ResponseID)
		response.Model = normalizeSeedanceResponseModel(forward.Model)
		response.Resolution = normalizeSeedanceResponseResolution(forward.VideoResolution)
		response.Duration = forward.VideoDurationSeconds
	}

	if value := strings.TrimSpace(meta.ID); value != "" {
		response.ID = value
	}
	if value := normalizeSeedanceResponseModel(meta.Model); value != "" {
		response.Model = value
	}
	if value := normalizeSeedanceResponseResolution(meta.Resolution); value != "" {
		response.Resolution = value
	}
	if meta.Duration > 0 {
		response.Duration = meta.Duration
	}
	// A provider can report completed before the gateway has durably recorded
	// usage. Keep the public state non-terminal during that window so the
	// contract never advertises a URL that is not yet downloadable.
	if response.Status == SeedanceVideoResponseStatusCompleted &&
		strings.TrimSpace(meta.SettlementStatus) != "" &&
		!strings.EqualFold(strings.TrimSpace(meta.SettlementStatus), SeedanceVideoSettlementSettled) {
		response.Status = SeedanceVideoResponseStatusInProgress
	}
	if response.Status == SeedanceVideoResponseStatusCompleted {
		settled := strings.TrimSpace(meta.SettlementStatus) == "" ||
			strings.EqualFold(strings.TrimSpace(meta.SettlementStatus), SeedanceVideoSettlementSettled)
		if settled {
			if value := canonicalSeedanceVideoContentURL(response.ID, meta.ContentURL); value != "" {
				response.ContentURL = &value
			}
		}
		// A completed task without a canonical content URL is not a usable
		// public result. Keep the task queryable, but expose it as in progress
		// until the gateway can provide a downloadable local resource.
		if response.ContentURL == nil {
			response.Status = SeedanceVideoResponseStatusInProgress
		}
	}

	if response.Status == SeedanceVideoResponseStatusFailed {
		errorCode := strings.TrimSpace(meta.ErrorCode)
		errorMessage := strings.TrimSpace(meta.ErrorMessage)
		if errorCode == "" {
			errorCode = SeedanceVideoResponseErrorCodeGenerationFailed
		}
		if errorMessage == "" {
			errorMessage = SeedanceVideoResponseErrorMessageGenerationFailed
		}
		response.Error = &SeedanceVideoResponseError{Code: errorCode, Message: errorMessage}
	}
	return response
}

// canonicalSeedanceVideoContentURL prevents an upstream media URL from
// crossing the public response boundary. The gateway owns the content route;
// only that exact relative path is safe to advertise to callers.
func canonicalSeedanceVideoContentURL(taskID, candidate string) string {
	taskID = strings.TrimSpace(taskID)
	candidate = strings.TrimSpace(candidate)
	if taskID == "" || candidate == "" {
		return ""
	}
	expected := "/v1/videos/" + url.PathEscape(taskID) + "/content"
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if candidate != expected {
		return ""
	}
	return candidate
}

func normalizeSeedanceResponseModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if canonical, ok := videoprovider.CanonicalModel(model); ok {
		return canonical
	}
	return model
}

func normalizeSeedanceResponseResolution(resolution string) string {
	resolution = strings.TrimSpace(resolution)
	if resolution == "" {
		return ""
	}
	if canonical, ok := LookupVideoBillingResolution(resolution); ok {
		return canonical
	}
	return strings.ToLower(resolution)
}
