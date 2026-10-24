package service

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

func validateSeedanceAccountCredentials(platform, accountType string, credentials map[string]any) error {
	if platform != PlatformSeedance {
		return nil
	}
	if accountType != AccountTypeAPIKey {
		return fmt.Errorf("seedance accounts only support apikey type")
	}
	apiKey, _ := credentials["api_key"].(string)
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("seedance api_key is required")
	}
	providerID, _ := credentials["video_provider"].(string)
	driver, err := videoprovider.Resolve(providerID)
	if err != nil {
		return err
	}
	if videoprovider.SupportsModelCapabilities(driver.ID()) {
		baseURL, _ := credentials["base_url"].(string)
		if strings.TrimSpace(baseURL) == "" {
			baseURL = driver.DefaultBaseURL()
		}
		if _, err := videoprovider.NormalizeVideoTasksBaseURL(baseURL); err != nil {
			return err
		}
		mapping, ok := credentials["model_mapping"].(map[string]any)
		if !ok || len(mapping) == 0 {
			return fmt.Errorf("video tasks accounts require an explicit model allowlist")
		}
		for model, target := range mapping {
			value, ok := target.(string)
			if !ok || !validVideoModelName(model) || !validVideoModelName(value) {
				return fmt.Errorf("video model mapping requires explicit public and upstream model names")
			}
		}
		return nil
	}
	if err := validateSeedanceModelSelection(driver, credentials["model_mapping"]); err != nil {
		return err
	}
	rawBaseURL, _ := credentials["base_url"].(string)
	if strings.TrimSpace(rawBaseURL) == "" {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("seedance base_url is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("seedance base_url must use HTTPS")
	}
	return nil
}

// Legacy protocols accept public aliases for their supported upstream models.
func validateSeedanceModelSelection(driver videoprovider.Driver, raw any) error {
	if raw == nil {
		return nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("seedance model_mapping must be a model allowlist")
	}
	for source, target := range mapping {
		value, isString := target.(string)
		if !isString || !validVideoModelName(source) || !validVideoModelName(value) {
			return fmt.Errorf("video model mapping requires explicit public and upstream model names")
		}
		if !driver.SupportsModel(value) {
			return fmt.Errorf("seedance upstream model %q is not supported by this provider", value)
		}
	}
	return nil
}

func (a *Account) isSeedanceModelSupported(requestedModel string) bool {
	if videoprovider.SupportsModelCapabilities(a.GetVideoProviderID()) {
		if _, ok := a.GetModelMapping()[requestedModel]; !ok {
			return false
		}
		_, ok := a.VideoModelCapability(requestedModel)
		return ok
	}
	_, err := a.ResolveSeedanceUpstreamModel(requestedModel)
	return err == nil
}

// ResolveSeedanceUpstreamModel returns the exact model sent by this account.
// The result is already mapped and must not pass through account mapping again.
func (a *Account) ResolveSeedanceUpstreamModel(public string) (string, error) {
	if a == nil || !a.IsSeedance() || !validVideoModelName(public) {
		return "", fmt.Errorf("valid video account and public model are required")
	}
	provider := a.GetVideoProviderID()
	if videoprovider.SupportsModelCapabilities(provider) {
		if _, ok := a.GetModelMapping()[public]; !ok {
			return "", fmt.Errorf("video model is not enabled on this account")
		}
		capability, ok := a.VideoModelCapability(public)
		if !ok {
			return "", fmt.Errorf("video model capability is unavailable")
		}
		return capability.UpstreamModel, nil
	}
	driver, err := videoprovider.Resolve(string(provider))
	if err != nil {
		return "", err
	}
	mapping := a.GetModelMapping()
	if len(mapping) == 0 {
		return videoprovider.ResolveLegacyUpstreamModel(provider, public)
	}
	if target, ok := mapping[public]; ok {
		if !validVideoModelName(target) || !driver.SupportsModel(target) {
			return "", fmt.Errorf("video upstream model is not supported by this provider")
		}
		// Existing identity entries were model selection, before mapping was
		// supported. Keep their driver's historical spelling and aliases.
		if target == public {
			return videoprovider.ResolveLegacyUpstreamModel(provider, public)
		}
		return target, nil
	}
	if canonical, known := videoprovider.CanonicalModel(public); known {
		for source, target := range mapping {
			if source != target {
				continue
			}
			if selected, ok := videoprovider.CanonicalModel(source); ok && selected == canonical {
				return videoprovider.ResolveLegacyUpstreamModel(provider, public)
			}
		}
	}
	return "", fmt.Errorf("video model is not enabled on this account")
}

func (a *Account) hasSeedanceModelRemapping(public string) bool {
	if a == nil {
		return false
	}
	target, exists := a.GetModelMapping()[public]
	return exists && target != public
}

func validVideoModelName(model string) bool {
	return model != "" && model == strings.TrimSpace(model) && len(model) <= 128 && !strings.ContainsAny(model, "*\r\n\x00")
}
