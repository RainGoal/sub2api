package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var (
	ErrUpstreamBalanceInvalid     = infraerrors.BadRequest("UPSTREAM_BALANCE_INVALID", "invalid upstream balance configuration")
	ErrUpstreamBalanceConflict    = infraerrors.Conflict("UPSTREAM_BALANCE_CONFLICT", "upstream balance configuration or credentials changed; reload and retry")
	ErrUpstreamBalanceNotFound    = infraerrors.NotFound("UPSTREAM_BALANCE_NOT_FOUND", "upstream wallet not found")
	ErrUpstreamBalanceUnavailable = infraerrors.ServiceUnavailable("UPSTREAM_BALANCE_UNAVAILABLE", "upstream balance service unavailable")
	ErrUpstreamBalanceBusy        = infraerrors.Conflict("UPSTREAM_BALANCE_BUSY", "upstream balance refresh already running")
)

const upstreamBalanceMaxWallets = 100

type UpstreamBalanceWallet struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	SiteURL        string  `json:"site_url"`
	SingleAccount  bool    `json:"single_account"`
	AccountIDs     []int64 `json:"account_ids"`
	QueryAccountID *int64  `json:"query_account_id"`
	Threshold      float64 `json:"threshold"`
	RechargeURL    string  `json:"recharge_url"`
	Enabled        bool    `json:"enabled"`
}

type UpstreamBalanceConfig struct {
	Version         int64                   `json:"version"`
	Enabled         bool                    `json:"enabled"`
	IntervalMinutes int                     `json:"interval_minutes"`
	Wallets         []UpstreamBalanceWallet `json:"wallets"`
}

