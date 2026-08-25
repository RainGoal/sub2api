package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ID string

const (
	ProviderBBLabuV1  ID = "bblabu_v1"
	ProviderFFLinkV1  ID = "fflink_v1"
	DefaultProviderID    = ProviderBBLabuV1
)

type Operation string

const (
	OperationCreate  Operation = "create"
	OperationStatus  Operation = "status"
	OperationContent Operation = "content"
	OperationCancel  Operation = "cancel"
	OperationModels  Operation = "models"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSettling  Status = "settling"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

type BillingPolicy string

const (
	BillingOutputOnly         BillingPolicy = "output_only"
	BillingOutputAndReference BillingPolicy = "output_and_reference"
)

type CreateRequest struct {
	RequestedModel  string         `json:"requested_model"`
	Model           string         `json:"model"`
	Prompt          string         `json:"prompt"`
	AspectRatio     string         `json:"aspect_ratio"`
	Duration        int            `json:"duration"`
	Resolution      string         `json:"resolution"`
	Audio           bool           `json:"audio,omitempty"`
	StartFrameURL   string         `json:"start_frame_url,omitempty"`
	EndFrameURL     string         `json:"end_frame_url,omitempty"`
	ReferenceImages []string       `json:"reference_images,omitempty"`
	ReferenceVideos []string       `json:"reference_videos,omitempty"`
	ReferenceAudios []string       `json:"reference_audios,omitempty"`
	Guidances       map[string]any `json:"guidances,omitempty"`
	Raw             map[string]any `json:"raw,omitempty"`
}

type RequestInfo struct {
	Model               string
	RequestedModel      string
	DurationSeconds     int
	Resolution          string
	ReferenceVideoCount int
	Audio               bool
}

type Task struct {
	ID                    string
	Status                Status
	Model                 string
	Resolution            string
	DurationSeconds       int
	ReferenceInputSeconds int
}

type RequestParams struct {
	BaseURL       string
	APIKey        string
	Operation     Operation
	TaskID        string
	CreateRequest *CreateRequest
}

type Driver interface {
	ID() ID
	DisplayName() string
	DefaultBaseURL() string
	ModelIDs() []string
	SupportsModel(model string) bool
	BillingPolicy() BillingPolicy
	BuildRequest(ctx context.Context, params RequestParams) (*http.Request, error)
	ParseModels(body []byte) ([]string, error)
	ParseTask(body []byte, fallbackTaskID string) (Task, error)
}

func IsPending(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "in_progress", "running", "processing", "settling":
		return true
	default:
		return false
	}
}

func IsFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func (r *CreateRequest) CanonicalJSON() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("video request is nil")
	}
	return json.Marshal(r)
}
