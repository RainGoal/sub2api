package videoprovider

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func videoTasksTestRequest(t *testing.T, body string, overrides []VideoModelCapability) *CreateRequest {
	t.Helper()
	request, _, err := ParseCreateRequestWithCapabilities([]byte(body), overrides)
	require.NoError(t, err)
	return request
}

func videoTasksTestPayload(t *testing.T, request *CreateRequest) map[string]any {
	t.Helper()
	driver, err := Resolve(string(ProviderVideoTasksJSONV1))
	require.NoError(t, err)
	req, err := driver.BuildRequest(context.Background(), RequestParams{BaseURL: DefaultBBLabuBaseURL, APIKey: "test", Operation: OperationCreate, CreateRequest: request})
	require.NoError(t, err)
	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	return payload
}

func TestVideoTasksBBLabuPresetsAreCompleteAndIsolated(t *testing.T) {
	capabilities := BBLabuModelCapabilities()
	require.Len(t, capabilities, 33)
	require.NoError(t, ValidateVideoModelCapabilities(capabilities))
	for _, capability := range capabilities {
		t.Run(capability.Model, func(t *testing.T) {
			require.False(t, capability.SupportsCancel)
			body, err := json.Marshal(map[string]any{"model": capability.Model, "prompt": "a bird"})
			require.NoError(t, err)
			request, info, err := ParseCreateRequestWithCapabilities(body, nil)
			require.NoError(t, err)
			require.Equal(t, capability.BillingUnit, info.BillingUnit)
			payload := videoTasksTestPayload(t, request)
			require.Equal(t, capability.Model, payload["model"])
			if capability.DurationMode == VideoDurationUnknown {
				require.NotContains(t, payload, "duration")
			} else {
				require.EqualValues(t, capability.DefaultDuration, payload["duration"])
			}
			if !capability.ResolutionControl {
				require.NotContains(t, payload, "resolution")
			}
		})
	}
	capabilities[0].Ratios[0] = "invalid"
	capability, _ := LookupVideoModelCapability(capabilities[0].Model, nil)
	require.NotContains(t, capability.Ratios, "invalid")
	legacy, ok := LookupModel(ModelSeedance20)
	require.True(t, ok)
	require.Equal(t, 4, legacy.MinDuration)
	legacyDriver, err := Resolve("bblabu_v1")
	require.NoError(t, err)
	require.Equal(t, []string{"Seedance-2.0", "Seedance-2.5"}, legacyDriver.ModelIDs())
	require.Len(t, DefaultModelIDs(), 4)
}

func TestVideoTasksFieldProfilesBuildOnlyDefinedFields(t *testing.T) {
	official := videoTasksTestRequest(t, `{"model":"Seedance-2.0","prompt":"a bird","ratio":"Auto","first_image":"https://cdn.example/first.png"}`, nil)
	official.Raw = map[string]any{"internal_secret": "must-not-leak", "n": 20, "guidances": "unsupported"}
	payload := videoTasksTestPayload(t, official)
	require.Equal(t, "Auto", payload["ratio"])
	require.Equal(t, "https://cdn.example/first.png", payload["first_image"])
	for _, key := range []string{"internal_secret", "n", "guidances", "aspect_ratio", "capability", "raw"} {
		require.NotContains(t, payload, key)
	}
	route := videoTasksTestRequest(t, `{"model":"sd16-seedance-2.0","prompt":"a bird","resolution":"4k","referenceImages":["https://cdn.example/ref.png"],"generate_audio":true,"first_image_url":"https://cdn.example/first.png","last_image_url":"https://cdn.example/last.png"}`, nil)
	payload = videoTasksTestPayload(t, route)
	require.Equal(t, "4k", payload["resolution"])
	require.Equal(t, "16:9", payload["aspect_ratio"])
	require.Equal(t, true, payload["generate_audio"])
	require.Equal(t, []any{"https://cdn.example/ref.png"}, payload["reference_image_urls"])
	for _, key := range []string{"ratio", "referenceImages", "first_image", "last_image"} {
		require.NotContains(t, payload, key)
	}
}

