package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
)

type modelPlazaMonitorCatalog interface {
	ListGroups(context.Context) ([]PlazaGroup, error)
}
type modelPlazaMonitorKeys interface {
	GetByID(context.Context, int64) (*APIKey, error)
	ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error)
}

type ModelPlazaMonitorService struct {
	repo      ModelPlazaMonitorRepository
	settings  SettingRepository
	plaza     modelPlazaMonitorCatalog
	keys      modelPlazaMonitorKeys
	baseURL   string
	client    *http.Client
	now       func() time.Time
	startOnce sync.Once
	stopOnce  sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	workers   chan struct{}
	wg        sync.WaitGroup
	stateMu   sync.Mutex
	stopped   bool
}

func NewModelPlazaMonitorService(repo ModelPlazaMonitorRepository, settings SettingRepository, plaza *ModelPlazaService, keys APIKeyRepository, cfg *config.Config) *ModelPlazaMonitorService {
	ctx, cancel := context.WithCancel(context.Background())
	port := 8080
	if cfg != nil && cfg.Server.Port > 0 {
		port = cfg.Server.Port
	}
	return &ModelPlazaMonitorService{
		repo: repo, settings: settings, plaza: plaza, keys: keys,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:     func() time.Time { return time.Now().UTC() },
		ctx:     ctx, cancel: cancel, workers: make(chan struct{}, 3),
	}
}

func (s *ModelPlazaMonitorService) GetConfig(ctx context.Context) (*ModelPlazaMonitorConfig, error) {
	cfg := &ModelPlazaMonitorConfig{DefaultIntervalSeconds: modelPlazaMonitorDefaultInterval, Groups: []ModelPlazaMonitorGroupConfig{}}
	value, err := s.settings.GetValue(ctx, SettingKeyModelPlazaMonitor)
	if errors.Is(err, ErrSettingNotFound) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(value), cfg); err != nil {
		return nil, fmt.Errorf("decode model plaza monitor config: %w", err)
	}
	if cfg.Groups == nil {
		cfg.Groups = []ModelPlazaMonitorGroupConfig{}
	}
	return cfg, nil
}

func (s *ModelPlazaMonitorService) Candidates(ctx context.Context) (*ModelPlazaMonitorCandidates, error) {
	groups, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := &ModelPlazaMonitorCandidates{Groups: []ModelPlazaMonitorCandidateGroup{}, APIKeys: []ModelPlazaMonitorCandidateKey{}}
	catalog := modelPlazaMonitorCatalogMap(groups)
	for _, group := range groups {
		g := ModelPlazaMonitorCandidateGroup{ID: group.ID, Name: group.Name, Platform: group.Platform, Models: []ModelPlazaMonitorCandidateModel{}}
		for _, model := range group.Models {
			g.Models = append(g.Models, ModelPlazaMonitorCandidateModel{Name: model.Name, Platform: model.Platform, Supported: catalog[group.ID][model.Platform+"\x00"+model.Name]})
		}
		out.Groups = append(out.Groups, g)
		// All pages are read; a group with many keys must not silently lose its
		// monitoring credential from the selector. Only metadata reaches the DTO.
		for page := 1; ; page++ {
			keys, info, err := s.keys.ListByGroupID(ctx, group.ID, pagination.PaginationParams{Page: page, PageSize: 100})
			if err != nil {
				return nil, err
			}
			for i := range keys {
				// ListByGroupID omits Group; load the same complete credential used
				// when saving so group fallback cannot appear as a valid candidate.
				key, err := s.keys.GetByID(ctx, keys[i].ID)
				if err != nil {
					return nil, err
				}
				if modelPlazaProbeKeyValid(key, group.ID) {
					out.APIKeys = append(out.APIKeys, ModelPlazaMonitorCandidateKey{ID: key.ID, Name: key.Name, GroupID: group.ID})
				}
			}
			if len(keys) < 100 || info == nil || page*100 >= int(info.Total) {
				break
			}
		}
	}
	return out, nil
}

