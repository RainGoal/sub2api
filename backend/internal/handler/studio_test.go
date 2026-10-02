package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type studioTestRepo struct {
	task          *service.StudioTask
	userID        int64
	saves         int
	items         []service.StudioTask
	limit, offset int
}

func (r *studioTestRepo) Get(_ context.Context, userID int64, kind, id string) (*service.StudioTask, error) {
	if r.task == nil || r.userID != userID || r.task.Kind != kind || r.task.ID != id {
		return nil, service.ErrStudioTaskNotFound
	}
	copy := *r.task
	return &copy, nil
}

func (r *studioTestRepo) Upsert(_ context.Context, userID int64, task *service.StudioTask) (*service.StudioTask, error) {
	r.userID, r.task = userID, task
	r.saves++
	return task, nil
}

func (r *studioTestRepo) List(_ context.Context, userID int64, limit, offset int) ([]service.StudioTask, error) {
	r.userID, r.limit, r.offset = userID, limit, offset
	return r.items, nil
}

type studioTestKeys struct{ userID int64 }

func (r *studioTestKeys) GetByID(context.Context, int64) (*service.APIKey, error) {
	return &service.APIKey{ID: 7, UserID: r.userID, Name: "Studio key"}, nil
}

type studioTestImages struct {
	expired        bool
	owner          service.ImageTaskOwner
	id             string
	requestedOwner service.ImageTaskOwner
}

func (r *studioTestImages) Get(_ context.Context, owner service.ImageTaskOwner, id string) (*service.ImageTask, error) {
	r.requestedOwner = owner
	if r.expired || owner != r.owner || id != r.id {
		return nil, service.ErrImageTaskNotFound
	}
	return &service.ImageTask{ID: id, TaskID: id, Object: "image.generation.task", Status: "completed", CreatedAt: time.Now().Add(-time.Minute).Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Result: json.RawMessage(`{"data":[{"url":"https://assets.example/result.png"}]}`)}, nil
}

type studioTestVideos struct{ owner int64 }

func (r *studioTestVideos) GetByProviderTask(_ context.Context, _ string, userID, keyID int64) (*service.SeedanceVideoPendingBilling, error) {
	if userID != r.owner || keyID != 7 {
		return nil, service.ErrSeedanceVideoTaskNotFound
	}
	return &service.SeedanceVideoPendingBilling{UserID: userID, APIKeyID: keyID, CreatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}, nil
}

func studioFixture(auth bool) (*gin.Engine, *studioTestRepo, *studioTestKeys, *studioTestImages) {
	gin.SetMode(gin.TestMode)
	repo, keys := &studioTestRepo{}, &studioTestKeys{userID: 42}
	images := &studioTestImages{owner: service.ImageTaskOwner{UserID: 42, APIKeyID: 7}, id: "imgtask_demo"}
	h := &StudioHandler{service: service.NewStudioService(repo, keys, images, &studioTestVideos{owner: 42})}
	router := gin.New()
	if auth {
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
	}
	router.GET("/api/v1/studio/tasks", h.List)
	router.PUT("/api/v1/studio/tasks/:id", h.Save)
	return router, repo, keys, images
}

func studioRecord() service.StudioTask {
	return service.StudioTask{ID: "imgtask_demo", Kind: "image", KeyID: 7, Title: "Product", Project: "Launch", Prompt: "product on white", Model: "image-model", Mode: "generation", Payload: service.StudioPayload{Model: "image-model", Prompt: "product on white", N: 2}, ReferenceNames: []string{}, CreatedAt: time.Now().UnixMilli(), Status: "processing"}
}

func studioPut(t *testing.T, router *gin.Engine, task service.StudioTask) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(task)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/studio/tasks/"+task.ID, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestStudioHandlerRequiresLogin(t *testing.T) {
	router, repo, _, _ := studioFixture(false)
	require.Equal(t, http.StatusUnauthorized, studioPut(t, router, studioRecord()).Code)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/studio/tasks", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, repo.saves)
}

