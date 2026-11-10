package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type plazaMonitorSettingsStub struct {
	SettingRepository
	value string
	err   error
}

func (s *plazaMonitorSettingsStub) GetValue(context.Context, string) (string, error) {
	return s.value, s.err
}

type plazaMonitorCatalogStub struct{ groups []PlazaGroup }

func (s *plazaMonitorCatalogStub) ListGroups(context.Context) ([]PlazaGroup, error) {
	return s.groups, nil
}

type plazaMonitorKeysStub struct {
	key        *APIKey
	listedKeys []APIKey
}

func (s *plazaMonitorKeysStub) GetByID(context.Context, int64) (*APIKey, error) { return s.key, nil }
func (s *plazaMonitorKeysStub) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	if s.listedKeys != nil {
		return s.listedKeys, nil, nil
	}
	if s.key == nil {
		return nil, nil, nil
	}
	return []APIKey{*s.key}, nil, nil
}

type plazaMonitorRepoStub struct {
	mu        sync.Mutex
	targets   []ModelPlazaMonitorTarget
	states    []ModelPlazaMonitorState
	history   []ModelPlazaMonitorPoint
	completed chan ModelPlazaMonitorCheck
}

func (r *plazaMonitorRepoStub) SaveConfig(_ context.Context, _ ModelPlazaMonitorConfig, targets []ModelPlazaMonitorTarget) error {
	r.targets = targets
	return nil
}
func (r *plazaMonitorRepoStub) ListStates(_ context.Context, groupID int64) ([]ModelPlazaMonitorState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []ModelPlazaMonitorState{}
	for _, state := range r.states {
		if groupID == 0 || state.Target.GroupID == groupID {
			out = append(out, state)
		}
	}
	return out, nil
}
func (r *plazaMonitorRepoStub) Claim(_ context.Context, target ModelPlazaMonitorTarget, now time.Time, token string) (*ModelPlazaMonitorState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.states {
		state := &r.states[i]
		if state.Target.Signature == target.Signature && state.LeaseToken == "" && !state.NextCheckAt.After(now) {
			state.LeaseToken = token
			copy := *state
			return &copy, nil
		}
	}
	return nil, nil
}
func (r *plazaMonitorRepoStub) Complete(_ context.Context, state ModelPlazaMonitorState, result ModelPlazaMonitorCheck) error {
	r.mu.Lock()
	for i := range r.states {
		if r.states[i].ID == state.ID {
			r.states[i].NextCheckAt = result.CheckedAt.Add(time.Duration(state.Target.IntervalSeconds) * time.Second)
			r.states[i].LeaseToken = ""
		}
	}
	r.mu.Unlock()
	if r.completed != nil {
		r.completed <- result
	}
	return nil
}
func (r *plazaMonitorRepoStub) History(context.Context, int64, int) ([]ModelPlazaMonitorPoint, error) {
	return r.history, nil
}

func newPlazaMonitorFixture(t *testing.T) (*ModelPlazaMonitorService, *plazaMonitorRepoStub, ModelPlazaMonitorConfig) {
	t.Helper()
	groupID := int64(7)
	key := &APIKey{ID: 3, Key: "monitor-test-secret", Name: "probe", GroupID: &groupID, Status: StatusActive}
	cfg := ModelPlazaMonitorConfig{Enabled: true, DefaultIntervalSeconds: 300, Groups: []ModelPlazaMonitorGroupConfig{{GroupID: 7, Enabled: true, IntervalSeconds: 30, ProbeAPIKeyID: 3, Models: []ModelPlazaMonitorModelConfig{{Platform: PlatformOpenAI, Name: "gpt-4.1", APIMode: MonitorAPIModeChatCompletions}}}}}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &plazaMonitorRepoStub{}
	s := NewModelPlazaMonitorService(repo, &plazaMonitorSettingsStub{value: string(data)}, nil, nil, nil)
	s.plaza = &plazaMonitorCatalogStub{groups: []PlazaGroup{{ID: 7, Models: []PlazaModel{{Name: "gpt-4.1", Platform: PlatformOpenAI}}}}}
	s.keys = &plazaMonitorKeysStub{key: key}
	t.Cleanup(s.Stop)
	return s, repo, cfg
}

