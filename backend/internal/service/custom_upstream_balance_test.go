package service

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type balanceAccounts struct {
	AccountRepository // Every unimplemented write panics: probes must only read.
	values            map[int64]*Account
	listCalls         atomic.Int64
}

func (r *balanceAccounts) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]Account, error) {
	r.listCalls.Add(1)
	result := []Account{}
	for _, a := range r.values {
		result = append(result, *a)
	}
	return result, nil
}
func (r *balanceAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	a := r.values[id]
	if a == nil {
		return nil, ErrAccountNotFound
	}
	raw, _ := json.Marshal(a)
	var copy Account
	_ = json.Unmarshal(raw, &copy)
	return &copy, nil
}

type balanceMemoryRepo struct {
	mu            sync.Mutex
	config        *UpstreamBalanceConfig
	snapshots     map[string]*UpstreamBalanceSnapshot
	accounts      *balanceAccounts
	configReads   atomic.Int64
	snapshotReads atomic.Int64
}

func (r *balanceMemoryRepo) GetConfig(context.Context) (*UpstreamBalanceConfig, error) {
	r.configReads.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := json.Marshal(r.config)
	var copy UpstreamBalanceConfig
	_ = json.Unmarshal(raw, &copy)
	return &copy, nil
}
func (r *balanceMemoryRepo) SaveConfig(_ context.Context, c *UpstreamBalanceConfig) (*UpstreamBalanceConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.Version != r.config.Version {
		return nil, ErrUpstreamBalanceConflict
	}
	copy := *c
	copy.Version++
	r.config = &copy
	return &copy, nil
}
func (r *balanceMemoryRepo) GetSnapshots(context.Context) (map[string]*UpstreamBalanceSnapshot, error) {
	r.snapshotReads.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	result := map[string]*UpstreamBalanceSnapshot{}
	for id, snapshot := range r.snapshots {
		copy := *snapshot
		result[id] = &copy
	}
	return result, nil
}
func (r *balanceMemoryRepo) SaveSnapshot(_ context.Context, v int64, a *Account, s *UpstreamBalanceSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v != r.config.Version || upstreamBalanceAccountIdentity(a) != upstreamBalanceAccountIdentity(r.accounts.values[a.ID]) {
		return ErrUpstreamBalanceConflict
	}
	r.snapshots[s.Item.WalletID] = s
	return nil
}

type balanceHTTP struct {
	mu       sync.Mutex
	requests []*nethttp.Request
	hook     func(*nethttp.Request) *nethttp.Response
}

