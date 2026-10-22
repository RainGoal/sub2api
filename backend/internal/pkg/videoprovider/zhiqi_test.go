package videoprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func zhiqiTestCapability() VideoModelCapability {
	return VideoModelCapability{
		Model: "your-video-model", Profile: ProfileZhiqiV1, Enabled: true,
		DurationMode: VideoDurationControlled, MinDuration: 1, MaxDuration: 15, DefaultDuration: 5,
		Resolutions: []string{"720p"}, DefaultResolution: "720p", ResolutionControl: true,
		Ratios: []string{"16:9"}, DefaultRatio: "16:9", MaxImages: 2, MaxVideos: 1, MaxAudios: 1,
		MaxTotalAssets: 6, Frames: true, AllowFirstOnly: true, AllowFramesWithReferences: true,
		GenerateAudio: true, BillingUnit: VideoBillingPerSecond, BillingPolicy: BillingOutputOnly,
		MeteringPolicy: VideoMeteringOutputSeconds, MeteringTrusted: true, ContentMode: "content_endpoint",
	}
}

func TestZhiqiDriverMapsUnifiedFieldsWithoutChangingTheirMeaning(t *testing.T) {
	c := zhiqiTestCapability()
	request := videoTasksTestRequest(t, `{"model":"your-video-model","prompt":"waves","duration":8,"image_url":"https://cdn.example/first.png","end_frame_url":"https://cdn.example/last.png","reference_images":["https://cdn.example/ref.png"],"reference_videos":["https://cdn.example/ref.mp4"],"reference_audios":["https://cdn.example/ref.mp3"],"audio":true}`, []VideoModelCapability{c})
	request.Raw = map[string]any{"input_reference": "https://cdn.example/must-not-leak.png", "secret": "must-not-leak", "n": 20}
	driver, err := Resolve("zhiqi")
	require.NoError(t, err)
	req, err := driver.BuildRequest(context.Background(), RequestParams{Operation: OperationCreate, APIKey: "key", CreateRequest: request})
	require.NoError(t, err)
	require.Equal(t, DefaultZhiqiBaseURL+"/videos", req.URL.String())
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "Bearer key", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(requestBody(t, req), &payload))
	require.Equal(t, map[string]any{
		"model": c.Model, "prompt": "waves", "n": float64(1), "duration": float64(8), "resolution": "720p",
		"aspect_ratio": "16:9", "generate_audio": true,
		"first_frame_image_url": "https://cdn.example/first.png", "last_frame_image_url": "https://cdn.example/last.png",
		"reference_image_urls": []any{"https://cdn.example/ref.png"},
		"reference_video_urls": []any{"https://cdn.example/ref.mp4"},
		"reference_audio_urls": []any{"https://cdn.example/ref.mp3"},
	}, payload)
}

