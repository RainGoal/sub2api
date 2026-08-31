package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWriteSeedanceVideoResponseWritesCanonicalJSONOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	result := &service.SeedanceVideoForwardResult{
		OpenAIForwardResult: &service.OpenAIForwardResult{
			ResponseID:           "provider-task-1",
			Model:                "Seedance-2.5",
			VideoResolution:      "720P",
			VideoDurationSeconds: 10,
		},
		TaskStatus: "pending",
	}
	meta := service.SeedanceVideoResponseMeta{
		ID:         "sdv-public-1",
		Model:      "Seedance-2.5",
		Resolution: "720p",
		Duration:   10,
	}

	writeSeedanceVideoResponse(c, http.StatusAccepted, result, meta)
	firstBody := recorder.Body.String()
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"id":"sdv-public-1","object":"video","status":"queued","model":"seedance-2.5","resolution":"720p","duration":10,"content_url":null,"error":null}`, firstBody)
	require.True(t, service.IsResponseCommitted(c))

	// A later code path (for example billing/recovery) must not append another
	// JSON object or overwrite the original HTTP status.
	writeSeedanceVideoResponse(c, http.StatusOK, result, service.SeedanceVideoResponseMeta{ID: "sdv-rewritten"})
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, firstBody, recorder.Body.String())
}

func TestSeedanceVideoResponseMetaUsesLocalSnapshotAndTerminalFields(t *testing.T) {
	requestInfo := service.SeedanceVideoRequestInfo{
		Model:           "Seedance-2.0",
		Resolution:      "1080p",
		DurationSeconds: 8,
	}
	pending := &service.SeedanceVideoPendingBilling{
		Model:           "Seedance-2.5",
		Resolution:      "720p",
		DurationSeconds: 12,
	}

	queued := seedanceVideoResponseMeta("sdv-queued", &service.SeedanceVideoForwardResult{
		TaskStatus: "pending",
	}, pending, requestInfo)
	require.Equal(t, "Seedance-2.5", queued.Model)
	require.Equal(t, "720p", queued.Resolution)
	require.Equal(t, 12, queued.Duration)
	require.Empty(t, queued.ContentURL)
	require.Empty(t, queued.ErrorCode)

	completed := seedanceVideoResponseMeta("sdv-completed", &service.SeedanceVideoForwardResult{
		TaskStatus: "succeeded",
	}, &service.SeedanceVideoPendingBilling{
		Model: "Seedance-2.5", Resolution: "720p", DurationSeconds: 12,
		SettlementStatus: service.SeedanceVideoSettlementSettled,
	}, requestInfo)
	require.Equal(t, "/v1/videos/sdv-completed/content", completed.ContentURL)
	require.Empty(t, completed.ErrorCode)

	unsettled := seedanceVideoResponseMeta("sdv-unsettled", &service.SeedanceVideoForwardResult{
		TaskStatus: "completed",
	}, pending, requestInfo)
	require.Empty(t, unsettled.ContentURL)

	failed := seedanceVideoResponseMeta("sdv-failed", &service.SeedanceVideoForwardResult{
		TaskStatus: "error",
	}, pending, requestInfo)
	require.Empty(t, failed.ContentURL)
	require.Equal(t, "video_generation_failed", failed.ErrorCode)
	require.NotEmpty(t, failed.ErrorMessage)

	cancellationFailed := seedanceVideoResponseMeta("sdv-cancel-failed", &service.SeedanceVideoForwardResult{
		TaskStatus: service.SeedanceVideoResponseStatusFailed,
	}, pending, requestInfo)
	cancellationFailed.ErrorCode = service.SeedanceVideoResponseErrorCodeCancellationFailed
	cancellationFailed.ErrorMessage = service.SeedanceVideoResponseErrorMessageCancellationFailed
	response := service.BuildSeedanceVideoResponse(&service.SeedanceVideoForwardResult{
		TaskStatus: service.SeedanceVideoResponseStatusFailed,
	}, cancellationFailed)
	require.Equal(t, service.SeedanceVideoResponseErrorCodeCancellationFailed, response.Error.Code)
	require.Equal(t, service.SeedanceVideoResponseErrorMessageCancellationFailed, response.Error.Message)
}

func TestSeedanceVideoLocalTerminalResultPreventsProviderStateRegression(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{
		Model: "Seedance-2.5", Resolution: "720p", DurationSeconds: 10,
		UpstreamStatus:   service.SeedanceVideoResponseStatusCanceled,
		SettlementStatus: service.SeedanceVideoSettlementReleased,
	}
	result, ok := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-canceled", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusCanceled, result.TaskStatus)
	require.Equal(t, 0, result.VideoCount)

	response := service.BuildSeedanceVideoResponse(result, seedanceVideoResponseMeta("task-canceled", result, pending, service.SeedanceVideoRequestInfo{}))
	require.Equal(t, service.SeedanceVideoResponseStatusCanceled, response.Status)
	require.Nil(t, response.ContentURL)

	completedPending := *pending
	completedPending.UpstreamStatus = service.SeedanceVideoResponseStatusCompleted
	completedPending.SettlementStatus = service.SeedanceVideoSettlementSettled
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-completed", &completedPending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusCompleted, result.TaskStatus)
	require.Equal(t, 1, result.VideoCount)
}

func TestSeedanceVideoLocalTerminalResultOnlyMakesCanceledCancelIdempotent(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{
		UpstreamStatus:   service.SeedanceVideoResponseStatusCanceled,
		SettlementStatus: service.SeedanceVideoSettlementReleased,
	}
	result, ok := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-canceled", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusCanceled, result.TaskStatus)

	pending.UpstreamStatus = service.SeedanceVideoResponseStatusFailed
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-failed", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusFailed, result.TaskStatus)

	pending.UpstreamStatus = service.SeedanceVideoResponseStatusCompleted
	pending.SettlementStatus = service.SeedanceVideoSettlementSettled
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-completed", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusCompleted, result.TaskStatus)

	pending.SettlementStatus = service.SeedanceVideoSettlementPending
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-awaiting-settlement", pending)
	require.False(t, ok)
	require.Nil(t, result)
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-awaiting-settlement", pending)
	require.False(t, ok)
	require.Nil(t, result)
}

func TestSeedanceVideoLocalTerminalResultDoesNotShortCircuitPendingRelease(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{
		UpstreamStatus:   service.SeedanceVideoResponseStatusFailed,
		SettlementStatus: service.SeedanceVideoSettlementPending,
	}

	result, ok := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-failed-pending", pending)
	require.False(t, ok)
	require.Nil(t, result)
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-failed-pending", pending)
	require.False(t, ok)
	require.Nil(t, result)
}

func TestSeedanceVideoLocalTerminalResultTreatsLegacyCancelRequestAsCanceled(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{
		UpstreamStatus:   service.SeedanceVideoCancellationRequestedStatus,
		SettlementStatus: service.SeedanceVideoSettlementReleased,
	}
	result, ok := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-legacy-cancel", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusCanceled, result.TaskStatus)
}

func TestSeedanceVideoContentAvailabilityTracksSettlementState(t *testing.T) {
	cases := []struct {
		name        string
		status      string
		settlement  string
		unavailable bool
	}{
		{name: "completed while settlement processing", status: service.SeedanceVideoResponseStatusCompleted, settlement: service.SeedanceVideoSettlementProcessing, unavailable: true},
		{name: "cancellation processing", status: service.SeedanceVideoCancellationRequestedStatus, settlement: service.SeedanceVideoSettlementProcessing, unavailable: true},
		{name: "completed pending billing", status: service.SeedanceVideoResponseStatusCompleted, settlement: service.SeedanceVideoSettlementPending, unavailable: true},
		{name: "queued pending billing", status: service.SeedanceVideoResponseStatusQueued, settlement: service.SeedanceVideoSettlementPending, unavailable: true},
		{name: "running pending billing", status: service.SeedanceVideoResponseStatusInProgress, settlement: service.SeedanceVideoSettlementPending, unavailable: true},
		{name: "settled completed", status: service.SeedanceVideoResponseStatusCompleted, settlement: service.SeedanceVideoSettlementSettled, unavailable: false},
		{name: "settled legacy running status", status: service.SeedanceVideoResponseStatusInProgress, settlement: service.SeedanceVideoSettlementSettled, unavailable: false},
		{name: "released failed", status: service.SeedanceVideoResponseStatusFailed, settlement: service.SeedanceVideoSettlementReleased, unavailable: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unavailable, _, _ := seedanceVideoContentUnavailable(&service.SeedanceVideoPendingBilling{
				UpstreamStatus: tc.status, SettlementStatus: tc.settlement,
			})
			require.Equal(t, tc.unavailable, unavailable)
		})
	}
}

func TestSeedanceVideoLocalTerminalResultBlocksConcurrentFinalization(t *testing.T) {
	pending := &service.SeedanceVideoPendingBilling{
		Model: "Seedance-2.5", Resolution: "720p", DurationSeconds: 10,
		UpstreamStatus:   service.SeedanceVideoResponseStatusInProgress,
		SettlementStatus: service.SeedanceVideoSettlementProcessing,
	}
	result, ok := seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-processing", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusInProgress, result.TaskStatus)

	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-processing", pending)
	require.False(t, ok)
	require.Nil(t, result)

	pending.UpstreamStatus = service.SeedanceVideoResponseStatusCompleted
	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointStatus, "task-completing-status", pending)
	require.True(t, ok)
	require.Equal(t, service.SeedanceVideoResponseStatusInProgress, result.TaskStatus)

	result, ok = seedanceVideoLocalTerminalResult(service.SeedanceVideoEndpointCancel, "task-completing", pending)
	require.False(t, ok)
	require.Nil(t, result)
}

func TestSeedanceVideoContentUnavailableAfterLocalTerminalOrFinalization(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		settlement string
	}{
		{name: "canceled", status: service.SeedanceVideoResponseStatusCanceled, settlement: service.SeedanceVideoSettlementReleased},
		{name: "failed", status: service.SeedanceVideoResponseStatusFailed, settlement: service.SeedanceVideoSettlementReleased},
		{name: "processing", status: service.SeedanceVideoResponseStatusInProgress, settlement: service.SeedanceVideoSettlementProcessing},
		{name: "completed before settlement", status: service.SeedanceVideoResponseStatusCompleted, settlement: service.SeedanceVideoSettlementProcessing},
		{name: "legacy released running", status: service.SeedanceVideoResponseStatusInProgress, settlement: service.SeedanceVideoSettlementReleased},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unavailable, code, message := seedanceVideoContentUnavailable(&service.SeedanceVideoPendingBilling{
				UpstreamStatus: tc.status, SettlementStatus: tc.settlement,
			})
			require.True(t, unavailable)
			require.Equal(t, "video_content_unavailable", code)
			require.NotEmpty(t, message)
		})
	}
}

func TestSeedanceVideoReleaseFailureDoesNotExposeTerminalFailure(t *testing.T) {
	result := &service.SeedanceVideoForwardResult{
		TaskStatus: service.SeedanceVideoResponseStatusFailed,
		OpenAIForwardResult: &service.OpenAIForwardResult{
			ResponseID: "task-release-retry",
		},
	}

	setSeedanceVideoReleasePendingStatus(result)
	response := service.BuildSeedanceVideoResponse(result, service.SeedanceVideoResponseMeta{
		ID:               "task-release-retry",
		SettlementStatus: service.SeedanceVideoSettlementPending,
	})

	require.Equal(t, service.SeedanceVideoResponseStatusInProgress, response.Status)
	require.Nil(t, response.Error)

	// A successful release keeps the provider's terminal failure visible.
	terminal := &service.SeedanceVideoForwardResult{TaskStatus: service.SeedanceVideoResponseStatusFailed}
	response = service.BuildSeedanceVideoResponse(terminal, service.SeedanceVideoResponseMeta{})
	require.Equal(t, service.SeedanceVideoResponseStatusFailed, response.Status)
	require.NotNil(t, response.Error)
}
