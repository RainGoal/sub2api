package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

// The public response must be independent of the provider wire protocol. In
// particular, task_id and job_id are input details and must never become
// distinct response schemas for callers.
func TestSeedanceVideoResponseIsIdenticalAcrossProviderTaskIDProtocols(t *testing.T) {
	meta := SeedanceVideoResponseMeta{
		ID:         "sdv-public-123",
		Model:      "Seedance-2.5",
		Resolution: "720P",
		Duration:   10,
	}
	cases := []struct {
		name   string
		driver videoprovider.Driver
		body   string
	}{
		{
			name:   "bblabu task_id",
			driver: mustSeedanceResponseDriver(t, videoprovider.ProviderBBLabuV1),
			body:   `{"task_id":"provider-task-1","status":"queued","model":"Seedance-2.5","resolution":"720p","duration":10}`,
		},
		{
			name:   "fflink job_id",
			driver: mustSeedanceResponseDriver(t, videoprovider.ProviderFFLinkV1),
			body:   `{"job_id":"provider-job-1","status":"pending","model":"seedance-2.5","resolution":"720p","duration":10}`,
		},
	}

	var first SeedanceVideoResponse
	for index, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			task, err := tc.driver.ParseTask([]byte(tc.body), "")
			require.NoError(t, err)
			result := &SeedanceVideoForwardResult{
				OpenAIForwardResult: seedanceForwardResultFromTask(task),
				TaskStatus:          string(task.Status),
			}
			got := BuildSeedanceVideoResponse(result, meta)

			require.Equal(t, "sdv-public-123", got.ID)
			require.Equal(t, SeedanceVideoResponseStatusQueued, got.Status)
			require.Equal(t, "seedance-2.5", got.Model)
			require.Equal(t, "720p", got.Resolution)
			require.Equal(t, 10, got.Duration)
			require.Nil(t, got.ContentURL)
			require.Nil(t, got.Error)

			if index == 0 {
				first = got
			} else {
				require.Equal(t, first, got)
			}

			body, err := json.Marshal(got)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &fields))
			require.ElementsMatch(t,
				[]string{"id", "object", "status", "model", "resolution", "duration", "content_url", "error"},
				seedanceVideoResponseMapKeys(fields),
			)
			require.NotContains(t, fields, "task_id")
			require.NotContains(t, fields, "job_id")
			require.NotContains(t, string(body), "bblabu")
			require.NotContains(t, string(body), "fflink")
		})
	}
}

func TestSeedanceVideoResponseTerminalStatesRemainProviderNeutral(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     string
		wantStatus string
		wantURL    bool
		wantError  bool
	}{
		{name: "completed", status: "succeeded", wantStatus: SeedanceVideoResponseStatusCompleted, wantURL: true},
		{name: "failed", status: "error", wantStatus: SeedanceVideoResponseStatusFailed, wantError: true},
		{name: "canceled", status: "cancelled", wantStatus: SeedanceVideoResponseStatusCanceled},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := &SeedanceVideoForwardResult{
				OpenAIForwardResult: &OpenAIForwardResult{ResponseID: "provider-id"},
				TaskStatus:          tc.status,
			}
			meta := SeedanceVideoResponseMeta{ID: "sdv-terminal"}
			if tc.wantURL {
				meta.ContentURL = "/v1/videos/sdv-terminal/content"
			}
			if tc.wantError {
				meta.ErrorCode = "video_generation_failed"
				meta.ErrorMessage = "Video generation failed"
			}
			got := BuildSeedanceVideoResponse(result, meta)
			require.Equal(t, tc.wantStatus, got.Status)
			require.Equal(t, tc.wantURL, got.ContentURL != nil)
			require.Equal(t, tc.wantError, got.Error != nil)
		})
	}
}

func mustSeedanceResponseDriver(t *testing.T, id videoprovider.ID) videoprovider.Driver {
	t.Helper()
	driver, err := videoprovider.Resolve(string(id))
	require.NoError(t, err)
	return driver
}

func seedanceVideoResponseMapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
