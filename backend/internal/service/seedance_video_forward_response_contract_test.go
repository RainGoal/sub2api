package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardSeedanceVideoStatusDoesNotWriteProviderJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task-1", nil)
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"task-1","status":"running"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), c, seedanceVideoTestAccount(),
		string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointStatus, "task-1", nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "task-1", result.ResponseID)
	require.Equal(t, "running", result.TaskStatus)
	require.Empty(t, recorder.Body.String())
	require.Empty(t, recorder.Result().Header.Get("Content-Type"))
}

func TestForwardSeedanceVideoCancelDoesNotWriteProviderJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/videos/jobs/job-1", nil)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1","status":"canceled"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), c, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "job-1", result.ResponseID)
	require.Equal(t, "canceled", result.TaskStatus)
	require.Empty(t, recorder.Body.String())
	require.Empty(t, recorder.Result().Header.Get("Content-Type"))
}

func TestForwardSeedanceVideoCancelDefaultsMissingStatusToCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusNoContent,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, "job-1", result.ResponseID)
	require.Equal(t, string(videoprovider.StatusCanceled), result.TaskStatus)
}

func TestForwardSeedanceVideoCreateDefaultsMissingStatusToQueued(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"task-1"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointCreate, "", &SeedanceVideoCreateRequest{
			Model: videoprovider.ModelSeedance20, Prompt: "waves", Duration: 10, Resolution: "720p",
			AspectRatio: "16:9", Raw: map[string]any{},
		},
	)

	require.NoError(t, err)
	require.Equal(t, "task-1", result.ResponseID)
	require.Equal(t, string(videoprovider.StatusPending), result.TaskStatus)
	require.Equal(t, SeedanceVideoResponseStatusQueued, BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{ID: "task-1"}).Status)
}

func TestForwardSeedanceVideoCreateTreatsProviderSuccessAsQueued(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1","status":"success"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCreate, "", &SeedanceVideoCreateRequest{
			Model: videoprovider.ModelSeedance25, Prompt: "waves", Duration: 10, Resolution: "720p",
			AspectRatio: "16:9", Raw: map[string]any{},
		},
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusPending), result.TaskStatus)
	require.Equal(t, SeedanceVideoResponseStatusQueued, BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{ID: "job-1"}).Status)
}

func TestForwardSeedanceVideoCancelTreatsGenericSuccessAsCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1","status":"success"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusCanceled), result.TaskStatus)
	require.Equal(t, SeedanceVideoResponseStatusCanceled, BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{ID: "job-1"}).Status)
}

func TestForwardSeedanceVideoCancelTreatsExplicitBooleanFailureAsFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"success":false}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusFailed), result.TaskStatus)
	response := BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{
		ID: "job-1", ErrorCode: SeedanceVideoResponseErrorCodeCancellationFailed,
		ErrorMessage: SeedanceVideoResponseErrorMessageCancellationFailed,
	})
	require.Equal(t, SeedanceVideoResponseStatusFailed, response.Status)
	require.Equal(t, SeedanceVideoResponseErrorCodeCancellationFailed, response.Error.Code)
}

func TestForwardSeedanceVideoCancelBooleanFailureOverridesGenericSuccess(t *testing.T) {
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"success","success":false}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusFailed), result.TaskStatus)
}

func TestForwardSeedanceVideoCancelPreservesExplicitFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1","status":"error"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusFailed), result.TaskStatus)
	response := BuildSeedanceVideoResponse(result, SeedanceVideoResponseMeta{
		ID: "job-1", ErrorCode: SeedanceVideoResponseErrorCodeCancellationFailed,
		ErrorMessage: SeedanceVideoResponseErrorMessageCancellationFailed,
	})
	require.Equal(t, SeedanceVideoResponseStatusFailed, response.Status)
	require.Equal(t, &SeedanceVideoResponseError{
		Code:    SeedanceVideoResponseErrorCodeCancellationFailed,
		Message: SeedanceVideoResponseErrorMessageCancellationFailed,
	}, response.Error)
}

func TestForwardSeedanceVideoCancelPreservesExplicitCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"job_id":"job-1","status":"completed"}`)),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, string(videoprovider.StatusCompleted), result.TaskStatus)
	require.Equal(t, 1, result.VideoCount)
}

func TestForwardSeedanceVideoCancelTreatsWhitespaceBodyAsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := seedanceVideoTestAccount()
	account.Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
	account.Credentials["base_url"] = "https://api.fflink.top/v1"
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusNoContent,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(" \n\t")),
	}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, account,
		string(videoprovider.ProviderFFLinkV1), SeedanceVideoEndpointCancel, "job-1", nil,
	)

	require.NoError(t, err)
	require.Equal(t, "job-1", result.ResponseID)
	require.Equal(t, string(videoprovider.StatusCanceled), result.TaskStatus)
}

func TestForwardSeedanceVideoCancelReportsProviderCapabilityGap(t *testing.T) {
	upstream := &seedanceVideoUpstream{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), nil, seedanceVideoTestAccount(),
		string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointCancel, "task-1", nil,
	)

	require.Nil(t, result)
	require.ErrorIs(t, err, videoprovider.ErrVideoTaskCancellationUnsupported)
	require.Empty(t, upstream.requests)
}

func TestForwardSeedanceVideoOversizedJSONMarksResponseCommitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/task-large", nil)
	upstream := &seedanceVideoUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"running","padding":"too-large"}`)),
	}}}
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 8
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	result, err := svc.ForwardSeedanceVideo(
		context.Background(), c, seedanceVideoTestAccount(),
		string(videoprovider.ProviderBBLabuV1), SeedanceVideoEndpointStatus, "task-large", nil,
	)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.JSONEq(t, `{"error":{"type":"upstream_error","message":"Upstream response too large"}}`, recorder.Body.String())
	require.True(t, IsResponseCommitted(c))
}
