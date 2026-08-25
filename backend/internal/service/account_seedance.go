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
	if _, err := videoprovider.Resolve(providerID); err != nil {
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