func (h *balanceHTTP) Do(req *nethttp.Request, _ string, _ int64, _ int) (*nethttp.Response, error) {
	h.mu.Lock()
	h.requests = append(h.requests, req)
	h.mu.Unlock()
	if h.hook != nil {
		return h.hook(req), nil
	}
	return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":12.5,"unit":"USD"}`), nil
}
func (h *balanceHTTP) DoWithTLS(req *nethttp.Request, p string, id int64, c int, _ *tlsfingerprint.Profile) (*nethttp.Response, error) {
	return h.Do(req, p, id, c)
}
func balanceResponse(body string) *nethttp.Response {
	return &nethttp.Response{StatusCode: 200, Header: nethttp.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

type balanceLeaderLock struct{}

func (*balanceLeaderLock) TryAcquireLeaderLock(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (*balanceLeaderLock) ReleaseLeaderLock(context.Context, string, string) error { return nil }

func balanceFixture(t *testing.T, count int) (*UpstreamBalanceService, *balanceMemoryRepo, *balanceAccounts, *balanceHTTP, string) {
	t.Helper()
	accounts := &balanceAccounts{values: map[int64]*Account{}}
	for i := 1; i <= count; i++ {
		accounts.values[int64(i)] = &Account{ID: int64(i), Name: "key", Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Credentials: map[string]any{"base_url": "https://relay.example/v1", "api_key": "secret"}}
	}
	id := uuid.NewString()
	repo := &balanceMemoryRepo{config: &UpstreamBalanceConfig{Version: 1, IntervalMinutes: 30, Wallets: []UpstreamBalanceWallet{{ID: id, Name: "relay", SiteURL: "https://relay.example", SingleAccount: true, AccountIDs: []int64{}, Threshold: 20, Enabled: true}}}, snapshots: map[string]*UpstreamBalanceSnapshot{}, accounts: accounts}
	http := &balanceHTTP{}
	tester := &AccountTestService{cfg: &config.Config{}, httpUpstream: http}
	s := NewUpstreamBalanceService(repo, accounts, tester)
	s.lockCache = &balanceLeaderLock{}
	s.now = func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(s.Stop)
	return s, repo, accounts, http, id
}

func TestUpstreamBalanceNormalizeAndDiscovery(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://EXAMPLE.com.:443/v1/", "https://example.com"},
		{"https://example.com/team/v1", "https://example.com/team"},
		{"https://sub.example.com:8443/v1", "https://sub.example.com:8443"},
		{"https://example.com/other", "https://example.com/other"},
	} {
		got, err := NormalizeUpstreamBalanceSite(tc.raw)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, raw := range []string{"https://secret@example.com", "https://example.com?key=secret", "https://example.com#secret", "file:///tmp/x", "https://example.com/a/../b"} {
		_, err := NormalizeUpstreamBalanceSite(raw)
		require.Error(t, err)
	}
	s, _, accounts, _, _ := balanceFixture(t, 2)
	accounts.values[3] = &Account{ID: 3, Type: AccountTypeOAuth, Credentials: map[string]any{"base_url": "https://relay.example", "api_key": "oauth-secret"}}
	discovery, err := s.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, discovery.Sites, 1)
	require.Len(t, discovery.Sites[0].Accounts, 2)
	raw, err := json.Marshal(discovery)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret")
}

func TestUpstreamBalanceClassifiesExplicitWalletOnly(t *testing.T) {
	for _, tc := range []struct{ body, status, kind string }{
		{`{"mode":"unrestricted","isValid":true,"balance":-2,"unit":"USD"}`, "ok", "wallet"},
		{`{"mode":"quota_limited","isValid":true,"remaining":200,"balance":200,"unit":"USD"}`, "unsupported", "key_quota"},
		{`{"mode":"unrestricted","isValid":true,"subscription":{},"balance":200,"unit":"USD"}`, "unsupported", "subscription"},
		{`{"mode":"unrestricted","isValid":true,"planName":"monthly","remaining":200,"unit":"USD"}`, "unsupported", "subscription"},
		{`{"mode":"unrestricted","isValid":true,"remaining":200,"unit":"USD"}`, "failed", "unknown"},
		{`{"mode":"unrestricted","isValid":false,"balance":200,"unit":"USD"}`, "failed", "unknown"},
		{`{"mode":"unrestricted","isValid":true,"balance":200,"unit":"unexpected"}`, "failed", "unknown"},
	} {
		item := parseUpstreamBalanceUsage([]byte(tc.body))
		require.Equal(t, tc.status, item.Status)
		require.Equal(t, tc.kind, item.Kind)
		if tc.kind != "wallet" {
			require.Nil(t, item.Balance)
		}
	}
}

func TestUpstreamBalanceRefreshIsReadOnlyAndDefaultOff(t *testing.T) {
	s, repo, accounts, http, id := balanceFixture(t, 2)
	before, _ := json.Marshal(accounts.values)
	require.NoError(t, s.RunDue(context.Background()))
	require.Empty(t, http.requests)
	item, err := s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "wallet", item.Kind)
	require.True(t, item.LowBalance)
	require.Equal(t, 2, item.AccountCount)
	require.Len(t, http.requests, 1)
	require.Equal(t, "https://relay.example/v1/usage", http.requests[0].URL.String())
	require.Equal(t, "Bearer secret", http.requests[0].Header.Get("Authorization"))
	require.True(t, HTTPUpstreamRedirectsDisabled(http.requests[0].Context()))
	_, err = s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, http.requests, 1)
	after, _ := json.Marshal(accounts.values)
	require.JSONEq(t, string(before), string(after))
	require.Len(t, repo.snapshots, 1)
	accounts.values[3] = &Account{ID: 3, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://relay.example", "api_key": "new-secret"}}
	list, err := s.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, list.Items[0].AccountCount)
	accounts.values[1].Credentials["api_key"] = "rotated"
	list, err = s.List(context.Background())
	require.NoError(t, err)
	require.Nil(t, list.Items[0].Balance)
	require.Equal(t, "unconfigured", list.Items[0].Status)
}