func TestStudioHandlerVerifiesOwnershipAndSavesRealImageResult(t *testing.T) {
	router, repo, keys, images := studioFixture(true)
	keys.userID = 99
	require.Equal(t, http.StatusNotFound, studioPut(t, router, studioRecord()).Code)
	keys.userID = 42
	images.owner.UserID = 99
	require.Equal(t, http.StatusNotFound, studioPut(t, router, studioRecord()).Code)
	require.Zero(t, repo.saves)
	images.owner.UserID = 42
	w := studioPut(t, router, studioRecord())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, int64(42), repo.userID)
	require.Equal(t, images.owner, images.requestedOwner)
	require.Equal(t, "completed", repo.task.Status)
	require.Equal(t, "Studio key", repo.task.KeyName)
	require.Equal(t, "https://assets.example/result.png", repo.task.ImageResult.Result.Data[0].URL)
	require.Contains(t, w.Body.String(), `"code":0`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestStudioHandlerPreservesInputsAfterGatewayExpiration(t *testing.T) {
	router, repo, _, images := studioFixture(true)
	require.Equal(t, http.StatusOK, studioPut(t, router, studioRecord()).Code)
	originalTime := repo.task.CreatedAt
	images.expired = true
	updated := *repo.task
	updated.Title, updated.Project = "Selected version", "Summer"
	updated.Prompt, updated.Model, updated.CreatedAt = "changed", "other", time.Now().UnixMilli()
	updated.Payload.Prompt = "changed"
	updated.Adopted = []string{"https://assets.example/result.png"}
	require.Equal(t, http.StatusOK, studioPut(t, router, updated).Code)
	require.Equal(t, "Selected version", repo.task.Title)
	require.Equal(t, "Summer", repo.task.Project)
	require.Equal(t, "product on white", repo.task.Prompt)
	require.Equal(t, "product on white", repo.task.Payload.Prompt)
	require.Equal(t, "image-model", repo.task.Model)
	require.Equal(t, originalTime, repo.task.CreatedAt)
	updated.KeyID = 8
	require.Equal(t, http.StatusBadRequest, studioPut(t, router, updated).Code)
}

func TestStudioHandlerRejectsUnsafeAndOversizedRecords(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:image/png;base64,secret", "//evil.example/image", "/v1/user/profile", "https://user:secret@assets.example/file"} {
		t.Run(raw, func(t *testing.T) {
			router, repo, _, _ := studioFixture(true)
			task := studioRecord()
			task.Payload.StartFrameURL = raw
			require.Equal(t, http.StatusBadRequest, studioPut(t, router, task).Code)
			require.Zero(t, repo.saves)
		})
	}
	for _, extra := range []string{`,"token":"secret"`, `,"payload":{"api_key":"secret"}`, `,"imageResult":{"b64_json":"blob"}`} {
		router, repo, _, _ := studioFixture(true)
		data, err := json.Marshal(studioRecord())
		require.NoError(t, err)
		body := strings.TrimSuffix(string(data), "}") + extra + "}"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/studio/tasks/imgtask_demo", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Zero(t, repo.saves)
	}
	router, repo, _, _ := studioFixture(true)
	task := studioRecord()
	task.Prompt = strings.Repeat("x", service.StudioMaxRecordBytes+1)
	require.Equal(t, http.StatusRequestEntityTooLarge, studioPut(t, router, task).Code)
	require.Zero(t, repo.saves)
}

func TestStudioHandlerPaginationAndVideoOwnership(t *testing.T) {
	router, repo, _, _ := studioFixture(true)
	repo.items = []service.StudioTask{studioRecord(), studioRecord()}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/studio/tasks?page=2&page_size=1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 2, repo.limit)
	require.Equal(t, 1, repo.offset)
	require.Equal(t, int64(42), repo.userID)
	require.Contains(t, w.Body.String(), `"has_more":true`)
	for _, query := range []string{"page=0", "page_size=101", "page=not-a-number"} {
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/studio/tasks?"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	task := studioRecord()
	task.Kind, task.ID = "video", "video_demo"
	content := "/v1/videos/video_demo/content"
	task.VideoResult = &service.StudioVideoTask{ID: task.ID, Status: "completed", ContentURL: &content}
	task.Status = "completed"
	require.Equal(t, http.StatusOK, studioPut(t, router, task).Code)
	task.ID, task.VideoResult.ID = "other", "other"
	task.KeyID = 8
	task.VideoResult.ContentURL = nil
	require.Equal(t, http.StatusNotFound, studioPut(t, router, task).Code)
}