func TestVideoTasksRejectsInvalidInputsAndCombinations(t *testing.T) {
	tests := map[string]string{
		"fractional duration":   `{"model":"Seedance-2.0","prompt":"x","duration":5.5}`,
		"string duration":       `{"model":"Seedance-2.0","prompt":"x","duration":"5"}`,
		"out of bounds":         `{"model":"Seedance-2.0","prompt":"x","duration":16}`,
		"unknown model":         `{"model":"new-upstream-only","prompt":"x"}`,
		"unsupported field":     `{"model":"Seedance-2.0","prompt":"x","watermark":false}`,
		"invalid bool":          `{"model":"kling-3.0","prompt":"x","audio":1}`,
		"conflicting aliases":   `{"model":"Seedance-2.0","prompt":"x","ratio":"Auto","aspect_ratio":"16:9"}`,
		"non-string asset":      `{"model":"Seedance-2.0","prompt":"x","referenceImages":[2]}`,
		"private asset":         `{"model":"Seedance-2.0","prompt":"x","referenceImages":["https://127.0.0.1/a.png"]}`,
		"frame auto":            `{"model":"Seedance-2.0","prompt":"x","first_image":"https://cdn.example/a.png"}`,
		"frames and refs":       `{"model":"Seedance-2.0","prompt":"x","ratio":"Auto","first_image":"https://cdn.example/a.png","referenceImages":["https://cdn.example/b.png"]}`,
		"tail without first":    `{"model":"sd14-seedance-2.0","prompt":"x","last_image":"https://cdn.example/a.png"}`,
		"paired frames":         `{"model":"sd16-seedance-2.0","prompt":"x","first_image":"https://cdn.example/a.png"}`,
		"audio reference":       `{"model":"niulai-pro","prompt":"x","reference_audios":["https://cdn.example/a.mp3"]}`,
		"unknown duration":      `{"model":"omni-fast","prompt":"x","duration":10}`,
		"fixed duration":        `{"model":"sd8-seedance-2.5-s","prompt":"x","duration":15}`,
		"discrete duration":     `{"model":"sd17-seedance-2.0-3","prompt":"x","duration":7}`,
		"no resolution control": `{"model":"kling-3.0","prompt":"x","resolution":"720p"}`,
		"unknown mapping":       `{"model":"mm3-minimax-h3-2k","prompt":"x","reference_videos":["https://cdn.example/a.mp4"],"reference_video_durations":[5]}`,
		"missing metadata":      `{"model":"sd14-seedance-2.0","prompt":"x","reference_videos":["https://cdn.example/a.mp4"]}`,
		"combined seconds":      `{"model":"sd14-seedance-2.0","prompt":"x","duration":15,"reference_videos":["https://cdn.example/a.mp4"],"reference_video_durations":[11]}`,
		"multi n":               `{"model":"Seedance-2.0","prompt":"x","n":2}`,
		"trailing JSON":         `{"model":"Seedance-2.0","prompt":"x"} {}`,
		"invalid trailing":      `{"model":"Seedance-2.0","prompt":"x"} invalid`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseCreateRequestWithCapabilities([]byte(body), nil)
			require.Error(t, err)
		})
	}
	request := videoTasksTestRequest(t, `{"model":"sd14-seedance-2.0","prompt":"x","duration":15,"reference_videos":["https://cdn.example/a.mp4"],"reference_video_durations":[10]}`, nil)
	require.NotContains(t, videoTasksTestPayload(t, request), "reference_video_durations")
}

func TestVideoTasksPreservesPublicInputsForAccountRevalidation(t *testing.T) {
	request := videoTasksTestRequest(t, `{"model":"sd14-seedance-2.0","prompt":"waves","duration":15,"reference_videos":["https://cdn.example/ref.mp4"],"reference_video_durations":[10]}`, nil)
	require.Equal(t, "waves", request.Raw["prompt"])
	require.Equal(t, json.Number("15"), request.Raw["duration"])
	require.Equal(t, []any{"https://cdn.example/ref.mp4"}, request.Raw["reference_videos"])
	require.Equal(t, []any{json.Number("10")}, request.Raw["reference_video_durations"])
	body, err := json.Marshal(request.Raw)
	require.NoError(t, err)
	revalidated, _, err := ParseCreateRequestWithCapabilities(body, []VideoModelCapability{*request.Capability})
	require.NoError(t, err)
	require.Equal(t, request.Prompt, revalidated.Prompt)
	require.Equal(t, request.ReferenceVideos, revalidated.ReferenceVideos)
	require.Equal(t, request.ReferenceVideoDurations, revalidated.ReferenceVideoDurations)
	require.NotContains(t, videoTasksTestPayload(t, revalidated), "raw")
	require.NotContains(t, videoTasksTestPayload(t, revalidated), "reference_video_durations")
}

