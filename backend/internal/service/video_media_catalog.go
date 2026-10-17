package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

type VideoMediaPrice struct {
	Resolution     string              `json:"resolution"`
	Version        int                 `json:"version"`
	BillingUnit    VideoBillingUnit    `json:"billing_unit"`
	UnitPrice      float64             `json:"unit_price"`
	QuantityPolicy VideoQuantityPolicy `json:"quantity_policy"`
}

// Variants are complete, independently valid contracts. Never union one
// supplier's duration range with another supplier's asset limits.
type VideoMediaVariant struct {
	DurationMode                   string            `json:"duration_mode"`
	MinDuration                    int               `json:"min_duration"`
	MaxDuration                    int               `json:"max_duration"`
	DefaultDuration                int               `json:"default_duration"`
	AllowedDurations               []int             `json:"allowed_durations"`
	Resolutions                    []string          `json:"resolutions"`
	DefaultResolution              string            `json:"default_resolution"`
	ResolutionControl              bool              `json:"resolution_control"`
	Ratios                         []string          `json:"ratios"`
	DefaultRatio                   string            `json:"default_ratio"`
	MaxImages                      int               `json:"max_images"`
	MaxImagesByResolution          map[string]int    `json:"max_images_by_resolution,omitempty"`
	MaxVideos                      int               `json:"max_videos"`
	MaxAudios                      int               `json:"max_audios"`
	MaxTotalAssets                 int               `json:"max_total_assets"`
	Frames                         bool              `json:"frames"`
	AllowFirstOnly                 bool              `json:"allow_first_only"`
	AllowFramesWithReferences      bool              `json:"allow_frames_with_references"`
	FramesRequireAutoRatio         bool              `json:"frames_require_auto_ratio"`
	GenerateAudio                  bool              `json:"generate_audio"`
	AudioRequiresImage             bool              `json:"audio_requires_image"`
	MaxReferenceVideoSeconds       int               `json:"max_reference_video_seconds"`
	RequireReferenceVideoDurations bool              `json:"require_reference_video_durations"`
	MaxCombinedVideoSeconds        int               `json:"max_combined_video_seconds"`
	SupportsContent                bool              `json:"supports_content"`
	SupportsCancel                 bool              `json:"supports_cancel"`
	Prices                         []VideoMediaPrice `json:"prices"`
}

type VideoMediaModel struct {
	ID       string              `json:"id"`
	Kind     string              `json:"kind"`
	Variants []VideoMediaVariant `json:"variants"`
}

func publicVideoVariant(cap videoprovider.VideoModelCapability) VideoMediaVariant {
	variant := VideoMediaVariant{DurationMode: cap.DurationMode, MinDuration: cap.MinDuration, MaxDuration: cap.MaxDuration,
		DefaultDuration: cap.DefaultDuration, AllowedDurations: cap.AllowedDurations, Resolutions: cap.Resolutions,
		DefaultResolution: cap.DefaultResolution, ResolutionControl: cap.ResolutionControl, Ratios: cap.Ratios, DefaultRatio: cap.DefaultRatio,
		MaxImages: cap.MaxImages, MaxImagesByResolution: cap.MaxImagesByResolution, MaxVideos: cap.MaxVideos, MaxAudios: cap.MaxAudios,
		MaxTotalAssets: cap.MaxTotalAssets, Frames: cap.Frames, AllowFirstOnly: cap.AllowFirstOnly,
		AllowFramesWithReferences: cap.AllowFramesWithReferences, FramesRequireAutoRatio: cap.FramesRequireAutoRatio,
		GenerateAudio: cap.GenerateAudio, AudioRequiresImage: cap.AudioRequiresImage, MaxReferenceVideoSeconds: cap.MaxReferenceVideoSeconds,
		RequireReferenceVideoDurations: cap.RequireReferenceVideoDurations, MaxCombinedVideoSeconds: cap.MaxCombinedVideoSeconds,
		SupportsContent: true, SupportsCancel: cap.SupportsCancel, Prices: []VideoMediaPrice{}}
	for _, combination := range cap.UnverifiedCombinations {
		if combination == "reference_videos" {
			variant.MaxVideos, variant.MaxReferenceVideoSeconds = 0, 0
			variant.RequireReferenceVideoDurations = false
			// Keep a combined cap only when it also restricts output without a reference.
			if variant.MaxDuration > 0 && variant.MaxCombinedVideoSeconds >= variant.MaxDuration {
				variant.MaxCombinedVideoSeconds = 0
			}
		}
	}
	return variant
}

