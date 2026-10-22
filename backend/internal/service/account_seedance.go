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

// Video model selection is an allowlist, not a cross-model rewrite rule.
func validateSeedanceModelSelection(driver videoprovider.Driver, raw any) error {
	if raw == nil {
		return nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("seedance model_mapping must be a model allowlist")
	}
	for source, target := range mapping {
		canonical, known := videoprovider.CanonicalModel(source)
		if !known || !driver.SupportsModel(source) {
			return fmt.Errorf("seedance model %q is not supported by this provider", source)
		}
		value, isString := target.(string)
		mapped, targetKnown := videoprovider.CanonicalModel(value)
		if !isString || !targetKnown || canonical != mapped {
			return fmt.Errorf("seedance model selection must preserve the selected model %q", source)
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
	driver, err := videoprovider.Resolve(string(a.GetVideoProviderID()))
	if err != nil || !driver.SupportsModel(requestedModel) {
		return false
	}
	mapping := a.GetModelMapping()
	if len(mapping) == 0 {
		return true
	}
	canonical, _ := videoprovider.CanonicalModel(requestedModel)
	for selected := range mapping {
		if model, known := videoprovider.CanonicalModel(selected); known && model == canonical {
			return true
		}
	}
	return false
}

func validVideoModelName(model string) bool {
	return model != "" && model == strings.TrimSpace(model) && len(model) <= 128 && !strings.ContainsAny(model, "*\r\n\x00")
}
