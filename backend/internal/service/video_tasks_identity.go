package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

const SeedanceVideoTaskIdentityVersion = 1

// Completed tasks retain their original credential for the gateway's seven
// day retrieval window. Unfinished and review tasks retain it without expiry.
const SeedanceVideoTaskCredentialRetention = 7 * 24 * time.Hour

// NewSeedanceVideoPublicTaskID creates an opaque identity before submission.
// A failed entropy source must prevent submission, never fall back to a counter.
func NewSeedanceVideoPublicTaskID() (string, error) {
	return newSeedanceVideoPublicTaskID(rand.Reader)
}

func newSeedanceVideoPublicTaskID(source io.Reader) (string, error) {
	var entropy [16]byte
	if _, err := io.ReadFull(source, entropy[:]); err != nil {
		return "", fmt.Errorf("generate video task identity: %w", err)
	}
	return "vid_" + hex.EncodeToString(entropy[:]), nil
}

func (p *SeedanceVideoPendingBilling) PublicID() string {
	if p == nil {
		return ""
	}
	if p.IDVersion == SeedanceVideoTaskIdentityVersion {
		return strings.TrimSpace(p.StateID)
	}
	return strings.TrimSpace(p.TaskID)
}

func (p *SeedanceVideoPendingBilling) UpstreamID() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.TaskID)
}

func SeedanceVideoPublicTaskID(p *SeedanceVideoPendingBilling) string { return p.PublicID() }

// VideoTasksExecutionSnapshot contains only execution metadata; credentials
// remain attached to the original account and never enter task JSON.
type VideoTasksExecutionSnapshot struct {
	Version           int                                 `json:"version"`
	Protocol          string                              `json:"protocol"`
	BaseURL           string                              `json:"base_url"`
	UpstreamModel     string                              `json:"upstream_model"`
	FieldProfile      string                              `json:"field_profile,omitempty"`
	CapabilityVersion string                              `json:"capability_version,omitempty"`
	SupportsContent   bool                                `json:"supports_content"`
	SupportsCancel    bool                                `json:"supports_cancel"`
	Capability        *videoprovider.VideoModelCapability `json:"capability,omitempty"`
}

func (s *VideoTasksExecutionSnapshot) Validate() error {
	if s == nil {
		return nil
	}
	if s.Version != 1 || strings.TrimSpace(s.UpstreamModel) == "" {
		return fmt.Errorf("invalid video execution snapshot version or model")
	}
	protocol, err := videoprovider.NormalizeID(s.Protocol)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(s.BaseURL)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid video execution snapshot base URL")
	}
	if videoprovider.SupportsModelCapabilities(protocol) && (parsed.Scheme != "https" || s.Capability == nil) {
		return fmt.Errorf("video tasks execution requires HTTPS and frozen capability")
	}
	if s.Capability != nil {
		if err := videoprovider.ValidateVideoModelCapabilities([]videoprovider.VideoModelCapability{*s.Capability}); err != nil {
			return err
		}
		if err := videoprovider.ValidateCapabilityProvider(protocol, *s.Capability); err != nil {
			return err
		}
		if s.FieldProfile != "" && s.FieldProfile != string(s.Capability.Profile) {
			return fmt.Errorf("video execution field profile differs from capability")
		}
		if s.Capability.UpstreamModel != "" && s.Capability.UpstreamModel != s.UpstreamModel {
			return fmt.Errorf("video execution upstream model differs from capability")
		}
	}
	return nil
}

func ValidateSeedanceVideoTaskIdentity(p *SeedanceVideoPendingBilling) error {
	if p == nil || (p.IDVersion != 0 && p.IDVersion != SeedanceVideoTaskIdentityVersion) {
		return fmt.Errorf("unsupported video task identity version")
	}
	if p.IDVersion == SeedanceVideoTaskIdentityVersion {
		if !strings.HasPrefix(p.StateID, "vid_") || len(p.StateID) != 36 {
			return fmt.Errorf("invalid video task public identity")
		}
		if _, err := hex.DecodeString(p.StateID[4:]); err != nil {
			return fmt.Errorf("invalid video task public identity: %w", err)
		}
		if p.DurationSeconds < 0 {
			return fmt.Errorf("negative video task duration")
		}
	}
	if p.SalesSnapshot != nil {
		if err := p.SalesSnapshot.Validate(); err != nil {
			return err
		}
	}
	return p.ExecutionSnapshot.Validate()
}

// AccountForSeedanceVideoTask preserves immutable routing metadata while
// reading the credential from the original account. Never mutate cached rows.
func AccountForSeedanceVideoTask(p *SeedanceVideoPendingBilling, account *Account) (*Account, error) {
	if p == nil || account == nil || account.ID != p.AccountID {
		return nil, fmt.Errorf("video task original account is unavailable")
	}
	if p.ExecutionSnapshot == nil {
		return account, nil
	}
	if err := p.ExecutionSnapshot.Validate(); err != nil {
		return nil, err
	}
	copy := *account
	copy.Credentials = maps.Clone(account.Credentials)
	if copy.Credentials == nil {
		copy.Credentials = make(map[string]any)
	}
	copy.Credentials["base_url"] = p.ExecutionSnapshot.BaseURL
	copy.Credentials["video_provider"] = p.ExecutionSnapshot.Protocol
	copy.Extra = maps.Clone(account.Extra)
	if copy.Extra == nil {
		copy.Extra = make(map[string]any)
	}
	if p.ExecutionSnapshot.Capability != nil {
		copy.Extra["video_model_capabilities"] = []videoprovider.VideoModelCapability{*p.ExecutionSnapshot.Capability}
		copy.Credentials["model_mapping"] = map[string]any{p.ExecutionSnapshot.Capability.Model: p.ExecutionSnapshot.UpstreamModel}
	}
	return &copy, nil
}
