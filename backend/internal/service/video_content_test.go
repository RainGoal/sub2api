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

func videoContentTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestZhiqiVideoContentJSONBecomesPublicBytesWithoutLeakingCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, redirect := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "redirect"}[redirect], func(t *testing.T) {
			upstream := &seedanceVideoUpstream{responses: []*http.Response{
				videoContentTestResponse(200, "application/json", `{"id":"supplier-task","status":"succeeded","usage":{"duration_seconds":5}}`),
				videoContentTestResponse(200, "application/json; charset=utf-8", `{"url":"https://cdn.example/video.mp4?signed=opaque","content_type":"video/mp4","error":null}`),
			}}
			if redirect {
				response := videoContentTestResponse(302, "text/html", "")
				response.Header.Set("Location", "https://second-cdn.example/final.mp4")
				upstream.responses = append(upstream.responses, response)
			}
			bytes := videoContentTestResponse(206, "video/mp4", "video-bytes")
			bytes.Header.Set("Content-Range", "bytes 0-10/100")
			upstream.responses = append(upstream.responses, bytes)
			account := seedanceVideoTestAccount()
			account.Credentials["video_provider"] = string(videoprovider.ProviderZhiqiV1)
			account.Credentials["base_url"] = videoprovider.DefaultZhiqiBaseURL
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/public-task/content", nil)
			c.Request.Header.Set("Range", "bytes=0-10")
			c.Request.Header.Set("If-Range", "video-version")
			_, err := svc.ForwardSeedanceVideo(context.Background(), c, account, string(videoprovider.ProviderZhiqiV1), SeedanceVideoEndpointContent, "supplier-task", nil)
			require.NoError(t, err)
			require.Equal(t, 206, recorder.Code)
			require.Equal(t, "video-bytes", recorder.Body.String())
			require.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
			require.Equal(t, "bytes 0-10/100", recorder.Header().Get("Content-Range"))
			require.Equal(t, "Bearer seedance-secret", upstream.requests[1].Header.Get("Authorization"))
			for _, req := range upstream.requests[2:] {
				require.Empty(t, req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("X-Tenant"))
				require.Equal(t, "bytes=0-10", req.Header.Get("Range"))
				require.Equal(t, "video-version", req.Header.Get("If-Range"))
				require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
			}
		})
	}
}

func TestZhiqiVideoContentRejectsInvalidURLOrNonVideoBeforePublicWrite(t *testing.T) {
	driver, err := videoprovider.Resolve(string(videoprovider.ProviderZhiqiV1))
	require.NoError(t, err)
	parser, ok := driver.(videoprovider.ContentURLParser)
	require.True(t, ok)
	for _, test := range []struct {
		name, source string
		next         *http.Response
		calls        int
	}{
		{"private source", `{"url":"https://127.0.0.1/video.mp4"}`, nil, 0},
		{"provider delivery error", `{"url":"https://cdn.example/v.mp4","error":{"code":"media_storage_failed"}}`, nil, 0},
		{"oversized JSON", strings.Repeat(" ", 1<<20+1), nil, 0},
		{"JSON instead of video", `{"url":"https://cdn.example/v.mp4"}`, videoContentTestResponse(200, "application/json", `{"url":"https://third.example/v.mp4"}`), 1},
		{"HTML instead of video", `{"url":"https://cdn.example/v.mp4"}`, videoContentTestResponse(200, "text/html", "upstream error"), 1},
		{"private redirect", `{"url":"https://cdn.example/v.mp4"}`, &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://169.254.169.254/latest/meta-data"}}, Body: io.NopCloser(strings.NewReader(""))}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &seedanceVideoUpstream{}
			if test.next != nil {
				upstream.responses = []*http.Response{test.next}
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/t/content", nil)
			_, err := svc.resolveVideoContentURL(context.Background(), c, seedanceVideoTestAccount(), parser, videoContentTestResponse(200, "application/json", test.source))
			require.Error(t, err)
			require.Len(t, upstream.requests, test.calls)
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestZhiqiVideoContentDirectResponseRemainsBytesAndRejectsHTMLOrRedirect(t *testing.T) {
	for _, test := range []struct {
		name, contentType string
		status            int
		reject            bool
	}{
		{"video", "video/mp4", 200, false},
		{"binary", "application/octet-stream", 200, false},
		{"partial", "video/mp4", 206, false},
		{"range unavailable", "text/plain", 416, false},
		{"HTML error", "text/html", 200, true},
		{"redirect", "text/html", 302, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &seedanceVideoUpstream{responses: []*http.Response{
				videoContentTestResponse(200, "application/json", `{"id":"supplier-task","status":"succeeded"}`),
				videoContentTestResponse(test.status, test.contentType, "content-bytes"),
			}}
			account := seedanceVideoTestAccount()
			account.Credentials["video_provider"] = string(videoprovider.ProviderZhiqiV1)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/t/content", nil)
			_, err := svc.ForwardSeedanceVideo(context.Background(), c, account, "zhiqi_v1", SeedanceVideoEndpointContent, "supplier-task", nil)
			if test.reject {
				require.Error(t, err)
				require.False(t, c.Writer.Written())
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.status, recorder.Code)
			require.Equal(t, "content-bytes", recorder.Body.String())
		})
	}
}
