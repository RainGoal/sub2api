package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const StudioMaxRecordBytes = 128 << 10

var (
	ErrStudioTaskNotFound = infraerrors.New(http.StatusNotFound, "STUDIO_TASK_NOT_FOUND", "Studio task not found")
	ErrStudioInvalidTask  = infraerrors.New(http.StatusBadRequest, "STUDIO_INVALID_TASK", "Invalid Studio task record")
	ErrStudioUnavailable  = infraerrors.New(http.StatusServiceUnavailable, "STUDIO_UNAVAILABLE", "Studio history is temporarily unavailable")
	studioTaskID          = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,256}$`)
)

// StudioTask is a user's display metadata, never a billing or execution source.
// Explicit nested schemas prevent credentials and uploaded file bodies entering history.
type StudioTask struct {
	ID             string           `json:"id"`
	Kind           string           `json:"kind"`
	KeyID          int64            `json:"keyId"`
	KeyName        string           `json:"keyName"`
	Title          string           `json:"title"`
	Project        string           `json:"project"`
	Prompt         string           `json:"prompt"`
	Model          string           `json:"model"`
	Mode           string           `json:"mode"`
	Payload        StudioPayload    `json:"payload"`
	ReferenceNames []string         `json:"referenceNames"`
	CreatedAt      int64            `json:"createdAt"`
	Status         string           `json:"status"`
	ImageResult    *StudioImageTask `json:"imageResult,omitempty"`
	VideoResult    *StudioVideoTask `json:"videoResult,omitempty"`
	Adopted        []string         `json:"adopted,omitempty"`
	ParentID       string           `json:"parentId,omitempty"`
}

type StudioPayload struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	Size            string   `json:"size,omitempty"`
	Quality         string   `json:"quality,omitempty"`
	N               int      `json:"n,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	Duration        int      `json:"duration,omitempty"`
	AspectRatio     string   `json:"aspect_ratio,omitempty"`
	Audio           *bool    `json:"audio,omitempty"`
	StartFrameURL   string   `json:"start_frame_url,omitempty"`
	EndFrameURL     string   `json:"end_frame_url,omitempty"`
	ReferenceImages []string `json:"referenceImages,omitempty"`
	ReferenceVideos []string `json:"referenceVideos,omitempty"`
	ReferenceAudios []string `json:"referenceAudios,omitempty"`
}

