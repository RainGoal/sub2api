package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSeedanceVideoStatus(t *testing.T) {
	tests := map[string]string{
		"queued":      SeedanceVideoResponseStatusQueued,
		"pending":     SeedanceVideoResponseStatusQueued,
		"running":     SeedanceVideoResponseStatusInProgress,
		"in_progress": SeedanceVideoResponseStatusInProgress,
		"processing":  SeedanceVideoResponseStatusInProgress,
		"settling":    SeedanceVideoResponseStatusInProgress,
		"completed":   SeedanceVideoResponseStatusCompleted,
		"succeeded":   SeedanceVideoResponseStatusCompleted,
		"success":     SeedanceVideoResponseStatusCompleted,
		"failed":      SeedanceVideoResponseStatusFailed,
		"error":       SeedanceVideoResponseStatusFailed,
		"canceled":    SeedanceVideoResponseStatusCanceled,
		"cancelled":   SeedanceVideoResponseStatusCanceled,
		"":            SeedanceVideoResponseStatusInProgress,
		"unknown":     SeedanceVideoResponseStatusInProgress,
	}
	for input, want := range tests {
		require.Equal(t, want, NormalizeSeedanceVideoStatus(input), input)
	}
	require.Equal(t, SeedanceVideoResponseStatusCompleted, NormalizeSeedanceVideoStatus(" SUCCESS "))
}

func TestBuildSeedanceVideoResponseUsesMetaAndKeepsNullableFields(t *testing.T) {
	providerResult := &SeedanceVideoForwardResult{
		OpenAIForwardResult: &OpenAIForwardResult{
			ResponseID:           "provider-task",
			Model:                "Seedance-2.0",
			VideoResolution:      "480p",
			VideoDurationSeconds: 4,
		},
		TaskStatus: "pending",
	}
	response := BuildSeedanceVideoResponse(providerResult, SeedanceVideoResponseMeta{
		ID:         "local-task",
		Model:      "Seedance-2.5",
		Resolution: "4K",
		Duration:   10,
	})

	require.Equal(t, SeedanceVideoResponse{
		ID:         "local-task",
		Object:     "video",
		Status:     SeedanceVideoResponseStatusQueued,
		Model:      "seedance-2.5",
		Resolution: "4k",
		Duration:   10,
		ContentURL: nil,
		Error:      nil,
	}, response)

	body, err := json.Marshal(response)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"local-task","object":"video","status":"queued","model":"seedance-2.5","resolution":"4k","duration":10,"content_url":null,"error":null}`, string(body))
}

func TestBuildSeedanceVideoResponseBuildsTaskErrorAndFallsBackToResult(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{
		OpenAIForwardResult: &OpenAIForwardResult{
			ResponseID:           "provider-task",
			Model:                "Seedance-2.0",
			VideoResolution:      "720p",
			VideoDurationSeconds: 8,
		},
		TaskStatus: "failed",
	}, SeedanceVideoResponseMeta{
		ErrorCode:    "video_generation_failed",
		ErrorMessage: "generation failed",
	})

	require.Equal(t, "provider-task", response.ID)
	require.Equal(t, SeedanceVideoResponseStatusFailed, response.Status)
	require.Equal(t, "seedance-2.0", response.Model)
	require.Equal(t, "720p", response.Resolution)
	require.Equal(t, 8, response.Duration)
	require.Equal(t, &SeedanceVideoResponseError{Code: "video_generation_failed", Message: "generation failed"}, response.Error)
}

func TestBuildSeedanceVideoResponseAlwaysIncludesDefaultTaskError(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{TaskStatus: "failed"}, SeedanceVideoResponseMeta{})

	require.Equal(t, SeedanceVideoResponseStatusFailed, response.Status)
	require.Equal(t, &SeedanceVideoResponseError{
		Code: "video_generation_failed", Message: "Video generation failed",
	}, response.Error)
}

func TestBuildSeedanceVideoResponseAcceptsCanonicalContentURLForCompletedTask(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{TaskStatus: "completed"}, SeedanceVideoResponseMeta{
		ID:               "task-1",
		Model:            "seedance-2.5",
		Resolution:       "720p",
		Duration:         12,
		SettlementStatus: SeedanceVideoSettlementSettled,
		ContentURL:       "/v1/videos/task-1/content",
	})

	require.NotNil(t, response.ContentURL)
	require.Equal(t, "/v1/videos/task-1/content", *response.ContentURL)
	require.Equal(t, SeedanceVideoResponseStatusCompleted, response.Status)
}

func TestBuildSeedanceVideoResponseRejectsProviderContentURL(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{TaskStatus: "completed"}, SeedanceVideoResponseMeta{
		ID:               "task-1",
		SettlementStatus: SeedanceVideoSettlementSettled,
		ContentURL:       "https://provider.example/videos/task-1/content",
	})

	require.Equal(t, SeedanceVideoResponseStatusInProgress, response.Status)
	require.Nil(t, response.ContentURL)
}

func TestBuildSeedanceVideoResponseDoesNotAdvertiseCompletedWithoutContentURL(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{
		OpenAIForwardResult: &OpenAIForwardResult{ResponseID: "task-1"},
		TaskStatus:          SeedanceVideoResponseStatusCompleted,
	}, SeedanceVideoResponseMeta{ID: "task-1", SettlementStatus: SeedanceVideoSettlementSettled})

	require.Equal(t, SeedanceVideoResponseStatusInProgress, response.Status)
	require.Nil(t, response.ContentURL)
	require.Nil(t, response.Error)
}

func TestBuildSeedanceVideoResponseDowngradesUnsettledCompletion(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{TaskStatus: "completed"}, SeedanceVideoResponseMeta{
		ID:               "task-1",
		SettlementStatus: SeedanceVideoSettlementProcessing,
		ContentURL:       "/v1/videos/task-1/content",
	})

	require.Equal(t, SeedanceVideoResponseStatusInProgress, response.Status)
	require.Nil(t, response.ContentURL)
	require.Nil(t, response.Error)
}

func TestBuildSeedanceVideoResponseRejectsTerminalFieldsForNonMatchingStatus(t *testing.T) {
	response := BuildSeedanceVideoResponse(&SeedanceVideoForwardResult{TaskStatus: "running"}, SeedanceVideoResponseMeta{
		ID: "task-1", ContentURL: "/v1/videos/task-1/content",
		ErrorCode: "video_generation_failed", ErrorMessage: "failed",
	})

	require.Equal(t, SeedanceVideoResponseStatusInProgress, response.Status)
	require.Nil(t, response.ContentURL)
	require.Nil(t, response.Error)
}