func (s *ModelPlazaMonitorService) UpdateConfig(ctx context.Context, cfg ModelPlazaMonitorConfig) (*ModelPlazaMonitorConfig, error) {
	if cfg.Version < 0 || cfg.Version >= 2147483646 || !modelPlazaMonitorIntervalValid(cfg.DefaultIntervalSeconds) || len(cfg.Groups) > 100 {
		return nil, ErrModelPlazaMonitorConfig
	}
	groups, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	catalog := modelPlazaMonitorCatalogMap(groups)
	seen := map[int64]bool{}
	targets := []ModelPlazaMonitorTarget{}
	for i := range cfg.Groups {
		group := &cfg.Groups[i]
		if group.GroupID <= 0 || seen[group.GroupID] || !modelPlazaMonitorIntervalValid(group.IntervalSeconds) || len(group.Models) > 200 {
			return nil, ErrModelPlazaMonitorConfig
		}
		seen[group.GroupID] = true
		models := map[string]bool{}
		for j := range group.Models {
			model := &group.Models[j]
			if model.IntervalSeconds < 0 || model.IntervalSeconds > modelPlazaMonitorMaxInterval || model.Name == "" || len(model.Name) > 256 || !modelPlazaProbePlatformSupported(model.Platform) {
				return nil, ErrModelPlazaMonitorConfig
			}
			if model.APIMode == "" {
				model.APIMode = MonitorAPIModeChatCompletions
			}
			if model.APIMode != MonitorAPIModeChatCompletions && (model.Platform != PlatformOpenAI || model.APIMode != MonitorAPIModeResponses) {
				return nil, ErrModelPlazaMonitorConfig
			}
			key := model.Platform + "\x00" + model.Name
			if models[key] {
				return nil, ErrModelPlazaMonitorConfig
			}
			models[key] = true
			if cfg.Enabled && group.Enabled && !catalog[group.GroupID][key] {
				return nil, ErrModelPlazaMonitorConfig
			}
		}
		if cfg.Enabled && group.Enabled {
			if len(group.Models) == 0 {
				return nil, ErrModelPlazaMonitorConfig
			}
			key, err := s.keys.GetByID(ctx, group.ProbeAPIKeyID)
			if err != nil {
				return nil, ErrModelPlazaMonitorConfig
			}
			if !modelPlazaProbeKeyValid(key, group.GroupID) {
				return nil, ErrModelPlazaMonitorConfig
			}
			if cfg.Enabled {
				targets = append(targets, modelPlazaMonitorGroupTargets(*group)...)
			}
		}
	}
	if len(targets) > 1000 {
		return nil, ErrModelPlazaMonitorConfig
	}
	if cfg.Groups == nil {
		cfg.Groups = []ModelPlazaMonitorGroupConfig{}
	}
	if err := s.repo.SaveConfig(ctx, cfg, targets); err != nil {
		return nil, err
	}
	cfg.Version++
	return &cfg, nil
}

func modelPlazaMonitorIntervalValid(value int) bool {
	return value > 0 && value <= modelPlazaMonitorMaxInterval
}

func modelPlazaProbeKeyValid(key *APIKey, groupID int64) bool {
	if key == nil || key.GroupID == nil || *key.GroupID != groupID || !key.IsActive() || key.IsExpired() || key.IsQuotaExhausted() || key.Key == "" || key.FallbackGroupID != nil {
		return false
	}
	if key.Group != nil && (key.Group.FallbackGroupID != nil || key.Group.FallbackGroupIDOnInvalidRequest != nil) {
		return false
	}
	return true
}

func modelPlazaMonitorGroupTargets(group ModelPlazaMonitorGroupConfig) []ModelPlazaMonitorTarget {
	out := make([]ModelPlazaMonitorTarget, 0, len(group.Models))
	for _, model := range group.Models {
		interval := model.IntervalSeconds
		if interval == 0 {
			interval = group.IntervalSeconds
		}
		t := ModelPlazaMonitorTarget{GroupID: group.GroupID, Platform: model.Platform, Model: model.Name, APIMode: model.APIMode, IntervalSeconds: interval, APIKeyID: group.ProbeAPIKeyID}
		data, _ := json.Marshal(t)
		sum := sha256.Sum256(data)
		t.Signature = hex.EncodeToString(sum[:])
		out = append(out, t)
	}
	return out
}

func modelPlazaMonitorCatalogMap(groups []PlazaGroup) map[int64]map[string]bool {
	out := map[int64]map[string]bool{}
	for _, group := range groups {
		models := map[string]bool{}
		platforms := map[string]string{}
		ambiguous := map[string]bool{}
		for _, model := range group.Models {
			if platform, seen := platforms[model.Name]; seen && platform != model.Platform {
				ambiguous[model.Name] = true
			}
			platforms[model.Name] = model.Platform
		}
		for _, model := range group.Models {
			// Gateway requests carry only the model name; a platform in the
			// monitor target cannot disambiguate cross-platform routing.
			models[model.Platform+"\x00"+model.Name] = !ambiguous[model.Name] && modelPlazaProbeSupported(model)
		}
		out[group.ID] = models
	}
	return out
}

