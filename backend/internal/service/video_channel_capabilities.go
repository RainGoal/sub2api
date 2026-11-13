package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

func (s *OpenAIGatewayService) videoChannelLimits(ctx context.Context, key *APIKey, model string) (*videoprovider.VideoCapabilityLimits, error) {
	if s.channelService == nil || key == nil || key.GroupID == nil {
		return nil, nil
	}
	channel, err := s.channelService.seedanceSalesChannelForGroup(ctx, *key.GroupID)
	if err != nil {
		return nil, err
	}
	if channel == nil {
		return nil, nil
	}
	pricing := seedanceStatsPricingForModel(channel.ModelPricing, model)
	if pricing == nil {
		return nil, nil
	}
	return pricing.VideoCapabilities, nil
}

func effectiveVideoAccountCapability(account *Account, model string, limits *videoprovider.VideoCapabilityLimits) (videoprovider.VideoModelCapability, bool) {
	capability, ok := account.VideoModelCapability(model)
	if !ok {
		return capability, false
	}
	capability, err := limits.Apply(capability)
	return capability, err == nil
}

// ListVideoMediaModelsForGroup is used only after the caller has applied plaza
// visibility rules. It shares the key catalog's complete capability contracts.
func (s *OpenAIGatewayService) ListVideoMediaModelsForGroup(ctx context.Context, group *Group) ([]VideoMediaModel, error) {
	if group == nil {
		return nil, fmt.Errorf("video group is required")
	}
	return s.ListVideoMediaModels(ctx, &APIKey{GroupID: &group.ID, Group: group})
}