type StudioTaskError struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type StudioImageOutput struct {
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type StudioImageResult struct {
	Created int64               `json:"created,omitempty"`
	Data    []StudioImageOutput `json:"data,omitempty"`
}

type StudioImageTask struct {
	ID          string             `json:"id"`
	TaskID      string             `json:"task_id"`
	Object      string             `json:"object"`
	Status      string             `json:"status"`
	PollURL     string             `json:"poll_url,omitempty"`
	HTTPStatus  int                `json:"http_status,omitempty"`
	ImageURL    string             `json:"image_url,omitempty"`
	Result      *StudioImageResult `json:"result,omitempty"`
	Error       *StudioTaskError   `json:"error,omitempty"`
	CreatedAt   int64              `json:"created_at"`
	CompletedAt *int64             `json:"completed_at,omitempty"`
	ExpiresAt   int64              `json:"expires_at"`
}

type StudioVideoTask struct {
	ID         string           `json:"id"`
	Object     string           `json:"object"`
	Status     string           `json:"status"`
	Model      string           `json:"model"`
	Resolution string           `json:"resolution"`
	Duration   int              `json:"duration"`
	ContentURL *string          `json:"content_url"`
	Error      *StudioTaskError `json:"error"`
}

type StudioRepository interface {
	Get(context.Context, int64, string, string) (*StudioTask, error)
	List(context.Context, int64, int, int) ([]StudioTask, error)
	Upsert(context.Context, int64, *StudioTask) (*StudioTask, error)
}

type StudioKeyReader interface {
	GetByID(context.Context, int64) (*APIKey, error)
}
type StudioImageReader interface {
	Get(context.Context, ImageTaskOwner, string) (*ImageTask, error)
}
type StudioVideoReader interface {
	GetByProviderTask(context.Context, string, int64, int64) (*SeedanceVideoPendingBilling, error)
}

type StudioService struct {
	repo   StudioRepository
	keys   StudioKeyReader
	images StudioImageReader
	videos StudioVideoReader
}

func NewStudioService(repo StudioRepository, keys StudioKeyReader, images StudioImageReader, videos StudioVideoReader) *StudioService {
	return &StudioService{repo: repo, keys: keys, images: images, videos: videos}
}

func (s *StudioService) List(ctx context.Context, userID int64, page, pageSize int) ([]StudioTask, bool, error) {
	if userID <= 0 || page < 1 || page > 10000 || pageSize < 1 || pageSize > 100 {
		return nil, false, ErrStudioInvalidTask
	}
	items, err := s.repo.List(ctx, userID, pageSize+1, (page-1)*pageSize)
	if err != nil {
		return nil, false, ErrStudioUnavailable.WithCause(err)
	}
	hasMore := len(items) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	if items == nil {
		items = []StudioTask{}
	}
	return items, hasMore, nil
}

func (s *StudioService) Save(ctx context.Context, userID int64, task StudioTask) (*StudioTask, error) {
	if userID <= 0 || validateStudioTask(&task) != nil {
		return nil, ErrStudioInvalidTask
	}
	previous, err := s.repo.Get(ctx, userID, task.Kind, task.ID)
	if err != nil && !errors.Is(err, ErrStudioTaskNotFound) {
		return nil, ErrStudioUnavailable.WithCause(err)
	}
	if previous != nil {
		if previous.KeyID != task.KeyID {
			return nil, ErrStudioInvalidTask
		}
		// Immutable inputs survive later edits and expired gateway task records.
		task.KeyName, task.Prompt, task.Model, task.Mode = previous.KeyName, previous.Prompt, previous.Model, previous.Mode
		task.Payload, task.CreatedAt, task.ReferenceNames, task.ParentID = previous.Payload, previous.CreatedAt, previous.ReferenceNames, previous.ParentID
	} else {
		key, keyErr := s.keys.GetByID(ctx, task.KeyID)
		if keyErr != nil || key == nil || key.UserID != userID {
			return nil, ErrStudioTaskNotFound
		}
		task.KeyName = key.Name
	}
	if task.Kind == "image" {
		actual, readErr := s.images.Get(ctx, ImageTaskOwner{UserID: userID, APIKeyID: task.KeyID}, task.ID)
		if readErr != nil && previous == nil {
			if errors.Is(readErr, ErrImageTaskNotFound) {
				return nil, ErrStudioTaskNotFound
			}
			return nil, ErrStudioUnavailable.WithCause(readErr)
		}
		if actual != nil {
			encoded, marshalErr := json.Marshal(actual)
			if marshalErr != nil {
				return nil, ErrStudioUnavailable.WithCause(marshalErr)
			}
			var result StudioImageTask
			if unmarshalErr := json.Unmarshal(encoded, &result); unmarshalErr != nil {
				return nil, ErrStudioUnavailable.WithCause(unmarshalErr)
			}
			task.ImageResult, task.Status = &result, actual.Status
			if previous == nil {
				task.CreatedAt = actual.CreatedAt * 1000
			}
		}
	} else if previous == nil {
		actual, readErr := s.videos.GetByProviderTask(ctx, task.ID, userID, task.KeyID)
		if readErr != nil {
			if errors.Is(readErr, ErrSeedanceVideoTaskNotFound) {
				return nil, ErrStudioTaskNotFound
			}
			return nil, ErrStudioUnavailable.WithCause(readErr)
		}
		if actual == nil {
			return nil, ErrStudioTaskNotFound
		}
		if created, parseErr := time.Parse(time.RFC3339Nano, actual.CreatedAt); parseErr == nil {
			task.CreatedAt = created.UnixMilli()
		}
	}
	if err := validateStudioTask(&task); err != nil {
		return nil, err
	}
	saved, err := s.repo.Upsert(ctx, userID, &task)
	if err != nil {
		return nil, ErrStudioUnavailable.WithCause(err)
	}
	return saved, nil
}

func validateStudioTask(task *StudioTask) error {
	if !studioTaskID.MatchString(task.ID) || (task.Kind != "image" && task.Kind != "video") || task.KeyID <= 0 ||
		(task.Mode != "generation" && task.Mode != "edit") || task.CreatedAt <= 0 || task.CreatedAt > time.Now().Add(24*time.Hour).UnixMilli() ||
		len(task.Title) > 240 || len(task.Project) > 240 || len(task.KeyName) > 240 || len(task.Model) > 256 || len(task.Prompt) > 32000 ||
		len(task.ReferenceNames) > 32 || len(task.Adopted) > 64 || (task.ParentID != "" && !studioTaskID.MatchString(task.ParentID)) || !studioStatusValid(task.Status) {
		return ErrStudioInvalidTask
	}
	if task.Kind == "image" && task.VideoResult != nil || task.Kind == "video" && task.ImageResult != nil {
		return ErrStudioInvalidTask
	}
	for _, name := range task.ReferenceNames {
		if len(name) > 512 {
			return ErrStudioInvalidTask
		}
	}
	payload := task.Payload
	if len(payload.Model) > 256 || len(payload.Prompt) > 32000 || len(payload.Size) > 64 || len(payload.Quality) > 64 || len(payload.Resolution) > 64 || len(payload.AspectRatio) > 64 || payload.N < 0 || payload.N > 100 || payload.Duration < 0 || payload.Duration > 600 {
		return ErrStudioInvalidTask
	}
	urls := []string{payload.StartFrameURL, payload.EndFrameURL}
	for _, refs := range [][]string{payload.ReferenceImages, payload.ReferenceVideos, payload.ReferenceAudios} {
		if len(refs) > 32 {
			return ErrStudioInvalidTask
		}
		urls = append(urls, refs...)
	}
	for _, adopted := range task.Adopted {
		if task.Kind == "video" && studioVideoContentPath(adopted, task.ID) {
			continue
		}
		urls = append(urls, adopted)
	}
	if result := task.ImageResult; result != nil {
		if result.ID != task.ID || result.TaskID != task.ID || !studioStatusValid(result.Status) || len(result.Object) > 128 || !studioErrorValid(result.Error) {
			return ErrStudioInvalidTask
		}
		urls = append(urls, result.ImageURL)
		if result.PollURL != "" && result.PollURL != "/v1/images/tasks/"+task.ID && result.PollURL != "/images/tasks/"+task.ID {
			return ErrStudioInvalidTask
		}
		if result.Result != nil {
			if len(result.Result.Data) > 100 {
				return ErrStudioInvalidTask
			}
			for _, output := range result.Result.Data {
				if len(output.RevisedPrompt) > 32000 {
					return ErrStudioInvalidTask
				}
				urls = append(urls, output.URL)
			}
		}
	}
	if result := task.VideoResult; result != nil {
		if result.ID != task.ID || !studioStatusValid(result.Status) || len(result.Object) > 128 || len(result.Model) > 256 || len(result.Resolution) > 64 || !studioErrorValid(result.Error) {
			return ErrStudioInvalidTask
		}
		if result.ContentURL != nil && !studioVideoContentPath(*result.ContentURL, task.ID) {
			urls = append(urls, *result.ContentURL)
		}
	}
	for _, raw := range urls {
		if !studioURLValid(raw) {
			return ErrStudioInvalidTask
		}
	}
	encoded, err := json.Marshal(task)
	if err != nil || len(encoded) > StudioMaxRecordBytes {
		return ErrStudioInvalidTask
	}
	return nil
}

func studioStatusValid(status string) bool {
	switch status {
	case "processing", "queued", "in_progress", "completed", "failed", "canceled", "cancelled", "pending", "unknown":
		return true
	}
	return false
}

func studioVideoContentPath(raw, id string) bool {
	return raw == "/v1/videos/"+id+"/content" || raw == "/v1/videos/jobs/"+id+"/content"
}

func studioErrorValid(taskErr *StudioTaskError) bool {
	return taskErr == nil || len(taskErr.Type) <= 128 && len(taskErr.Code) <= 128 && len(taskErr.Message) <= 4000
}

func studioURLValid(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > 8192 || strings.ContainsAny(raw, "\\\r\n\t ") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
