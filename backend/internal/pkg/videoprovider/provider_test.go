package videoprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func requestBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return body
}

func TestBBLabuDriverContract(t *testing.T) {
	driver, err := Resolve("bblabu")
	require.NoError(t, err)
	require.Equal(t, ProviderBBLabuV1, driver.ID())
	require.Equal(t, BillingOutputAndReference, driver.BillingPolicy())
	require.Equal(t, []string{"Seedance-2.0", "Seedance-2.5"}, driver.ModelIDs())

	createReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationCreate,
		CreateRequest: &CreateRequest{RequestedModel: ModelSeedance20, Model: ModelSeedance20, Prompt: "waves", Duration: 10, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.bblabu.ai/v1/videos", createReq.URL.String())
	require.Equal(t, http.MethodPost, createReq.Method)
	require.Equal(t, "Bearer test-key", createReq.Header.Get("Authorization"))
	statusReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationStatus, TaskID: "task/1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.bblabu.ai/v1/videos/task%2F1", statusReq.URL.String())
	_, err = driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationCancel, TaskID: "task-1",
	})
	require.ErrorContains(t, err, "does not support task cancellation")
	require.ErrorIs(t, err, ErrVideoTaskCancellationUnsupported)

	request, _, err := ParseCreateRequest([]byte(`{
		"model":"seedance-2.0","prompt":"waves","duration":10,"resolution":"4k",
		"aspect_ratio":"16:9","camera_control":{"type":"orbit"}
	}`))
	require.NoError(t, err)
	encodedReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(requestBody(t, encodedReq), &payload))
	require.Equal(t, "Seedance-2.0", payload["model"])
	require.Equal(t, "4K", payload["resolution"])
	require.Equal(t, "16:9", payload["ratio"])
	require.Equal(t, map[string]any{"type": "orbit"}, payload["camera_control"])
	require.NotContains(t, payload, "aspect_ratio")

	task, err := driver.ParseTask([]byte(`{
		"task_id":"task-1","status":"succeeded","model":"Seedance-2.5",
		"resolution":"720p","duration":30,"usage":{"reference_video_input_seconds":12}
	}`), "")
	require.NoError(t, err)
	require.Equal(t, Task{ID: "task-1", Status: StatusCompleted, Model: ModelSeedance25, Resolution: "720p", DurationSeconds: 30, ReferenceInputSeconds: 12}, task)
}

func TestDefaultModelIDsPreserveLegacyAliasesAndIncludeProviderExtensions(t *testing.T) {
	require.Equal(t, []string{
		"Seedance-2.0", "Seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini",
	}, DefaultModelIDs())
}

func TestRegistryResolvesAliasesAndReturnsImmutableMetadata(t *testing.T) {
	registry, err := NewRegistry(
		Registration{Driver: fflinkDriver{}, Aliases: []string{"async-jobs"}},
		Registration{Driver: bblabuDriver{}, Aliases: []string{"videos-v1"}},
	)
	require.NoError(t, err)
	driver, err := registry.Resolve("ASYNC-JOBS", ProviderBBLabuV1)
	require.NoError(t, err)
	require.Equal(t, ProviderFFLinkV1, driver.ID())
	require.Equal(t, []ID{ProviderBBLabuV1, ProviderFFLinkV1}, registry.ProviderIDs())

	descriptors := registry.Descriptors()
	require.Equal(t, ProviderBBLabuV1, descriptors[0].ID)
	descriptors[0].ModelIDs[0] = "mutated"
	require.NotEqual(t, "mutated", registry.Descriptors()[0].ModelIDs[0])
}

func TestRegistryRejectsConflictingProtocolAliases(t *testing.T) {
	_, err := NewRegistry(
		Registration{Driver: bblabuDriver{}, Aliases: []string{"shared"}},
		Registration{Driver: fflinkDriver{}, Aliases: []string{"shared"}},
	)
	require.ErrorContains(t, err, "already registered")
}

func TestProviderSpecificReferenceLimitsRemainIsolated(t *testing.T) {
	images := make([]any, 10)
	for index := range images {
		images[index] = fmt.Sprintf("https://cdn.example.com/%d.png", index)
	}
	body, err := json.Marshal(map[string]any{
		"model": ModelSeedance20, "prompt": "waves", "duration": 10,
		"resolution": "720p", "aspect_ratio": "16:9", "referenceImages": images,
	})
	require.NoError(t, err)
	request, _, err := ParseCreateRequest(body)
	require.NoError(t, err)

	bblabu, err := Resolve("bblabu_v1")
	require.NoError(t, err)
	_, err = bblabu.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.ErrorContains(t, err, "reference asset count")

	fflink, err := Resolve("fflink_v1")
	require.NoError(t, err)
	_, err = fflink.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.NoError(t, err)

	videos := make([]any, 4)
	for index := range videos {
		videos[index] = fmt.Sprintf("https://cdn.example.com/%d.mp4", index)
	}
	body, err = json.Marshal(map[string]any{
		"model": ModelSeedance25, "prompt": "waves", "duration": 10,
		"resolution": "720p", "aspect_ratio": "16:9", "referenceVideos": videos,
	})
	require.NoError(t, err)
	request, _, err = ParseCreateRequest(body)
	require.NoError(t, err)
	_, err = bblabu.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultBBLabuBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.NoError(t, err)
	_, err = fflink.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.ErrorContains(t, err, "reference asset count")
}

