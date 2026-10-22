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
	if a == nil || !videoprovider.SupportsModelCapabilities(a.GetVideoProviderID()) {
		return videoprovider.VideoModelCapability{}, false
	}
	overrides, err := a.VideoModelCapabilities()
	if err != nil {
		return videoprovider.VideoModelCapability{}, false
	}
	upstream := a.GetModelMapping()[model]
	capability, ok := lookupAccountVideoModelCapability(model, upstream, overrides)
	if !ok || !capability.Enabled || (capability.UpstreamModel != "" && upstream != "" && capability.UpstreamModel != upstream) {
		return videoprovider.VideoModelCapability{}, false
	}
	if upstream != "" {
		capability.UpstreamModel = upstream
	}
	capability.Model = model
	if err := videoprovider.ValidateCapabilityProvider(a.GetVideoProviderID(), capability); err != nil {
		return videoprovider.VideoModelCapability{}, false
	}
	return capability, true
}

// Explicit public overrides (including disables) win over mapped overrides;
// supplier presets are only a fallback after both account-local identities.
func lookupAccountVideoModelCapability(model, upstream string, overrides []videoprovider.VideoModelCapability) (videoprovider.VideoModelCapability, bool) {
	for _, name := range []string{model, upstream} {
		if name == "" {
			continue
		}
		for _, capability := range overrides {
			if strings.EqualFold(capability.Model, name) {
				return videoprovider.LookupVideoModelCapability(name, overrides)
			}
		}
	}
	if capability, ok := videoprovider.LookupVideoModelCapability(model, nil); ok {
		return capability, true
	}
	return videoprovider.LookupVideoModelCapability(upstream, nil)
}

func ValidateVideoTasksAccount(account *Account) error {
	if account == nil || !account.IsSeedance() {
		return nil
	}
	capabilities, err := account.VideoModelCapabilities()
	if err != nil {
		return err
	}
	if videoprovider.SupportsModelCapabilities(account.GetVideoProviderID()) {
		for _, capability := range capabilities {
			if err := videoprovider.ValidateCapabilityProvider(account.GetVideoProviderID(), capability); err != nil {
				return err
			}
			if target := account.GetModelMapping()[capability.Model]; target != "" && capability.UpstreamModel != "" && target != capability.UpstreamModel {
				return fmt.Errorf("video capability upstream model differs from account mapping")
			}
		}
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

func videoTaskReferenceMeteringUnsupported(provider videoprovider.ID, references int, unit VideoBillingUnit, policy VideoQuantityPolicy) bool {
	return references > 0 && unit == VideoBillingPerSecond && policy == VideoQuantityOutputAndReferenceSeconds &&
		videoprovider.SupportsModelCapabilities(provider) && !videoprovider.SupportsReferenceVideoMetering(provider)
}