func (s *OpenAIGatewayService) ListVideoMediaModels(ctx context.Context, key *APIKey) ([]VideoMediaModel, error) {
	models := []VideoMediaModel{}
	if s == nil || key == nil || key.GroupID == nil || key.Group == nil || key.Group.Platform != PlatformSeedance || !s.VideoTasksEnabled() {
		return models, nil
	}
	if s.accountRepo == nil {
		return nil, fmt.Errorf("video account repository is unavailable")
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *key.GroupID, PlatformSeedance)
	if err != nil {
		return nil, err
	}
	byModel := map[string][]VideoMediaVariant{}
	seen := map[string]bool{}
	for i := range accounts {
		account := &accounts[i]
		if account.GetVideoProviderID() != videoprovider.ProviderVideoTasksJSONV1 {
			continue
		}
		for model := range account.GetModelMapping() {
			capability, ok := account.VideoModelCapability(model)
			if !ok || !account.isSeedanceModelSupported(model) || !key.Group.ModelAllowlist.Allows(model) {
				continue
			}
			variant := publicVideoVariant(capability)
			resolutions := capability.Resolutions
			if len(resolutions) == 0 {
				resolutions = []string{""}
			}
			for _, resolution := range resolutions {
				info := SeedanceVideoRequestInfo{Model: model, Resolution: resolution, DurationSeconds: capability.DefaultDuration, Capability: &capability}
				quote, quoteErr := s.ResolveSeedanceVideoPricingSnapshot(ctx, key, info)
				if quoteErr != nil || quote.SalesSnapshot == nil {
					continue
				}
				purchase, costErr := ResolveVideoTaskAccountCost(account, model, resolution, true, VideoQuantityPolicy(capability.MeteringPolicy), float64(capability.MaxDuration+capability.MaxReferenceVideoSeconds))
				if costErr != nil || validateVideoTaskPurchaseCapability(capability, capability.DefaultDuration, purchase) != nil {
					continue
				}
				price := quote.SalesSnapshot
				variant.Prices = append(variant.Prices, VideoMediaPrice{Resolution: resolution, Version: price.Version,
					BillingUnit: price.BillingUnit, UnitPrice: price.UnitPrice * price.RateMultiplier, QuantityPolicy: price.QuantityPolicy})
			}
			if len(variant.Prices) == 0 {
				continue
			}
			if capability.ResolutionControl {
				variant.Resolutions = nil
				defaultPriced := false
				for _, price := range variant.Prices {
					variant.Resolutions = append(variant.Resolutions, price.Resolution)
					if price.Resolution == capability.DefaultResolution {
						defaultPriced = true
					}
				}
				if !defaultPriced {
					continue
				}
			}
			encoded, err := json.Marshal(variant)
			if err != nil {
				return nil, err
			}
			identity := model + ":" + string(encoded)
			if !seen[identity] {
				byModel[model] = append(byModel[model], variant)
				seen[identity] = true
			}
		}
	}
	for model, variants := range byModel {
		sort.Slice(variants, func(i, j int) bool {
			a, _ := json.Marshal(variants[i])
			b, _ := json.Marshal(variants[j])
			return string(a) < string(b)
		})
		models = append(models, VideoMediaModel{ID: model, Kind: "video", Variants: variants})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func VideoMediaCatalogVersions(models []VideoMediaModel) (string, string) {
	body, _ := json.Marshal(models)
	catalogHash := sha256.Sum256(body)
	prices := []struct {
		Model  string
		Prices []VideoMediaPrice
	}{}
	for _, model := range models {
		for _, variant := range model.Variants {
			prices = append(prices, struct {
				Model  string
				Prices []VideoMediaPrice
			}{model.ID, variant.Prices})
		}
	}
	priceJSON, _ := json.Marshal(prices)
	priceHash := sha256.Sum256(priceJSON)
	return hex.EncodeToString(catalogHash[:]), hex.EncodeToString(priceHash[:])
}
