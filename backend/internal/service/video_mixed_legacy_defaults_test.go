package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestVideoProtocolLegacyDefaultsParticipateInMixedGroupValidation(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		for _, reverse := range []bool{false, true} {
			for _, field := range []string{"duration", "resolution", "aspect_ratio"} {
				t.Run(string(provider)+"/"+field+map[bool]string{false: "/legacy_first", true: "/zhiqi_first"}[reverse], func(t *testing.T) {
					capability := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
					capability.Model = "Seedance-2.0"
					capability.DefaultDuration, capability.DefaultResolution = 4, "480p"
					switch field {
					case "duration":
						capability.DefaultDuration = 6
					case "resolution":
						capability.DefaultResolution = "720p"
					case "aspect_ratio":
						capability.DefaultRatio = "9:16"
					}
					legacy := seedanceVideoTestAccount()
					legacy.ID = 20
					legacy.Credentials["video_provider"] = string(provider)
					legacy.Credentials["model_mapping"] = map[string]any{capability.Model: capability.Model}
					modern := videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, capability)
					svc, key := videoTasksGatewayPricingService(explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7))
					body := []byte(`{"model":"Seedance-2.0","prompt":"waves"}`)
					svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*legacy}}
					original, _, originalInfo, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
					require.NoError(t, err)
					require.Equal(t, capability.Model, originalInfo.Model)
					require.Equal(t, 4, original.Duration)
					require.Equal(t, "480p", original.Resolution)
					require.True(t, legacy.isSeedanceModelSupported(capability.Model))
					accounts := []Account{*legacy, *modern}
					if reverse {
						accounts[0], accounts[1] = accounts[1], accounts[0]
					}
					svc.accountRepo = &videoCatalogAccountRepo{accounts: accounts}
					_, _, _, err = svc.PrepareVideoRequestForKey(context.Background(), key, body)
					require.ErrorContains(t, err, "conflicting defaults")
					request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key,
						[]byte(`{"model":"Seedance-2.0","prompt":"waves","duration":6,"resolution":"720p","aspect_ratio":"16:9"}`))
					require.NoError(t, err)
					for i := range accounts {
						prepared, err := svc.PrepareVideoRequestForAccount(request, &accounts[i])
						require.NoError(t, err)
						require.Equal(t, 6, prepared.Duration)
						require.Equal(t, "720p", prepared.Resolution)
						require.Equal(t, "16:9", prepared.AspectRatio)
					}
				})
			}
		}
	}
}

func TestVideoProtocolLegacyDefaultEligibility(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		for _, scenario := range []string{"matching_defaults", "unsupported_duration", "missing_sales_tier", "missing_purchase_tier"} {
			t.Run(string(provider)+"/"+scenario, func(t *testing.T) {
				capability := videoProtocolCapability(videoprovider.ProviderZhiqiV1)
				capability.Model = "Seedance-2.0"
				legacy := seedanceVideoTestAccount()
				legacy.Credentials["video_provider"] = string(provider)
				body := []byte(`{"model":"Seedance-2.0","prompt":"waves"}`)
				sales := explicitVideoPrice(capability.Model, VideoBillingPerVideo, 0.7)
				tierPrice := 0.3
				tierOnly := explicitVideoPrice(capability.Model, VideoBillingPerVideo, tierPrice)
				tierOnly.PerRequestPrice = nil
				tierOnly.Intervals = []PricingInterval{{TierLabel: "720p", PerRequestPrice: &tierPrice}}
				switch scenario {
				case "matching_defaults":
					capability.DefaultDuration, capability.DefaultResolution = 4, "480p"
				case "unsupported_duration":
					capability.MaxDuration = 30
					body = []byte(`{"model":"Seedance-2.0","prompt":"waves","duration":20}`)
				case "missing_sales_tier":
					sales = tierOnly
				case "missing_purchase_tier":
					legacy.Extra = map[string]any{AccountModelCostPricingExtraKey: []ChannelModelPricing{tierOnly}}
				}
				modern := videoProtocolAccount(21, videoprovider.ProviderZhiqiV1, capability)
				svc, key := videoTasksGatewayPricingService(sales)
				svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*legacy, *modern}}
				request, _, _, err := svc.PrepareVideoRequestForKey(context.Background(), key, body)
				require.NoError(t, err)
				expectedDuration := capability.DefaultDuration
				if scenario == "unsupported_duration" {
					expectedDuration = 20
				}
				require.Equal(t, expectedDuration, request.Duration)
				require.Equal(t, capability.DefaultResolution, request.Resolution)
			})
		}
	}
}

func TestVideoProtocolLegacyPricesRetainCanonicalIdentity(t *testing.T) {
	for _, provider := range []videoprovider.ID{videoprovider.ProviderBBLabuV1, videoprovider.ProviderFFLinkV1} {
		t.Run(string(provider), func(t *testing.T) {
			legacy := seedanceVideoTestAccount()
			legacy.Credentials["video_provider"] = string(provider)
			svc, key := videoTasksGatewayPricingService()
			key.Group.VideoModelPrices[videoprovider.ModelSeedance20]["480p"] = 0.1
			svc.accountRepo = &videoCatalogAccountRepo{accounts: []Account{*legacy}}
			request, _, info, err := svc.PrepareVideoRequestForKey(context.Background(), key,
				[]byte(`{"model":"Seedance-2.0","prompt":"waves"}`))
			require.NoError(t, err)
			require.Equal(t, videoprovider.ModelSeedance20, info.Model)
			require.Equal(t, "Seedance-2.0", info.RequestedModel)
			require.Equal(t, 4, request.Duration)
			require.Equal(t, "480p", request.Resolution)
			quote, err := svc.ResolveSeedanceVideoPricingSnapshot(context.Background(), key, info)
			require.NoError(t, err)
			require.Nil(t, quote.SalesSnapshot)
		})
	}
}