func TestVideoTasksAccountCapabilitiesAreValidatedAndFrozen(t *testing.T) {
	custom, ok := LookupVideoModelCapability("grok-imagine-video", nil)
	require.True(t, ok)
	custom.Model = "Custom/Video-CaseSensitive"
	custom.SupportsCancel = true
	custom.Profile = ProfileBBLabuOfficial
	overrides := []VideoModelCapability{custom}
	parsed, err := ParseVideoModelCapabilities(overrides)
	require.NoError(t, err)
	request := videoTasksTestRequest(t, `{"model":"Custom/Video-CaseSensitive","prompt":"x"}`, parsed)
	require.Equal(t, custom.Model, videoTasksTestPayload(t, request)["model"])
	parsed[0].Profile = ProfileBBLabuRoute
	require.Equal(t, ProfileBBLabuOfficial, request.Capability.Profile)
	request.Capability.MaxDuration = 2
	_, err = encodeVideoTasksCreate(request, nil)
	require.Error(t, err)
	custom.Enabled = false
	_, _, err = ParseCreateRequestWithCapabilities([]byte(`{"model":"Custom/Video-CaseSensitive","prompt":"x"}`), []VideoModelCapability{custom})
	require.Error(t, err)
	_, err = ParseVideoModelCapabilities([]any{map[string]any{"model": "custom", "unknown_key": true}})
	require.Error(t, err)
	custom.Enabled = true
	require.Error(t, ValidateVideoModelCapabilities([]VideoModelCapability{custom, custom}))
	custom.DurationMode = VideoDurationUnknown
	require.Error(t, ValidateVideoModelCapabilities([]VideoModelCapability{custom}))
}

func TestVideoTasksURLsDiscoveryAndCancellation(t *testing.T) {
	driver, err := Resolve(string(ProviderVideoTasksJSONV1))
	require.NoError(t, err)
	for _, input := range []string{"", "http://api.example/v1", "https://user@api.example/v1", "https://api.example/v1?key=secret", "https://api.example/v1/videos", "https://api.example/a/../v1", "https://api.example/a//v1"} {
		_, err := NormalizeVideoTasksBaseURL(input)
		require.Error(t, err, input)
	}
	base, err := NormalizeVideoTasksBaseURL(" https://api.example/ ")
	require.NoError(t, err)
	require.Equal(t, "https://api.example/v1", base)
	base, err = NormalizeVideoTasksBaseURL("https://api.example/prefix/v2/")
	require.NoError(t, err)
	require.Equal(t, "https://api.example/prefix/v2", base)
	models, err := driver.ParseModels([]byte(`{"data":[{"id":"Brand/NewVideo"},{"id":"Seedance-2.0"},{"id":"brand/newvideo"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Brand/NewVideo", "Seedance-2.0"}, models)
	for _, operation := range []Operation{OperationModels, OperationStatus, OperationContent} {
		req, err := driver.BuildRequest(context.Background(), RequestParams{BaseURL: "https://api.example", APIKey: "key", Operation: operation, TaskID: "task/one"})
		require.NoError(t, err)
		require.Equal(t, "Bearer key", req.Header.Get("Authorization"))
		if operation == OperationModels {
			require.Equal(t, "https://api.example/v1/models", req.URL.String())
		} else {
			require.Contains(t, req.URL.String(), "/videos/task%2Fone")
		}
	}
	_, err = driver.BuildRequest(context.Background(), RequestParams{BaseURL: DefaultBBLabuBaseURL, APIKey: "key", Operation: OperationCancel, TaskID: "task"})
	require.ErrorIs(t, err, ErrVideoTaskCancellationUnsupported)
	capability, _ := LookupVideoModelCapability("Seedance-2.0", nil)
	_, err = driver.BuildRequest(context.Background(), RequestParams{BaseURL: DefaultBBLabuBaseURL, APIKey: "key", Operation: OperationCancel, TaskID: "task", Capability: &capability})
	require.ErrorIs(t, err, ErrVideoTaskCancellationUnsupported)
	capability.SupportsCancel = true
	req, err := driver.BuildRequest(context.Background(), RequestParams{BaseURL: "https://custom.example/v1", APIKey: "key", Operation: OperationCancel, TaskID: "task", Capability: &capability})
	require.NoError(t, err)
	require.Equal(t, "DELETE", req.Method)
}

func TestVideoTasksTaskParsingPreservesFractionalMetering(t *testing.T) {
	driver, err := Resolve(string(ProviderVideoTasksJSONV1))
	require.NoError(t, err)
	task, err := driver.ParseTask([]byte(`{"data":{"id":"raw-task","status":"completed","duration":5.125,"usage":{"reference_video_input_seconds":2.75}}}`), "")
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, task.Status)
	require.Equal(t, 5.125, task.DurationSecondsExact)
	require.Equal(t, 2.75, task.ReferenceInputSecondsExact)
	for _, body := range []string{`{"id":"t","duration":-1}`, `{"id":"t","duration":"5.5"}`, `{"id":"t","usage":{"reference_video_seconds":1e1000}}`, `{"status":"completed"}`, `{"id":"t"} {}`} {
		_, err := driver.ParseTask([]byte(body), "")
		require.Error(t, err, body)
	}
	task, err = driver.ParseTask([]byte(`{"status":"failure"}`), "fallback")
	require.NoError(t, err)
	require.Equal(t, "fallback", task.ID)
	require.Equal(t, StatusFailed, task.Status)
}