func TestUpstreamBalanceSaveRejectsConflicts(t *testing.T) {
	s, repo, _, _, _ := balanceFixture(t, 2)
	c, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	c.Wallets = append(c.Wallets, UpstreamBalanceWallet{ID: uuid.NewString(), Name: "second", SiteURL: c.Wallets[0].SiteURL, AccountIDs: []int64{2}})
	_, err = s.SaveConfig(context.Background(), c)
	require.ErrorIs(t, err, ErrUpstreamBalanceInvalid)
	c.Wallets[0].SingleAccount = false
	c.Wallets[0].AccountIDs = []int64{1}
	query := int64(2)
	c.Wallets[0].QueryAccountID = &query
	_, err = s.SaveConfig(context.Background(), c)
	require.ErrorIs(t, err, ErrUpstreamBalanceInvalid)
	c.Wallets[0].QueryAccountID = nil
	saved, err := s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, int64(2), saved.Version)
	_, err = s.SaveConfig(context.Background(), c)
	require.ErrorIs(t, err, ErrUpstreamBalanceConflict)
	require.Equal(t, int64(2), repo.config.Version)
}

func TestUpstreamBalanceRotatesLimitedKeysAndRetainsFailure(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 4)
	http.hook = func(req *nethttp.Request) *nethttp.Response {
		if len(http.requests) < 4 {
			return balanceResponse(`{"mode":"quota_limited","isValid":true,"remaining":200,"unit":"USD"}`)
		}
		return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":42,"unit":"USD"}`)
	}
	item, err := s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "key_quota", item.Kind)
	require.Nil(t, item.Balance)
	require.Len(t, http.requests, 3)
	s.now = func() time.Time { return time.Date(2026, 10, 8, 0, 1, 0, 0, time.UTC) }
	item, err = s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "wallet", item.Kind)
	require.Equal(t, int64(4), *item.QueriedAccountID)
	s.now = func() time.Time { return time.Date(2026, 10, 8, 0, 2, 0, 0, time.UTC) }
	http.hook = func(req *nethttp.Request) *nethttp.Response {
		if len(http.requests) == 5 {
			resp := balanceResponse("unavailable")
			resp.StatusCode = 503
			return resp
		}
		return balanceResponse(`{"mode":"quota_limited","isValid":true,"remaining":200,"unit":"USD"}`)
	}
	item, err = s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "failed", item.Status)
	require.Equal(t, 42.0, *item.Balance)
	require.False(t, item.LowBalance)
	require.Equal(t, int64(4), *repo.snapshots[id].Item.QueriedAccountID)
}

func TestUpstreamBalanceDiscardsChangedConfigAndCredentials(t *testing.T) {
	for _, change := range []string{"config", "credentials"} {
		t.Run(change, func(t *testing.T) {
			s, repo, accounts, http, id := balanceFixture(t, 1)
			http.hook = func(*nethttp.Request) *nethttp.Response {
				if change == "config" {
					repo.config.Version++
				} else {
					accounts.values[1].Credentials["api_key"] = "new"
				}
				return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":99,"unit":"USD"}`)
			}
			_, err := s.Refresh(context.Background(), id)
			require.ErrorIs(t, err, ErrUpstreamBalanceConflict)
			require.Empty(t, repo.snapshots)
		})
	}
}