type UpstreamBalanceItem struct {
	WalletID         string     `json:"wallet_id"`
	Status           string     `json:"status"`
	Kind             string     `json:"kind"`
	Balance          *float64   `json:"balance"`
	Currency         string     `json:"currency"`
	QueriedAccountID *int64     `json:"queried_account_id"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	LastAttemptAt    *time.Time `json:"last_attempt_at"`
	NextRefreshAt    *time.Time `json:"next_refresh_at"`
	FreshUntil       *time.Time `json:"fresh_until"`
	ErrorCode        string     `json:"error_code"`
	LowBalance       bool       `json:"low_balance"`
	AccountCount     int        `json:"account_count"`
}

// Snapshot fingerprints are persisted privately, never included in admin DTOs.
type UpstreamBalanceSnapshot struct {
	Item            UpstreamBalanceItem `json:"item"`
	WalletIdentity  string              `json:"wallet_identity"`
	AccountIdentity string              `json:"account_identity"`
	NextCandidateID int64               `json:"next_candidate_id,omitempty"`
}

type UpstreamBalanceList struct {
	Config *UpstreamBalanceConfig `json:"config"`
	Items  []UpstreamBalanceItem  `json:"items"`
}
type UpstreamBalanceAccount struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type UpstreamBalanceSite struct {
	SiteURL  string                   `json:"site_url"`
	Accounts []UpstreamBalanceAccount `json:"accounts"`
}
type UpstreamBalanceDiscovery struct {
	Sites []UpstreamBalanceSite `json:"sites"`
}

type UpstreamBalanceRepository interface {
	GetConfig(context.Context) (*UpstreamBalanceConfig, error)
	SaveConfig(context.Context, *UpstreamBalanceConfig) (*UpstreamBalanceConfig, error)
	GetSnapshots(context.Context) (map[string]*UpstreamBalanceSnapshot, error)
	SaveSnapshot(context.Context, int64, *Account, *UpstreamBalanceSnapshot) error
}

// UpstreamBalanceHTTPUpstream gives Wire a distinct, private connection pool.
type UpstreamBalanceHTTPUpstream struct{ HTTPUpstream }

type UpstreamBalanceService struct {
	repo      UpstreamBalanceRepository
	accounts  AccountRepository
	tester    *AccountTestService
	upstream  HTTPUpstream
	lockCache LeaderLockCache
	owner     string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	started   bool
	stopped   bool
	active    map[string]bool
	slots     chan struct{}
	wg        sync.WaitGroup
	cycleMu   sync.Mutex
	now       func() time.Time
}

func NewUpstreamBalanceService(repo UpstreamBalanceRepository, accounts AccountRepository, tester *AccountTestService) *UpstreamBalanceService {
	ctx, cancel := context.WithCancel(context.Background())
	s := &UpstreamBalanceService{repo: repo, accounts: accounts, tester: tester, owner: uuid.NewString(), ctx: ctx, cancel: cancel, active: map[string]bool{}, slots: make(chan struct{}, 2), now: time.Now}
	if tester != nil {
		s.upstream = tester.httpUpstream
	}
	return s
}

func ProvideUpstreamBalanceService(repo UpstreamBalanceRepository, accounts AccountRepository, tester *AccountTestService, lockCache LeaderLockCache, upstream UpstreamBalanceHTTPUpstream) *UpstreamBalanceService {
	s := NewUpstreamBalanceService(repo, accounts, tester)
	s.lockCache, s.upstream = lockCache, upstream.HTTPUpstream
	s.Start()
	return s
}

func DefaultUpstreamBalanceConfig() *UpstreamBalanceConfig {
	return &UpstreamBalanceConfig{IntervalMinutes: 30, Wallets: []UpstreamBalanceWallet{}}
}

func (s *UpstreamBalanceService) GetConfig(ctx context.Context) (*UpstreamBalanceConfig, error) {
	if s == nil || s.repo == nil {
		return nil, ErrUpstreamBalanceUnavailable
	}
	return s.repo.GetConfig(ctx)
}

// NormalizeUpstreamBalanceSite preserves deployments and subdomains while removing
// only the conventional API version suffix. No network request is made here.
func NormalizeUpstreamBalanceSite(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "\\") || (u.Scheme != "https" && u.Scheme != "http") || len(raw) > 2048 {
		return "", ErrUpstreamBalanceInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", ErrUpstreamBalanceInvalid
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.Path = strings.TrimSuffix(u.Path, "/v1")
	u.RawPath = ""
	return u.String(), nil
}

func upstreamBalanceAccountSite(a *Account) string {
	if a == nil || a.Type != AccountTypeAPIKey || strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return ""
	}
	site, err := NormalizeUpstreamBalanceSite(a.GetCredential("base_url"))
	if err != nil {
		return ""
	}
	return site
}

func (s *UpstreamBalanceService) accountIndex(ctx context.Context) (map[int64]*Account, error) {
	if s.accounts == nil {
		return nil, ErrUpstreamBalanceUnavailable
	}
	accounts, err := s.accounts.ListAllWithFilters(ctx, "", AccountTypeAPIKey, "", "", 0, "")
	if err != nil {
		return nil, err
	}
	index := make(map[int64]*Account, len(accounts))
	for i := range accounts {
		if upstreamBalanceAccountSite(&accounts[i]) != "" {
			index[accounts[i].ID] = &accounts[i]
		}
	}
	return index, nil
}

func (s *UpstreamBalanceService) Discover(ctx context.Context) (*UpstreamBalanceDiscovery, error) {
	index, err := s.accountIndex(ctx)
	if err != nil {
		return nil, err
	}
	groups := map[string][]UpstreamBalanceAccount{}
	for _, a := range index {
		site := upstreamBalanceAccountSite(a)
		groups[site] = append(groups[site], UpstreamBalanceAccount{ID: a.ID, Name: a.Name})
	}
	result := &UpstreamBalanceDiscovery{Sites: []UpstreamBalanceSite{}}
	for site, accounts := range groups {
		sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
		result.Sites = append(result.Sites, UpstreamBalanceSite{SiteURL: site, Accounts: accounts})
	}
	sort.Slice(result.Sites, func(i, j int) bool { return result.Sites[i].SiteURL < result.Sites[j].SiteURL })
	return result, nil
}

func (s *UpstreamBalanceService) SaveConfig(ctx context.Context, input *UpstreamBalanceConfig) (*UpstreamBalanceConfig, error) {
	if input == nil || input.Version < 0 || input.IntervalMinutes < 5 || input.IntervalMinutes > 1440 || len(input.Wallets) > upstreamBalanceMaxWallets {
		return nil, ErrUpstreamBalanceInvalid
	}
	previous, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if previous.Version != input.Version {
		return nil, ErrUpstreamBalanceConflict
	}
	previousWallets := make(map[string]UpstreamBalanceWallet, len(previous.Wallets))
	for _, w := range previous.Wallets {
		previousWallets[w.ID] = w
	}
	index, err := s.accountIndex(ctx)
	if err != nil {
		return nil, err
	}
	config := *input
	config.Wallets = append([]UpstreamBalanceWallet{}, input.Wallets...)
	seenIDs, assigned, singles, sites := map[string]bool{}, map[int64]bool{}, map[string]bool{}, map[string]bool{}
	for i := range config.Wallets {
		w := &config.Wallets[i]
		w.Name = strings.TrimSpace(w.Name)
		if w.Name == "" || utf8.RuneCountInString(w.Name) > 100 || math.IsNaN(w.Threshold) || math.IsInf(w.Threshold, 0) || w.Threshold < 0 || len(w.AccountIDs) > 1000 {
			return nil, ErrUpstreamBalanceInvalid
		}
		if w.ID == "" {
			w.ID = uuid.NewString()
		}
		if _, err := uuid.Parse(w.ID); err != nil || seenIDs[w.ID] {
			return nil, ErrUpstreamBalanceInvalid
		}
		seenIDs[w.ID] = true
		w.SiteURL, err = NormalizeUpstreamBalanceSite(w.SiteURL)
		if err != nil {
			return nil, err
		}
		old, hadOld := previousWallets[w.ID]
		sameMembership := hadOld && old.SiteURL == w.SiteURL && old.SingleAccount == w.SingleAccount
		if singles[w.SiteURL] || (w.SingleAccount && sites[w.SiteURL]) {
			return nil, ErrUpstreamBalanceInvalid
		}
		sites[w.SiteURL] = true
		if w.SingleAccount {
			singles[w.SiteURL] = true
			w.AccountIDs = []int64{}
		} else {
			w.AccountIDs = append([]int64{}, w.AccountIDs...)
			sort.Slice(w.AccountIDs, func(i, j int) bool { return w.AccountIDs[i] < w.AccountIDs[j] })
			for _, id := range w.AccountIDs {
				retainedMissing := sameMembership && slices.Contains(old.AccountIDs, id)
				if assigned[id] || (upstreamBalanceAccountSite(index[id]) != w.SiteURL && !retainedMissing) {
					return nil, ErrUpstreamBalanceInvalid
				}
				assigned[id] = true
			}
		}
		if w.QueryAccountID != nil {
			found := false
			for _, a := range upstreamBalanceWalletAccounts(*w, index) {
				if a.ID == *w.QueryAccountID {
					found = true
				}
			}
			retainedMissing := sameMembership && old.QueryAccountID != nil && *old.QueryAccountID == *w.QueryAccountID && (w.SingleAccount || slices.Contains(w.AccountIDs, *w.QueryAccountID))
			if !found && !retainedMissing {
				return nil, ErrUpstreamBalanceInvalid
			}
		}
		w.RechargeURL = strings.TrimSpace(w.RechargeURL)
		if w.RechargeURL != "" {
			u, parseErr := url.Parse(w.RechargeURL)
			if parseErr != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(w.RechargeURL) > 2048 {
				return nil, ErrUpstreamBalanceInvalid
			}
		}
	}
	return s.repo.SaveConfig(ctx, &config)
}

func upstreamBalanceWalletAccounts(w UpstreamBalanceWallet, index map[int64]*Account) []*Account {
	result := []*Account{}
	if w.SingleAccount {
		for _, a := range index {
			if upstreamBalanceAccountSite(a) == w.SiteURL {
				result = append(result, a)
			}
		}
	} else {
		for _, id := range w.AccountIDs {
			if a := index[id]; a != nil && upstreamBalanceAccountSite(a) == w.SiteURL {
				result = append(result, a)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func upstreamBalanceHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func upstreamBalanceWalletIdentity(w UpstreamBalanceWallet) string {
	return upstreamBalanceHash([]any{w.ID, w.SiteURL, w.SingleAccount, w.AccountIDs, w.QueryAccountID})
}

func upstreamBalanceAccountIdentity(a *Account) string {
	if a == nil {
		return ""
	}
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	return upstreamBalanceHash([]any{a.ID, a.Platform, a.Type, a.Credentials, a.ProxyID, proxy})
}

func upstreamBalanceItem(w UpstreamBalanceWallet, index map[int64]*Account, snapshot *UpstreamBalanceSnapshot, now time.Time) UpstreamBalanceItem {
	accounts := upstreamBalanceWalletAccounts(w, index)
	item := UpstreamBalanceItem{WalletID: w.ID, Status: "unconfigured", Kind: "unknown", AccountCount: len(accounts)}
	if w.QueryAccountID != nil {
		found := false
		for _, a := range accounts {
			if a.ID == *w.QueryAccountID {
				found = true
				break
			}
		}
		if !found {
			item.ErrorCode = "query_account_unavailable"
			return item
		}
	}
	if snapshot != nil && snapshot.WalletIdentity == upstreamBalanceWalletIdentity(w) && snapshot.Item.QueriedAccountID != nil {
		for _, a := range accounts {
			if a.ID == *snapshot.Item.QueriedAccountID && snapshot.AccountIdentity == upstreamBalanceAccountIdentity(a) {
				item = snapshot.Item
				break
			}
		}
	}
	item.AccountCount = len(accounts)
	item.LowBalance = item.Status == "ok" && item.Kind == "wallet" && item.Balance != nil && item.FreshUntil != nil && now.Before(*item.FreshUntil) && *item.Balance < w.Threshold
	return item
}

func (s *UpstreamBalanceService) List(ctx context.Context) (*UpstreamBalanceList, error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := &UpstreamBalanceList{Config: config, Items: []UpstreamBalanceItem{}}
	if len(config.Wallets) == 0 {
		return result, nil
	}
	index, err := s.accountIndex(ctx)
	if err != nil {
		return nil, err
	}
	snapshots, err := s.repo.GetSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range config.Wallets {
		result.Items = append(result.Items, upstreamBalanceItem(w, index, snapshots[w.ID], s.now()))
	}
	return result, nil
}
