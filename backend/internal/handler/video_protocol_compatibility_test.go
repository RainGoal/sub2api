//go:build unit

package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVideoProtocolHandlerRetriesExplicitAccountRejection(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1, videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
			t.Run(string(provider)+"/"+http.StatusText(status), func(t *testing.T) {
				h := newVideoTasksHandlerHarness(t)
				capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
				require.True(t, ok)
				capability.Profile = videoprovider.ProfileZhiqiV1
				h.accounts[0].Credentials["video_provider"] = string(videoprovider.ProviderZhiqiV1)
				h.accounts[0].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
				h.accounts[1].Credentials["video_provider"] = string(provider)
				if provider == videoprovider.ProviderZhiqiV1 {
					h.accounts[1].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
				}
				h.upstream.responses = []*http.Response{{StatusCode: status,
					Header: http.Header{"Content-Type": {"application/json"}},
					Body:   io.NopCloser(strings.NewReader(`{"error":{"type":"account_rejected","message":"request rejected"}}`))}}
				response := h.request(http.MethodPost, "")
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				require.Len(t, h.upstream.calls, 2)
				require.Equal(t, h.accounts[0].ID, h.upstream.calls[0].accountID)
				require.Equal(t, h.accounts[1].ID, h.upstream.calls[1].accountID)
				require.Equal(t, h.accounts[1].ID, h.tasks.pending.AccountID)
				require.Equal(t, string(provider), h.tasks.pending.ExecutionSnapshot.Protocol)
				require.Equal(t, 1, h.billing.reserved)
				require.Zero(t, h.billing.released)
				require.Equal(t, h.tasks.pending.PublicID(), gjson.GetBytes(response.Body.Bytes(), "id").String())
			})
		}
	}
}

func TestVideoProtocolHandlerDoesNotRetryRequestErrors(t *testing.T) {
	for _, status := range []int{400, 404, 409, 413, 415, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			h.upstream.status, h.upstream.body = status, `{"error":{"type":"invalid_request_error","message":"invalid request"}}`
			response := h.request(http.MethodPost, "")
			require.GreaterOrEqual(t, response.Code, 400)
			require.Len(t, h.upstream.calls, 1)
			require.Equal(t, 1, h.billing.released)
			require.Zero(t, h.billing.held)
		})
	}
}