func TestModelPlazaMonitorDisabledByDefault(t *testing.T) {
	s, _, _ := newPlazaMonitorFixture(t)
	s.settings = &plazaMonitorSettingsStub{err: ErrSettingNotFound}
	cfg, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Equal(t, 300, cfg.DefaultIntervalSeconds)
	require.Empty(t, cfg.Groups)
	_, err = s.Run(context.Background(), 7)
	require.ErrorIs(t, err, ErrModelPlazaMonitorDisabled)
}

func TestModelPlazaMonitorIntervalsAndIdentity(t *testing.T) {
	g := ModelPlazaMonitorGroupConfig{GroupID: 7, IntervalSeconds: 300, ProbeAPIKeyID: 3, Models: []ModelPlazaMonitorModelConfig{{Platform: PlatformOpenAI, Name: "same"}, {Platform: PlatformAnthropic, Name: "same", IntervalSeconds: 19}}}
	targets := modelPlazaMonitorGroupTargets(g)
	require.Equal(t, 300, targets[0].IntervalSeconds)
	require.Equal(t, 19, targets[1].IntervalSeconds)
	require.NotEqual(t, targets[0].Signature, targets[1].Signature)
	g.GroupID = 8
	require.NotEqual(t, targets[0].Signature, modelPlazaMonitorGroupTargets(g)[0].Signature)
	g.GroupID = 7
	g.IntervalSeconds = 301
	require.NotEqual(t, targets[0].Signature, modelPlazaMonitorGroupTargets(g)[0].Signature)
}

func TestModelPlazaMonitorRejectsInvalidConfiguration(t *testing.T) {
	for _, name := range []string{"zero interval", "duplicate", "wrong key group", "unsupported model", "fallback key"} {
		t.Run(name, func(t *testing.T) {
			s, _, cfg := newPlazaMonitorFixture(t)
			keys, ok := s.keys.(*plazaMonitorKeysStub)
			require.True(t, ok)
			switch name {
			case "zero interval":
				cfg.Groups[0].IntervalSeconds = 0
			case "duplicate":
				cfg.Groups[0].Models = append(cfg.Groups[0].Models, cfg.Groups[0].Models[0])
			case "wrong key group":
				id := int64(99)
				keys.key.GroupID = &id
			case "unsupported model":
				cfg.Groups[0].Models[0].Name = "not-in-catalog"
			case "fallback key":
				id := int64(9)
				keys.key.FallbackGroupID = &id
			}
			_, err := s.UpdateConfig(context.Background(), cfg)
			require.ErrorIs(t, err, ErrModelPlazaMonitorConfig)
		})
	}
}

func TestModelPlazaMonitorCanDisableInvalidatedCredential(t *testing.T) {
	s, repo, cfg := newPlazaMonitorFixture(t)
	cfg.Enabled = false
	keys, ok := s.keys.(*plazaMonitorKeysStub)
	require.True(t, ok)
	keys.key.Status = StatusAPIKeyDisabled
	updated, err := s.UpdateConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.False(t, updated.Enabled)
	require.Empty(t, repo.targets)
}

func TestModelPlazaMonitorStoresOnlyChosenModels(t *testing.T) {
	s, repo, cfg := newPlazaMonitorFixture(t)
	updated, err := s.UpdateConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, 1, updated.Version)
	require.Len(t, repo.targets, 1)
	require.Equal(t, "gpt-4.1", repo.targets[0].Model)
	candidates, err := s.Candidates(context.Background())
	require.NoError(t, err)
	data, err := json.Marshal(candidates)
	require.NoError(t, err)
	require.NotContains(t, string(data), "monitor-test-secret")
}

func TestModelPlazaMonitorCandidatesLoadCredentialGroup(t *testing.T) {
	for _, fallback := range []string{"none", "group", "invalid request"} {
		t.Run(fallback, func(t *testing.T) {
			s, _, cfg := newPlazaMonitorFixture(t)
			keys, ok := s.keys.(*plazaMonitorKeysStub)
			require.True(t, ok)
			keys.listedKeys = []APIKey{*keys.key}
			keys.key.Group = &Group{}
			fallbackID := int64(9)
			switch fallback {
			case "group":
				keys.key.Group.FallbackGroupID = &fallbackID
			case "invalid request":
				keys.key.Group.FallbackGroupIDOnInvalidRequest = &fallbackID
			}
			candidates, err := s.Candidates(context.Background())
			require.NoError(t, err)
			_, err = s.UpdateConfig(context.Background(), cfg)
			if fallback == "none" {
				require.Len(t, candidates.APIKeys, 1)
				require.NoError(t, err)
			} else {
				require.Empty(t, candidates.APIKeys)
				require.ErrorIs(t, err, ErrModelPlazaMonitorConfig)
			}
		})
	}
}