func (s *ModelPlazaMonitorService) GetStatus(ctx context.Context, groupID int64) (*ModelPlazaMonitorStatusResponse, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	out := &ModelPlazaMonitorStatusResponse{Enabled: cfg.Enabled, ComputedAt: s.now(), Items: []ModelPlazaMonitorStatus{}}
	if !cfg.Enabled {
		return out, nil
	}
	groups, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	catalog := modelPlazaMonitorCatalogMap(groups)
	states, err := s.repo.ListStates(ctx, groupID)
	if err != nil {
		return nil, err
	}
	for _, state := range states {
		t := state.Target
		if !catalog[t.GroupID][t.Platform+"\x00"+t.Model] {
			continue
		}
		history, err := s.repo.History(ctx, state.ID, modelPlazaMonitorHistoryLimit)
		if err != nil {
			return nil, err
		}
		status := state.Status
		if state.LastCheckedAt == nil || out.ComputedAt.Sub(*state.LastCheckedAt) > time.Duration(t.IntervalSeconds*2+30)*time.Second {
			status = "unknown"
		}
		next := state.NextCheckAt
		out.Items = append(out.Items, ModelPlazaMonitorStatus{GroupID: t.GroupID, Platform: t.Platform, Model: t.Model, IntervalSeconds: t.IntervalSeconds, Status: status, LastCheckedAt: state.LastCheckedAt, NextCheckAt: &next, Timeline: history})
	}
	return out, nil
}

func (s *ModelPlazaMonitorService) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		if s.stopped {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					if err := s.runDue(s.ctx, 0); err != nil && !errors.Is(err, context.Canceled) {
						slog.Warn("model_plaza_monitor_schedule_failed", "error", err)
					}
				}
			}
		}()
	})
}

func (s *ModelPlazaMonitorService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.stateMu.Lock()
		s.stopped = true
		s.cancel()
		s.stateMu.Unlock()
		s.wg.Wait()
	})
}

// Run queues due saved targets. It cannot bypass the configured interval, so
// repeated clicks cannot turn a low-cost health check into unbounded traffic.
func (s *ModelPlazaMonitorService) Run(ctx context.Context, groupID int64) (*ModelPlazaMonitorStatusResponse, error) {
	cfg, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrModelPlazaMonitorDisabled
	}
	if err := s.runDue(ctx, groupID); err != nil {
		return nil, err
	}
	return s.GetStatus(ctx, groupID)
}

func (s *ModelPlazaMonitorService) runDue(ctx context.Context, groupID int64) error {
	cfg, err := s.GetConfig(ctx)
	if err != nil || !cfg.Enabled {
		return err
	}
	states, err := s.repo.ListStates(ctx, groupID)
	if err != nil {
		return err
	}
	now := s.now()
	due := []ModelPlazaMonitorState{}
	for _, state := range states {
		if !state.NextCheckAt.After(now) {
			due = append(due, state)
		}
	}
	if len(due) == 0 {
		return nil
	}
	groups, err := s.plaza.ListGroups(ctx)
	if err != nil {
		return err
	}
	catalog := modelPlazaMonitorCatalogMap(groups)
	for _, pending := range due {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case s.workers <- struct{}{}:
		default:
			return nil
		}
		state, err := s.repo.Claim(ctx, pending.Target, now, uuid.NewString())
		if err != nil || state == nil {
			<-s.workers
			if err != nil {
				return err
			}
			continue
		}
		valid := catalog[state.Target.GroupID][state.Target.Platform+"\x00"+state.Target.Model]
		s.stateMu.Lock()
		if s.stopped {
			s.stateMu.Unlock()
			<-s.workers
			return s.ctx.Err()
		}
		s.wg.Add(1)
		s.stateMu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() { <-s.workers }()
			checkCtx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			result := ModelPlazaMonitorCheck{Status: "unknown", CheckedAt: s.now(), Reason: "configuration_invalid"}
			if valid {
				key, keyErr := s.keys.GetByID(checkCtx, state.Target.APIKeyID)
				if keyErr == nil && modelPlazaProbeKeyValid(key, state.Target.GroupID) {
					result = s.probe(checkCtx, state.Target, key.Key)
				}
			}
			// This also checks the lease and target identity in one transaction;
			// a stopped/reconfigured target cannot publish an outdated green result.
			if s.ctx.Err() != nil {
				return
			}
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer saveCancel()
			if err := s.repo.Complete(saveCtx, *state, result); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("model_plaza_monitor_save_failed", "error", err)
			}
		}()
	}
	return nil
}
