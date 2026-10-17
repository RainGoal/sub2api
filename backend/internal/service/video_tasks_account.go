package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

const VideoModelCapabilitiesExtra = "video_model_capabilities"
const VideoSupplierNameExtra = "video_supplier_name"

func (a *Account) VideoModelCapabilities() ([]videoprovider.VideoModelCapability, error) {
	if a == nil || !a.IsSeedance() {
		return nil, fmt.Errorf("video account is required")
	}
	return videoprovider.ParseVideoModelCapabilities(a.Extra[VideoModelCapabilitiesExtra])
}

func (a *Account) VideoModelCapability(model string) (videoprovider.VideoModelCapability, bool) {
	if a == nil || a.GetVideoProviderID() != videoprovider.ProviderVideoTasksJSONV1 {
		return videoprovider.VideoModelCapability{}, false
	}
	overrides, err := a.VideoModelCapabilities()
	if err != nil {
		return videoprovider.VideoModelCapability{}, false
	}
	capability, ok := videoprovider.LookupVideoModelCapability(model, overrides)
	return capability, ok && capability.Enabled
}

func ValidateVideoTasksAccount(account *Account) error {
	if account == nil || !account.IsSeedance() {
		return nil
	}
	if _, err := account.VideoModelCapabilities(); err != nil {
		return err
	}
	if raw, ok := account.Extra[VideoSupplierNameExtra]; ok {
		name, valid := raw.(string)
		if !valid || len(strings.TrimSpace(name)) > 100 {
			return fmt.Errorf("video supplier name must be a string of at most 100 bytes")
		}
	}
	return nil
}

func (s *OpenAIGatewayService) VideoTasksEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.VideoTasksJSONV1Enabled
}

func validateVideoTaskPurchaseCapability(capability videoprovider.VideoModelCapability, duration int, cost *SeedanceAccountCostSnapshot) error {
	if cost == nil || cost.BillingUnit != VideoBillingPerSecond {
		return nil
	}
	if cost.QuantityPolicy == VideoQuantityRequestedSeconds && duration <= 0 {
		return ErrSeedanceAccountCostMissing
	}
	if cost.MaxBillableSeconds > 0 && float64(duration) > cost.MaxBillableSeconds {
		return ErrSeedanceAccountCostMissing
	}
	if cost.QuantityPolicy != VideoQuantityRequestedSeconds && !capability.MeteringTrusted {
		return ErrSeedanceAccountCostMissing
	}
	return nil
}