func TestModelPlazaMonitorCatalogRejectsCrossPlatformModelNames(t *testing.T) {
	groups := []PlazaGroup{
		{ID: 7, Models: []PlazaModel{
			{Name: "shared", Platform: PlatformOpenAI},
			{Name: "shared", Platform: PlatformGrok},
			{Name: "unique", Platform: PlatformOpenAI},
			{Name: "unique", Platform: PlatformOpenAI},
		}},
		{ID: 8, Models: []PlazaModel{{Name: "unique", Platform: PlatformGrok}}},
	}
	catalog := modelPlazaMonitorCatalogMap(groups)
	require.False(t, catalog[7][PlatformOpenAI+"\x00shared"])
	require.False(t, catalog[7][PlatformGrok+"\x00shared"])
	require.True(t, catalog[7][PlatformOpenAI+"\x00unique"])
	require.True(t, catalog[8][PlatformGrok+"\x00unique"])
}

func TestModelPlazaMonitorCannotSelectOrPublishAmbiguousModel(t *testing.T) {
	s, repo, cfg := newPlazaMonitorFixture(t)
	catalog, ok := s.plaza.(*plazaMonitorCatalogStub)
	require.True(t, ok)
	catalog.groups[0].Models = append(catalog.groups[0].Models, PlazaModel{Name: "gpt-4.1", Platform: PlatformGrok})
	candidates, err := s.Candidates(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates.Groups[0].Models, 2)
	for _, model := range candidates.Groups[0].Models {
		require.False(t, model.Supported)
	}
	_, err = s.UpdateConfig(context.Background(), cfg)
	require.ErrorIs(t, err, ErrModelPlazaMonitorConfig)
	target := modelPlazaMonitorGroupTargets(cfg.Groups[0])[0]
	checked := time.Now().UTC()
	repo.states = []ModelPlazaMonitorState{{ID: 1, Target: target, Status: "operational", LastCheckedAt: &checked, NextCheckAt: checked.Add(-time.Minute)}}
	repo.completed = make(chan ModelPlazaMonitorCheck, 1)
	status, err := s.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, status.Items)
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer server.Close()
	s.baseURL = server.URL
	require.NoError(t, s.runDue(context.Background(), 7))
	select {
	case check := <-repo.completed:
		require.Equal(t, "unknown", check.Status)
		require.Equal(t, "configuration_invalid", check.Reason)
	case <-time.After(3 * time.Second):
		t.Fatal("invalidated target did not complete")
	}
	require.Empty(t, requests)
}

func TestModelPlazaMonitorRequestOnlyHiAndBoundedOutput(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformKimi, PlatformDeepseek, PlatformZhipu, PlatformMiniMax} {
		t.Run(platform, func(t *testing.T) {
			_, body, err := modelPlazaProbeRequest(ModelPlazaMonitorTarget{Platform: platform, Model: "test-model"})
			require.NoError(t, err)
			data, err := json.Marshal(body)
			require.NoError(t, err)
			require.Contains(t, string(data), "hi")
			require.NotContains(t, string(data), "system")
			require.NotContains(t, string(data), "instructions")
			require.NotContains(t, string(data), "challenge")
			require.NotContains(t, string(data), "50")
		})
	}
	_, body, err := modelPlazaProbeRequest(ModelPlazaMonitorTarget{Platform: PlatformOpenAI, Model: "gpt-5", APIMode: MonitorAPIModeResponses})
	require.NoError(t, err)
	require.Equal(t, "hi", body["input"])
	require.Equal(t, 16, body["max_output_tokens"])
	require.False(t, modelPlazaProbeSupported(PlazaModel{Platform: PlatformOpenAI, Name: "gpt-image-1"}))
	require.False(t, modelPlazaProbeSupported(PlazaModel{Platform: PlatformOpenAI, Name: "video", Pricing: &ChannelModelPricing{BillingMode: "video"}}))
}