func TestUpstreamBalanceRunnerSkipsOrphans(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 1)
	repo.config.Enabled = true
	valid := repo.config.Wallets[0]
	repo.config.Wallets = nil
	for range 12 {
		repo.config.Wallets = append(repo.config.Wallets, UpstreamBalanceWallet{ID: uuid.NewString(), Name: "deleted", SiteURL: "https://deleted.example", Enabled: true, SingleAccount: true})
	}
	repo.config.Wallets = append(repo.config.Wallets, valid)
	require.NoError(t, s.RunDue(context.Background()))
	require.Len(t, http.requests, 1)
	require.NotNil(t, repo.snapshots[id])
}

func TestUpstreamBalanceTransportFailuresAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"redirect", "secret", 302, "http_error"}, {"oversize", strings.Repeat("s", 256*1024+1), 200, "response_too_large"}, {"malformed", `{"secret":"sk-sensitive"}`, 200, "invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, http, id := balanceFixture(t, 1)
			http.hook = func(*nethttp.Request) *nethttp.Response {
				r := balanceResponse(tc.body)
				r.StatusCode = tc.status
				return r
			}
			item, err := s.Refresh(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, tc.want, item.ErrorCode)
			require.Nil(t, item.Balance)
			raw, _ := json.Marshal(item)
			require.NotContains(t, string(raw), "sk-sensitive")
		})
	}
}

func TestUpstreamBalanceManualSelectionDoesNotFallback(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 3)
	selected := int64(2)
	repo.config.Wallets[0].QueryAccountID = &selected
	http.hook = func(*nethttp.Request) *nethttp.Response {
		return balanceResponse(`{"mode":"quota_limited","isValid":true}`)
	}
	item, err := s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, http.requests, 1)
	require.True(t, reflect.DeepEqual(&selected, item.QueriedAccountID))
	require.Equal(t, "key_quota", item.Kind)
}

func TestUpstreamBalanceCanDisableAfterAccountDeleted(t *testing.T) {
	s, repo, accounts, _, _ := balanceFixture(t, 1)
	repo.config.Enabled = true
	repo.config.Wallets[0].SingleAccount = false
	repo.config.Wallets[0].AccountIDs = []int64{1}
	selected := int64(1)
	repo.config.Wallets[0].QueryAccountID = &selected
	delete(accounts.values, 1)
	c, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	c.Enabled = false
	saved, err := s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	require.False(t, saved.Enabled)
	list, err := s.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, "query_account_unavailable", list.Items[0].ErrorCode)
	saved, err = s.GetConfig(context.Background())
	require.NoError(t, err)
	saved.Wallets[0].AccountIDs = append(saved.Wallets[0].AccountIDs, 999)
	_, err = s.SaveConfig(context.Background(), saved)
	require.ErrorIs(t, err, ErrUpstreamBalanceInvalid)
}

func TestUpstreamBalanceDoesNotProbeWhenLeaderCacheUnavailable(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 1)
	s.lockCache = nil
	repo.config.Enabled = true
	require.NoError(t, s.RunDue(context.Background()))
	_, err := s.Refresh(context.Background(), id)
	require.ErrorIs(t, err, ErrUpstreamBalanceBusy)
	require.Empty(t, http.requests)
	require.Empty(t, repo.snapshots)
}

func TestUpstreamBalanceRefreshConcurrencyIsIndependentAndBounded(t *testing.T) {
	s, repo, _, http, _ := balanceFixture(t, 3)
	repo.config.Wallets = nil
	for id := int64(1); id <= 3; id++ {
		repo.config.Wallets = append(repo.config.Wallets, UpstreamBalanceWallet{ID: uuid.NewString(), Name: "wallet", SiteURL: "https://relay.example", AccountIDs: []int64{id}})
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)
	http.hook = func(req *nethttp.Request) *nethttp.Response {
		entered <- struct{}{}
		select {
		case <-release:
		case <-req.Context().Done():
		}
		return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":1,"unit":"USD"}`)
	}
	for i := range 2 {
		go func() { _, _ = s.Refresh(context.Background(), repo.config.Wallets[i].ID) }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("refresh did not start")
		}
	}
	_, err := s.Refresh(context.Background(), repo.config.Wallets[2].ID)
	require.ErrorIs(t, err, ErrUpstreamBalanceBusy)
	require.Len(t, http.requests, 2)
}
