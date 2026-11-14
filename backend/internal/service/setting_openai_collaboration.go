package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

const SettingKeyOpenAICollaborationSettings = "openai_collaboration_settings"

const (
	openAICollaborationCacheTTL  = 2 * time.Second
	openAICollaborationDBTimeout = time.Second
)

// OpenAICollaborationSettings is opt-in per authorized group and optional API key.
// Empty GroupIDs enables no traffic; empty APIKeyIDs permits all keys in those groups.
type OpenAICollaborationSettings struct {
	Enabled                     bool    `json:"enabled"`
	PlaintextEnabled            bool    `json:"plaintext_enabled"`
	LegacyCipherRecoveryEnabled bool    `json:"legacy_cipher_recovery_enabled"`
	GroupIDs                    []int64 `json:"group_ids"`
	APIKeyIDs                   []int64 `json:"api_key_ids"`
}

func DefaultOpenAICollaborationSettings() OpenAICollaborationSettings {
	return OpenAICollaborationSettings{GroupIDs: []int64{}, APIKeyIDs: []int64{}}
}

func (s OpenAICollaborationSettings) Allows(groupID, apiKeyID int64) bool {
	return s.Enabled && groupID > 0 && apiKeyID > 0 && slices.Contains(s.GroupIDs, groupID) &&
		(len(s.APIKeyIDs) == 0 || slices.Contains(s.APIKeyIDs, apiKeyID))
}

func (s OpenAICollaborationSettings) clone() OpenAICollaborationSettings {
	s.GroupIDs = append([]int64{}, s.GroupIDs...)
	s.APIKeyIDs = append([]int64{}, s.APIKeyIDs...)
	return s
}

func ValidateOpenAICollaborationSettings(settings OpenAICollaborationSettings) error {
	for name, ids := range map[string][]int64{"group_ids": settings.GroupIDs, "api_key_ids": settings.APIKeyIDs} {
		seen := make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return fmt.Errorf("%s must contain positive IDs", name)
			}
			if _, exists := seen[id]; exists {
				return fmt.Errorf("%s contains duplicate ID %d", name, id)
			}
			seen[id] = struct{}{}
		}
	}
	if settings.Enabled && len(settings.GroupIDs) == 0 {
		return fmt.Errorf("group_ids must not be empty when collaboration compatibility is enabled")
	}
	return nil
}

func parseOpenAICollaborationSettings(raw string) (OpenAICollaborationSettings, error) {
	settings := DefaultOpenAICollaborationSettings()
	if strings.TrimSpace(raw) == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return DefaultOpenAICollaborationSettings(), err
	}
	if err := ValidateOpenAICollaborationSettings(settings); err != nil {
		return DefaultOpenAICollaborationSettings(), err
	}
	return settings.clone(), nil
}

type cachedOpenAICollaborationSettings struct {
	settings  OpenAICollaborationSettings
	expiresAt time.Time
}

func (s *SettingService) GetOpenAICollaborationSettings(ctx context.Context) (OpenAICollaborationSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAICollaborationSettings(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAICollaborationSettings)
	if errors.Is(err, ErrSettingNotFound) {
		return DefaultOpenAICollaborationSettings(), nil
	}
	if err != nil {
		return DefaultOpenAICollaborationSettings(), err
	}
	return parseOpenAICollaborationSettings(raw)
}

// GetOpenAICollaborationSettingsCached fails closed on missing or unreadable settings.
// Other instances observe a saved change after at most the 2s cache lifetime plus
// the bounded database read. Each logical request/WS turn must take its own snapshot.
func (s *SettingService) GetOpenAICollaborationSettingsCached(ctx context.Context) OpenAICollaborationSettings {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAICollaborationSettings()
	}
	readCache := func() (OpenAICollaborationSettings, bool) {
		cached, ok := s.openAICollaborationCache.Load().(*cachedOpenAICollaborationSettings)
		if ok && cached != nil && time.Now().Before(cached.expiresAt) {
			return cached.settings.clone(), true
		}
		return OpenAICollaborationSettings{}, false
	}
	if settings, ok := readCache(); ok {
		return settings
	}
	s.openAICollaborationMu.Lock()
	defer s.openAICollaborationMu.Unlock()
	if settings, ok := readCache(); ok {
		return settings
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAICollaborationDBTimeout)
	defer cancel()
	settings, err := s.GetOpenAICollaborationSettings(dbCtx)
	if err != nil {
		slog.Warn("OpenAI collaboration compatibility disabled because settings could not be loaded", "error", err)
		settings = DefaultOpenAICollaborationSettings()
	}
	s.storeOpenAICollaborationCache(settings)
	return settings.clone()
}

func (s *SettingService) storeOpenAICollaborationCache(settings OpenAICollaborationSettings) {
	s.openAICollaborationCache.Store(&cachedOpenAICollaborationSettings{
		settings: settings.clone(), expiresAt: time.Now().Add(openAICollaborationCacheTTL),
	})
}

// persistSettingsWithCollaborationCache serializes a collaboration write with
// cache refresh, so an earlier database read cannot overwrite a saved shutdown.
func (s *SettingService) persistSettingsWithCollaborationCache(ctx context.Context, updates map[string]string) error {
	raw, touched := updates[SettingKeyOpenAICollaborationSettings]
	if !touched {
		return s.settingRepo.SetMultiple(ctx, updates)
	}
	settings, err := parseOpenAICollaborationSettings(raw)
	if err != nil {
		return err
	}
	s.openAICollaborationMu.Lock()
	defer s.openAICollaborationMu.Unlock()
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	s.storeOpenAICollaborationCache(settings)
	return nil
}

func (s *SettingService) SetOpenAICollaborationSettings(ctx context.Context, settings OpenAICollaborationSettings) error {
	if err := ValidateOpenAICollaborationSettings(settings); err != nil {
		return err
	}
	data, err := json.Marshal(settings.clone())
	if err != nil {
		return err
	}
	return s.persistSettingsWithCollaborationCache(ctx, map[string]string{SettingKeyOpenAICollaborationSettings: string(data)})
}