func TestModelPlazaMonitorProbeIsOneRequestAndUsesSignedMarker(t *testing.T) {
	s, _, _ := newPlazaMonitorFixture(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.True(t, VerifyChannelMonitorProbeHeader(r.Header.Get(ChannelMonitorProbeHeaderName), "secret", time.Now()))
		require.True(t, VerifyChannelMonitorProbeHeader(r.Header.Get(ModelPlazaMonitorProbeHeaderName), "secret", time.Now()))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, float64(8), body["max_tokens"])
		messages, ok := body["messages"].([]any)
		require.True(t, ok)
		require.Len(t, messages, 1)
		message, ok := messages[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "hi", message["content"])
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello"}}]}`))
	}))
	defer server.Close()
	s.baseURL = server.URL
	result := s.probe(context.Background(), ModelPlazaMonitorTarget{Platform: PlatformOpenAI, Model: "gpt-4.1"}, "secret")
	require.Equal(t, "operational", result.Status)
	require.False(t, result.ServiceFailure)
	require.Equal(t, 1, requests)
}

func TestModelPlazaMonitorUnsupportedBudgetIsUnknownWithoutRetry(t *testing.T) {
	s, _, _ := newPlazaMonitorFixture(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens requires a larger budget"}}`))
	}))
	defer server.Close()
	s.baseURL = server.URL
	result := s.probe(context.Background(), ModelPlazaMonitorTarget{Platform: PlatformOpenAI, Model: "reasoner"}, "secret")
	require.Equal(t, "unknown", result.Status)
	require.False(t, result.ServiceFailure)
	require.Equal(t, 1, requests)
	_, serviceFailure := modelPlazaProbeFailure(429, []byte(`{"error":{"message":"insufficient balance"}}`))
	require.False(t, serviceFailure)
	_, serviceFailure = modelPlazaProbeFailure(503, nil)
	require.True(t, serviceFailure)
}

func TestModelPlazaMonitorStalenessUsesEachModelsInterval(t *testing.T) {
	s, repo, cfg := newPlazaMonitorFixture(t)
	now := time.Now().UTC()
	checked := now.Add(-100 * time.Second)
	s.now = func() time.Time { return now }
	target := modelPlazaMonitorGroupTargets(cfg.Groups[0])[0]
	repo.states = []ModelPlazaMonitorState{{ID: 1, Target: target, Status: "operational", LastCheckedAt: &checked, NextCheckAt: now.Add(time.Minute)}}
	repo.history = []ModelPlazaMonitorPoint{{Status: "operational", CheckedAt: checked}}
	status, err := s.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, status.Items, 1)
	require.Equal(t, "unknown", status.Items[0].Status)
	repo.states[0].Target.IntervalSeconds = 300
	status, err = s.GetStatus(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "operational", status.Items[0].Status)
}

func TestModelPlazaMonitorRunRespectsSavedIntervalAndLease(t *testing.T) {
	s, repo, cfg := newPlazaMonitorFixture(t)
	target := modelPlazaMonitorGroupTargets(cfg.Groups[0])[0]
	repo.states = []ModelPlazaMonitorState{{ID: 1, Target: target, Status: "unknown", NextCheckAt: time.Now().Add(-time.Minute)}}
	repo.completed = make(chan ModelPlazaMonitorCheck, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer server.Close()
	s.baseURL = server.URL
	_, err := s.Run(context.Background(), 7)
	require.NoError(t, err)
	select {
	case check := <-repo.completed:
		require.Equal(t, "operational", check.Status)
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not complete")
	}
	_, err = s.Run(context.Background(), 7)
	require.NoError(t, err)
	select {
	case <-repo.completed:
		t.Fatal("repeated manual run bypassed interval")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestModelPlazaMonitorProbeDoesNotAcceptReasoningOnlyOutput(t *testing.T) {
	require.False(t, modelPlazaProbeHasOutput([]byte(`{"output":[{"type":"reasoning","summary":[]}]}`), PlatformOpenAI, MonitorAPIModeResponses))
	require.True(t, modelPlazaProbeHasOutput([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`), PlatformOpenAI, MonitorAPIModeResponses))
	require.False(t, modelPlazaProbeHasOutput([]byte(strings.Repeat("x", 10)), PlatformOpenAI, ""))
}