func TestFFLinkReferenceImagesDoNotIncludeStartAndEndFrames(t *testing.T) {
	images := make([]any, 12)
	for index := range images {
		images[index] = fmt.Sprintf("https://cdn.example.com/%d.png", index)
	}
	body, err := json.Marshal(map[string]any{
		"model": ModelSeedance20, "prompt": "waves", "duration": 10,
		"resolution": "720p", "aspect_ratio": "16:9", "referenceImages": images,
		"start_frame_url": "https://cdn.example.com/start.png",
		"end_frame_url":   "https://cdn.example.com/end.png",
	})
	require.NoError(t, err)
	request, _, err := ParseCreateRequest(body)
	require.NoError(t, err)
	fflink, err := Resolve("fflink_v1")
	require.NoError(t, err)
	_, err = fflink.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.NoError(t, err)
}

func TestFFLinkDriverContract(t *testing.T) {
	driver, err := Resolve("fflink")
	require.NoError(t, err)
	require.Equal(t, ProviderFFLinkV1, driver.ID())
	require.Equal(t, BillingOutputOnly, driver.BillingPolicy())
	require.True(t, driver.SupportsModel(ModelSeedance20Fast))
	require.False(t, driver.SupportsModel("kling-3.0"))

	createReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate,
		CreateRequest: &CreateRequest{RequestedModel: ModelSeedance20, Model: ModelSeedance20, Prompt: "waves", Duration: 4, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.fflink.top/v1/videos/generations", createReq.URL.String())
	statusReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationStatus, TaskID: "vidjob-1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.fflink.top/v1/videos/jobs/vidjob-1", statusReq.URL.String())
	contentReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationContent, TaskID: "vidjob-1",
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.fflink.top/v1/videos/jobs/vidjob-1/content", contentReq.URL.String())
	cancelReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCancel, TaskID: "vidjob-1",
	})
	require.NoError(t, err)
	require.Equal(t, statusReq.URL.String(), cancelReq.URL.String())
	require.Equal(t, http.MethodDelete, cancelReq.Method)
	require.Equal(t, "respond-async", createReq.Header.Get("Prefer"))

	request, _, err := ParseCreateRequest([]byte(`{
		"model":"seedance-2.0-fast","prompt":"waves","duration":4,"resolution":"720p",
		"aspect_ratio":"21:9","referenceImages":["https://cdn.example.com/a.png"]
	}`))
	require.NoError(t, err)
	encodedReq, err := driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(requestBody(t, encodedReq), &payload))
	require.Equal(t, ModelSeedance20Fast, payload["model"])
	require.Equal(t, "21:9", payload["aspect_ratio"])
	require.NotContains(t, payload, "referenceImages")
	require.NotNil(t, payload["guidances"])

	task, err := driver.ParseTask([]byte(`{"job_id":"vidjob-1","status":"running"}`), "")
	require.NoError(t, err)
	require.Equal(t, "vidjob-1", task.ID)
	require.Equal(t, StatusRunning, task.Status)

	models, err := driver.ParseModels([]byte(`{
		"data":[{"id":"seedance-2.0-fast"},{"id":"kling-3.0"},{"id":"seedance-2.0-fast"}]
	}`))
	require.NoError(t, err)
	require.Equal(t, []string{ModelSeedance20Fast}, models)
}

func TestFFLinkRejectsModelsWithoutLocalPricingCapability(t *testing.T) {
	driver, err := Resolve("fflink_v1")
	require.NoError(t, err)
	request, _, err := ParseCreateRequest([]byte(`{
		"model":"kling-3.0","prompt":"waves","duration":5,"resolution":"720p"
	}`))
	require.NoError(t, err)
	_, err = driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: DefaultFFLinkBaseURL, APIKey: "test-key", Operation: OperationCreate, CreateRequest: request,
	})
	require.ErrorContains(t, err, "unsupported video model")
}

func TestProviderRejectsUnsafeURLsAndTaskIDs(t *testing.T) {
	driver, err := Resolve("fflink_v1")
	require.NoError(t, err)
	_, err = driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: "https://api.example/v1?tenant=1", APIKey: "test-key", Operation: OperationCreate,
		CreateRequest: &CreateRequest{RequestedModel: ModelSeedance20, Model: ModelSeedance20, Prompt: "waves", Duration: 4, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.Error(t, err)
	_, err = driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: "http://api.example/v1", APIKey: "test-key", Operation: OperationCreate,
		CreateRequest: &CreateRequest{RequestedModel: ModelSeedance20, Model: ModelSeedance20, Prompt: "waves", Duration: 4, Resolution: "720p", AspectRatio: "16:9", Raw: map[string]any{}},
	})
	require.Error(t, err)
	_, err = driver.BuildRequest(context.Background(), RequestParams{
		BaseURL: "https://api.example/v1", APIKey: "test-key", Operation: OperationStatus, TaskID: "bad\nvalue",
	})
	require.Error(t, err)
}
