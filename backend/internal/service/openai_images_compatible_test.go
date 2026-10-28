//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompatibleImagesModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{
		"gemini-2.5-flash-image", "gemini-2.5-flash-image-preview", "gemini-3-pro-image", "gemini-3.1-flash-image",
		"gemini-nano-banana-2.1", "StudioV2/Banana:2.1", "dall-e-3", "flux-kontext-pro", "doubao-seedream-4-0-250828",
		"gemini-2.5-pro", "gemini-2.5-flash", "gemini-3-pro-imageless", "unknown-image", "gpt-5.5",
	} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw"}`, model))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, bytes.NewReader(body))
			parsed, err := (&OpenAIGatewayService{}).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)
			require.Equal(t, model, parsed.Model)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapability)
			require.Equal(t, OpenAIImagesCapabilityAPIKey, classifyOpenAIImagesCapability(&OpenAIImagesRequest{Model: model, N: 1}))
			require.Equal(t, OpenAIImagesCapabilityAPIKey, (&OpenAIImagesRequest{RequiredCapability: OpenAIImagesCapabilityBasic}).RequiredCapabilityForModel(model))
			require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
			for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
				require.False(t, (&Account{Platform: PlatformOpenAI, Type: typ}).SupportsOpenAIImageCapability(parsed.RequiredCapability))
			}
			require.False(t, isOpenAIImageGenerationModel(model), "compatible image IDs must not enter native Responses normalization")
		})
	}
	for _, model := range []string{"", " ", "\t\r\n"} {
		t.Run(fmt.Sprintf("reject_empty_%q", model), func(t *testing.T) {
			require.ErrorContains(t, validateCompatibleImagesModel(model), "images endpoint requires an image model")
		})
	}
}

func TestCompatibleImagesForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gemini-3.1-flash-image", "gemini-nano-banana-2.1", "StudioV2/Banana:2.1"} {
		for _, kind := range []string{"generation", "json_edit", "multipart_edit", "composite_multipart_alias", "channel_mapping", "account_mapping"} {
			t.Run(model+"/"+kind, func(t *testing.T) {
				endpoint := openAIImagesGenerationsEndpoint
				contentType := "application/json"
				channelModel := ""
				credentials := map[string]any{"api_key": "image-key", "base_url": "https://compatible.example/v1"}
				requestModel := model
				if kind == "channel_mapping" {
					requestModel, channelModel = "gpt-image-2", model
				}
				if kind == "account_mapping" {
					requestModel = "gpt-image-2"
					credentials["model_mapping"] = map[string]any{requestModel: model}
				}
				body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","size":"1024x1024","custom_field":"preserved"}`, requestModel))
				if kind == "json_edit" {
					endpoint = openAIImagesEditsEndpoint
					body = []byte(fmt.Sprintf(`{"model":%q,"prompt":"draw","images":[{"image_url":"https://source.example/input.png"}],"custom_field":"preserved"}`, model))
				}
				if strings.Contains(kind, "multipart") {
					endpoint = openAIImagesEditsEndpoint
					if kind == "composite_multipart_alias" {
						requestModel = "public-banana"
					}
					var buf bytes.Buffer
					writer := multipart.NewWriter(&buf)
					require.NoError(t, writer.WriteField("model", requestModel))
					require.NoError(t, writer.WriteField("prompt", "draw"))
					require.NoError(t, writer.WriteField("custom_field", "preserved"))
					part, err := writer.CreateFormFile("image", "input.png")
					require.NoError(t, err)
					_, err = part.Write([]byte("original-image-bytes"))
					require.NoError(t, err)
					require.NoError(t, writer.Close())
					body, contentType = buf.Bytes(), writer.FormDataContentType()
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", contentType)
				if kind == "composite_multipart_alias" {
					ctx := WithResolvedTargetPlatform(c.Request.Context(), PlatformOpenAI)
					ctx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{Matched: true, TargetPlatform: PlatformOpenAI, UpstreamModel: model, PublicModel: requestModel})
					c.Request = c.Request.WithContext(ctx)
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aW1hZ2U="}]}`))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)
				if kind == "channel_mapping" {
					require.Equal(t, OpenAIImagesCapabilityAPIKey, parsed.RequiredCapabilityForModel(channelModel))
				}
				result, err := svc.ForwardImages(c.Request.Context(), c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: credentials}, body, parsed, channelModel)
				require.NoError(t, err)
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, model, result.UpstreamModel)
				require.Equal(t, firstNonEmptyString(channelModel, parsed.Model), result.Model)
				require.Equal(t, "https://compatible.example"+endpoint, upstream.lastReq.URL.String())
				require.Equal(t, "Bearer image-key", upstream.lastReq.Header.Get("Authorization"))
				if strings.Contains(kind, "multipart") {
					r := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(upstream.lastBody))
					r.Header.Set("Content-Type", upstream.lastReq.Header.Get("Content-Type"))
					require.NoError(t, r.ParseMultipartForm(1<<20))
					require.Equal(t, model, r.FormValue("model"))
					require.Equal(t, "preserved", r.FormValue("custom_field"))
					file, _, err := r.FormFile("image")
					require.NoError(t, err)
					defer file.Close()
					data, err := io.ReadAll(file)
					require.NoError(t, err)
					require.Equal(t, "original-image-bytes", string(data))
				} else {
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, "preserved", gjson.GetBytes(upstream.lastBody, "custom_field").String())
				}
			})
		}
	}
}

func TestCompatibleImagesNativeAccountsRejectBeforeForwarding(t *testing.T) {
	for _, model := range []string{"gemini-3-pro-image", "gemini-nano-banana-2.1", "StudioV2/Banana:2.1"} {
		for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
			for _, mapping := range []string{"none", "channel", "account"} {
				t.Run(fmt.Sprintf("%s/%s/mapping=%s", model, typ, mapping), func(t *testing.T) {
					requestModel, channelModel := model, ""
					account := &Account{Platform: PlatformOpenAI, Type: typ, Credentials: map[string]any{"access_token": "unused"}}
					if mapping == "account" {
						account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": model}
						requestModel = "gpt-image-2"
					}
					if mapping == "channel" {
						requestModel, channelModel = "gpt-image-2", model
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
					upstream := &httpUpstreamRecorder{}
					svc := &OpenAIGatewayService{httpUpstream: upstream}
					_, err := svc.ForwardImages(context.Background(), c, account, nil, &OpenAIImagesRequest{Model: requestModel}, channelModel)
					require.ErrorContains(t, err, "images endpoint requires an image model")
					require.ErrorContains(t, err, model)
					require.Empty(t, upstream.requests)
				})
			}
		}
	}
}