func TestVideoProtocolHandlerRetryPreservesUnknownSubmission(t *testing.T) {
	for _, scenario := range []string{"transport_error", "server_error", "request_timeout", "accepted_without_identity"} {
		t.Run(scenario, func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t, 3)
			h.upstream.responses = []*http.Response{{StatusCode: http.StatusTooManyRequests,
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"error":"rate limited"}`))}}
			switch scenario {
			case "transport_error":
				h.upstream.err = errors.New("connection reset after write")
			case "server_error":
				h.upstream.status = http.StatusServiceUnavailable
				h.upstream.body = `{"error":"retry later"}`
			case "request_timeout":
				h.upstream.status = http.StatusRequestTimeout
				h.upstream.body = `{"error":"request timed out"}`
			case "accepted_without_identity":
				h.upstream.body = `{"status":"queued"}`
			}
			response := h.request(http.MethodPost, "")
			require.Equal(t, http.StatusBadGateway, response.Code, response.Body.String())
			require.Equal(t, "video_submission_unknown", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
			require.Len(t, h.upstream.calls, 2, "a possible task on the second account must not be sent to the third")
			require.Equal(t, h.accounts[1].ID, h.tasks.pending.AccountID)
			require.Equal(t, h.tasks.pending.PublicID(), response.Header().Get("X-Video-Task-ID"))
			require.Equal(t, service.SeedanceVideoSettlementNeedsReview, h.tasks.pending.SettlementStatus)
			require.Equal(t, 1, h.billing.reserved)
			require.Zero(t, h.billing.released)
			require.InDelta(t, 0.25, h.billing.held, 1e-8)
			require.Zero(t, h.tasks.released)
		})
	}
}

func TestVideoProtocolHandlerAllAccountsExplicitlyRejectReleaseHold(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			h.upstream.status, h.upstream.body = status, `{"error":"account rejected"}`
			response := h.request(http.MethodPost, "")
			require.GreaterOrEqual(t, response.Code, 400)
			require.Len(t, h.upstream.calls, 2)
			require.NotEqual(t, h.upstream.calls[0].accountID, h.upstream.calls[1].accountID)
			require.Equal(t, 1, h.billing.reserved)
			require.Equal(t, 1, h.billing.released)
			require.Zero(t, h.billing.held)
			require.Equal(t, 1, h.tasks.released)
		})
	}
}

func TestVideoProtocolHandlerMixedGroupPreservesClientContractAndOriginalRoute(t *testing.T) {
	for _, selectedProvider := range []videoprovider.ID{videoprovider.ProviderVideoTasksJSONV1, videoprovider.ProviderZhiqiV1} {
		t.Run(string(selectedProvider), func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			providers := []videoprovider.ID{videoprovider.ProviderZhiqiV1, videoprovider.ProviderVideoTasksJSONV1}
			if selectedProvider == videoprovider.ProviderZhiqiV1 {
				providers[0], providers[1] = providers[1], providers[0]
			}
			for i := range h.accounts {
				account := &h.accounts[i]
				capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
				require.True(t, ok)
				if providers[i] == videoprovider.ProviderZhiqiV1 {
					capability.Profile = videoprovider.ProfileZhiqiV1
				}
				if i == 0 {
					capability.MaxImages, capability.MaxImagesByResolution = 0, nil
				}
				account.Credentials["video_provider"] = string(providers[i])
				account.Credentials["base_url"] = "https://" + string(providers[i]) + ".example/v1"
				account.Credentials["model_mapping"] = map[string]any{"Seedance-2.0": string(providers[i]) + "-upstream-model"}
				account.Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
				prices := append([]service.ChannelModelPricing(nil), account.Extra[service.AccountModelCostPricingExtraKey].([]service.ChannelModelPricing)...)
				for j := range prices {
					prices[j].Models = []string{string(providers[i]) + "-upstream-model"}
				}
				account.Extra[service.AccountModelCostPricingExtraKey] = prices
			}
			created := h.request(http.MethodPost, "")
			require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
			require.Len(t, h.upstream.calls, 1, "an incompatible supplier cannot receive a create request")
			call := h.upstream.calls[0]
			require.Equal(t, h.accounts[1].ID, call.accountID)
			require.Equal(t, string(selectedProvider)+"-upstream-model", gjson.Get(call.body, "model").String())
			imageField := "referenceImages.0"
			if selectedProvider == videoprovider.ProviderZhiqiV1 {
				imageField = "reference_image_urls.0"
			}
			require.Equal(t, "https://cdn.example/ref.png", gjson.Get(call.body, imageField).String())
			id := gjson.GetBytes(created.Body.Bytes(), "id").String()
			require.Regexp(t, `^vid_[0-9a-f]{32}$`, id)
			require.Equal(t, "Seedance-2.0", gjson.GetBytes(created.Body.Bytes(), "model").String())
			require.NotContains(t, created.Body.String(), "upstream-model")
			require.NotContains(t, created.Body.String(), "upstream-video-123")
			require.Equal(t, 1, h.billing.reserved)
			h.accounts[1].Credentials["video_provider"] = string(videoprovider.ProviderFFLinkV1)
			h.accounts[1].Credentials["base_url"] = "https://changed.example/v1"
			h.accounts[1].Extra[service.VideoModelCapabilitiesExtra] = nil
			h.cfg.Gateway.VideoTasksJSONV1Enabled = false
			h.upstream.body = `{"id":"upstream-video-123","status":"running"}`
			status := h.request(http.MethodGet, id)
			require.Equal(t, http.StatusOK, status.Code, status.Body.String())
			require.Equal(t, id, gjson.GetBytes(status.Body.Bytes(), "id").String())
			require.Equal(t, "in_progress", gjson.GetBytes(status.Body.Bytes(), "status").String())
			require.Len(t, h.upstream.calls, 2)
			require.Equal(t, "https://"+string(selectedProvider)+".example/v1/videos/upstream-video-123", h.upstream.calls[1].url)
			require.Equal(t, call.accountID, h.upstream.calls[1].accountID)
			require.Zero(t, h.billing.released)
		})
	}
}

func TestVideoProtocolHandlerMissingPurchasePricePreservesServiceUnavailable(t *testing.T) {
	h := newVideoTasksHandlerHarness(t)
	for i := range h.accounts {
		delete(h.accounts[i].Extra, service.AccountModelCostPricingExtraKey)
	}
	response := h.request(http.MethodPost, "")
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Equal(t, "seedance_account_cost_not_configured", gjson.GetBytes(response.Body.Bytes(), "error.type").String())
	require.Empty(t, h.upstream.calls)
	require.Zero(t, h.billing.reserved, "reject missing purchase configuration before reserving customer balance")
}

func TestVideoProtocolHandlerMixedLegacyAccountSkipsUnsupportedParameters(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			h.accounts[0].Credentials["video_provider"] = string(provider)
			h.accounts[0].Credentials["model_mapping"] = map[string]any{"seedance-2.0": "seedance-2.0"}
			capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
			require.True(t, ok)
			capability.MaxDuration, capability.AllowedDurations = 30, nil
			h.accounts[1].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
			response := h.request(http.MethodPost, "", `{"model":"Seedance-2.0","prompt":"waves","duration":20,"resolution":"720p"}`)
			require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
			require.Len(t, h.upstream.calls, 1)
			require.Equal(t, h.accounts[1].ID, h.upstream.calls[0].accountID)
			require.Equal(t, int64(20), gjson.Get(h.upstream.calls[0].body, "duration").Int())
		})
	}
}

func TestVideoProtocolHandlerMixedLegacyAccountRemainsEligible(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		for _, accountCount := range []int{1, 2} {
			t.Run(string(provider)+map[int]string{1: "/legacy_only", 2: "/mixed"}[accountCount], func(t *testing.T) {
				h := newVideoTasksHandlerHarness(t, accountCount)
				h.accounts[0].Credentials["video_provider"] = string(provider)
				response := h.request(http.MethodPost, "")
				require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
				require.Len(t, h.upstream.calls, 1)
				require.Equal(t, h.accounts[0].ID, h.upstream.calls[0].accountID)
				require.Equal(t, string(provider), h.tasks.pending.ExecutionSnapshot.Protocol)
				require.Equal(t, "Seedance-2.0", gjson.GetBytes(response.Body.Bytes(), "model").String())
				require.Equal(t, 1, h.billing.reserved)
				require.Zero(t, h.billing.released)
				id := gjson.GetBytes(response.Body.Bytes(), "id").String()
				h.upstream.body = `{"id":"upstream-video-123","status":"running"}`
				status := h.request(http.MethodGet, id)
				require.Equal(t, http.StatusOK, status.Code, status.Body.String())
				require.Equal(t, "Seedance-2.0", gjson.GetBytes(status.Body.Bytes(), "model").String())
			})
		}
	}
}

func TestVideoProtocolHandlerMixedLegacyDefaultConflictPrecedesReservation(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			h := newVideoTasksHandlerHarness(t)
			h.accounts[0].Credentials["video_provider"] = string(provider)
			capability, ok := videoprovider.LookupVideoModelCapability("Seedance-2.0", nil)
			require.True(t, ok)
			capability.Profile, capability.DefaultDuration = videoprovider.ProfileZhiqiV1, 6
			h.accounts[1].Credentials["video_provider"] = string(videoprovider.ProviderZhiqiV1)
			h.accounts[1].Extra[service.VideoModelCapabilitiesExtra] = []videoprovider.VideoModelCapability{capability}
			response := h.request(http.MethodPost, "", `{"model":"Seedance-2.0","prompt":"waves"}`)
			require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "conflicting defaults")
			require.Empty(t, h.upstream.calls)
			require.Zero(t, h.billing.reserved)
		})
	}
}