func TestZhiqiDriverModelsAndTaskURLs(t *testing.T) {
	driver, err := Resolve(string(ProviderZhiqiV1))
	require.NoError(t, err)
	require.Empty(t, driver.ModelIDs())
	require.False(t, driver.SupportsModel("Seedance-2.0"))
	models, err := driver.ParseModels([]byte(`{"data":[{"id":"Brand/NewVideo"},{"id":"Brand/NewVideo"},{"id":"Other/Video"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Brand/NewVideo", "Other/Video"}, models)
	for _, operation := range []Operation{OperationStatus, OperationContent, OperationModels} {
		req, err := driver.BuildRequest(context.Background(), RequestParams{BaseURL: "https://api.example/", APIKey: "key", Operation: operation, TaskID: "vidtask_1"})
		require.NoError(t, err)
		paths := map[Operation]string{OperationStatus: "/v1/videos/vidtask_1", OperationContent: "/v1/videos/vidtask_1/content", OperationModels: "/v1/models"}
		require.Equal(t, paths[operation], req.URL.Path)
	}
	_, err = driver.BuildRequest(context.Background(), RequestParams{APIKey: "key", Operation: OperationCancel, TaskID: "vidtask_1"})
	require.ErrorIs(t, err, ErrVideoTaskCancellationUnsupported)
}

func TestZhiqiOfficialTaskFixturesAndMissingUsage(t *testing.T) {
	driver := zhiqiDriver{}
	queued, err := driver.ParseTask([]byte(`{"id":"vidtask_1","object":"video.task","status":"queued","created":1783425600,"model":"your-video-model"}`), "")
	require.NoError(t, err)
	require.Equal(t, StatusPending, queued.Status)
	completed, err := driver.ParseTask([]byte(`{"id":"vidtask_1","object":"video.task","status":"succeeded","created":1783425600,"completed_at":1783425660,"model":"your-video-model","progress":"100%","data":[{"url":"https://video-cdn.example.com/generated/result-1.mp4"}]}`), "")
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, completed.Status)
	require.Zero(t, completed.DurationSecondsExact)
	for _, status := range []string{"failed", "expired"} {
		task, err := driver.ParseTask([]byte(`{"id":"vidtask_1","status":"`+status+`","error":{"code":"media_storage_failed","message":"delivery failed"}}`), "")
		require.NoError(t, err)
		require.Equal(t, StatusFailed, task.Status)
	}
	task, err := driver.ParseTask([]byte(`{"id":"vidtask_1","status":"in_progress","progress":"99%"}`), "")
	require.NoError(t, err)
	require.Equal(t, StatusRunning, task.Status)
}

func TestZhiqiMeteringIsExactAndProtocolLocal(t *testing.T) {
	body := []byte(`{"id":"vidtask_1","status":"succeeded","duration":99,"seconds":"100","usage":{"duration_seconds":4.125,"total_cost":0.4}}`)
	task, err := (zhiqiDriver{}).ParseTask(body, "")
	require.NoError(t, err)
	require.Equal(t, 4.125, task.DurationSecondsExact)
	require.Zero(t, task.ReferenceInputSecondsExact)
	legacy, err := (videoTasksJSONDriver{}).ParseTask([]byte(`{"id":"vidtask_1","status":"succeeded","usage":{"duration_seconds":4.125}}`), "")
	require.NoError(t, err)
	require.Zero(t, legacy.DurationSecondsExact)
	for _, value := range []string{`"4"`, `-1`, `3601`, `true`, `{}`, `[]`, `1e999`} {
		_, err := (zhiqiDriver{}).ParseTask([]byte(`{"id":"vidtask_1","status":"succeeded","usage":{"duration_seconds":`+value+`}}`), "")
		require.Error(t, err, value)
	}
	task, err = (zhiqiDriver{}).ParseTask([]byte(`{"status":"succeeded","duration":8,"created":1,"completed_at":500}`), "vidtask_1")
	require.NoError(t, err)
	require.Equal(t, "vidtask_1", task.ID)
	require.Zero(t, task.DurationSecondsExact)
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"status":"queued"}`, `{"id":"x"} invalid`} {
		_, err := (zhiqiDriver{}).ParseTask([]byte(body), "")
		require.Error(t, err, body)
	}
}

func TestModelCapabilityDriversRejectForeignProfiles(t *testing.T) {
	require.True(t, SupportsModelCapabilities(ProviderVideoTasksJSONV1))
	require.True(t, SupportsModelCapabilities(ProviderZhiqiV1))
	require.False(t, SupportsModelCapabilities(ProviderBBLabuV1))
	require.False(t, SupportsModelCapabilities("unknown"))
	require.Equal(t, ProfileBBLabuRoute, DefaultCapabilityProfile(ProviderVideoTasksJSONV1))
	require.Equal(t, ProfileZhiqiV1, DefaultCapabilityProfile(ProviderZhiqiV1))
	require.Empty(t, DefaultCapabilityProfile(ProviderBBLabuV1))
	require.Empty(t, DefaultCapabilityProfile("unknown"))
	c := zhiqiTestCapability()
	require.NoError(t, ValidateCapabilityProvider(ProviderZhiqiV1, c))
	require.Error(t, ValidateCapabilityProvider(ProviderVideoTasksJSONV1, c))
	require.Error(t, ValidateCapabilityProvider(ProviderBBLabuV1, c))
	r := &CreateRequest{Model: c.Model, Prompt: "waves", Duration: 5, Resolution: "720p", AspectRatio: "16:9", Capability: &c}
	_, err := encodeVideoTasksCreate(r, nil)
	require.Error(t, err)
	c.Profile = ProfileBBLabuRoute
	r.Capability = &c
	_, err = encodeZhiqiCreate(r, nil)
	require.Error(t, err)
	c.Profile = ProfileZhiqiV1
	c.SupportsCancel = true
	require.Error(t, ValidateCapabilityProvider(ProviderZhiqiV1, c))
}

func TestZhiqiRejectsUnsupportedActualReferenceMetering(t *testing.T) {
	require.True(t, SupportsReferenceVideoMetering(ProviderVideoTasksJSONV1))
	for _, id := range []ID{ProviderZhiqiV1, ProviderBBLabuV1, ProviderFFLinkV1, "unknown"} {
		require.False(t, SupportsReferenceVideoMetering(id), id)
	}
	c := zhiqiTestCapability()
	c.BillingPolicy = BillingOutputAndReference
	c.MeteringPolicy = VideoMeteringOutputAndReferenceSeconds
	c.MaxReferenceVideoSeconds = 15
	require.NoError(t, ValidateVideoModelCapabilities([]VideoModelCapability{c}))
	require.ErrorContains(t, ValidateCapabilityProvider(ProviderZhiqiV1, c), "does not provide reference video metering")
	c.Profile = ProfileBBLabuRoute
	require.NoError(t, ValidateCapabilityProvider(ProviderVideoTasksJSONV1, c), "existing JSON reference metering must remain available")
	for _, change := range []func(*VideoModelCapability){
		func(c *VideoModelCapability) { c.BillingPolicy = BillingOutputAndReference },
		func(c *VideoModelCapability) { c.MeteringPolicy = VideoMeteringOutputAndReferenceSeconds },
	} {
		c := zhiqiTestCapability()
		change(&c)
		require.ErrorContains(t, (zhiqiDriver{}).ValidateCapability(c), "does not provide reference video metering")
	}
}

func TestCapabilityModelMappingOnlyChangesUpstreamWire(t *testing.T) {
	for _, profile := range []VideoFieldProfile{ProfileBBLabuOfficial, ProfileBBLabuRoute, ProfileZhiqiV1} {
		t.Run(string(profile), func(t *testing.T) {
			c := zhiqiTestCapability()
			c.Profile, c.Model, c.UpstreamModel = profile, "public-video", "Provider/PrivateVideo"
			r, info, err := ParseCreateRequestWithCapabilities([]byte(`{"model":"public-video","prompt":"waves"}`), []VideoModelCapability{c})
			require.NoError(t, err)
			require.Equal(t, c.Model, info.Model)
			require.Equal(t, c.Model, r.Model)
			var body []byte
			if profile == ProfileZhiqiV1 {
				body, err = encodeZhiqiCreate(r, nil)
			} else {
				body, err = encodeVideoTasksCreate(r, nil)
			}
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			require.Equal(t, c.UpstreamModel, payload["model"])
			require.Equal(t, c.Model, r.Model)
			for _, invalid := range []string{" ", " model", "model\r\n"} {
				c.UpstreamModel = invalid
				require.Error(t, ValidateVideoModelCapabilities([]VideoModelCapability{c}))
			}
		})
	}
}

func TestZhiqiContentFallbackIsStrictAndOptIn(t *testing.T) {
	driver, err := Resolve(string(ProviderZhiqiV1))
	require.NoError(t, err)
	parser, ok := driver.(ContentURLParser)
	require.True(t, ok)
	contentURL, err := parser.ParseContentURL([]byte(`{"url":"https://video-cdn.example.com/generated/result-1.mp4","content_type":"video/mp4","error":null}`))
	require.NoError(t, err)
	require.Equal(t, "https://video-cdn.example.com/generated/result-1.mp4", contentURL)
	for _, body := range []string{
		`{"url":"https://cdn.example/video.mp4","error":{"type":"media_storage_failed"}}`,
		`{"error":"failed"}`, `{"url":4}`, `{"data":[{"url":"https://cdn.example/a.mp4"}]}`,
		`{"url":"http://cdn.example/a.mp4"}`, `{"url":"https://127.0.0.1/a.mp4"}`,
		`{"url":"https://192.168.1.1/a.mp4"}`, `{"url":"https://localhost/a.mp4"}`,
		`{"url":"https://user:password@cdn.example/a.mp4"}`,
		`{"url":"https://cdn.example/a.mp4"} {}`, `null`, `[]`,
	} {
		_, err := parser.ParseContentURL([]byte(body))
		require.Error(t, err, body)
	}
	for _, id := range []ID{ProviderBBLabuV1, ProviderFFLinkV1, ProviderVideoTasksJSONV1} {
		driver, err := Resolve(string(id))
		require.NoError(t, err)
		_, ok := driver.(ContentURLParser)
		require.False(t, ok, id)
	}
}
