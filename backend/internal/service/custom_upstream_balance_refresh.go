package service

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

func (s *UpstreamBalanceService) Refresh(ctx context.Context, id string) (*UpstreamBalanceItem, error) {
	return s.refresh(ctx, id, false)
}

func (s *UpstreamBalanceService) refresh(ctx context.Context, id string, scheduled bool) (*UpstreamBalanceItem, error) {
	return s.refreshWithState(ctx, id, scheduled, nil)
}

type upstreamBalanceRefreshState struct {
	config *UpstreamBalanceConfig
	index  map[int64]*Account
}

// A periodic batch shares one read-only account index. Each request still loads
// its own current account and checks the config revision before using a key.
func (s *UpstreamBalanceService) refreshWithState(ctx context.Context, id string, scheduled bool, state *upstreamBalanceRefreshState) (*UpstreamBalanceItem, error) {
	if s == nil || s.repo == nil || s.tester == nil || s.upstream == nil {
		return nil, ErrUpstreamBalanceUnavailable
	}
	s.mu.Lock()
	if s.stopped || s.active[id] {
		s.mu.Unlock()
		return nil, ErrUpstreamBalanceBusy
	}
	s.active[id] = true
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return nil, ErrUpstreamBalanceBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	var err error
	var config *UpstreamBalanceConfig
	if state != nil {
		config = state.config
	} else {
		config, err = s.GetConfig(ctx)
		if err != nil {
			return nil, err
		}
	}
	var wallet *UpstreamBalanceWallet
	for i := range config.Wallets {
		if config.Wallets[i].ID == id {
			wallet = &config.Wallets[i]
			break
		}
	}
	if wallet == nil {
		return nil, ErrUpstreamBalanceNotFound
	}
	if scheduled && (!config.Enabled || !wallet.Enabled) {
		return nil, nil
	}
	release, acquired := s.acquireLock(ctx, "custom:upstream_balance:wallet:"+wallet.ID, 45*time.Second)
	if !acquired {
		return nil, ErrUpstreamBalanceBusy
	}
	defer release()
	var index map[int64]*Account
	if state != nil {
		index = state.index
	} else {
		index, err = s.accountIndex(ctx)
		if err != nil {
			return nil, err
		}
	}
	// Read after acquiring the wallet lock: a manual refresh or another instance
	// may have just saved a result while this periodic batch was waiting.
	snapshots, err := s.repo.GetSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	previous := upstreamBalanceItem(*wallet, index, snapshots[id], now, upstreamBalanceInterval(config))
	// Both manual and periodic calls share the persisted minimum cadence.
	if previous.LastAttemptAt != nil && now.Sub(*previous.LastAttemptAt) < 10*time.Second {
		return &previous, nil
	}
	if scheduled && previous.NextRefreshAt != nil && now.Before(*previous.NextRefreshAt) {
		return &previous, nil
	}
	candidates := upstreamBalanceWalletAccounts(*wallet, index)
	if wallet.QueryAccountID != nil {
		filtered := []*Account{}
		for _, a := range candidates {
			if a.ID == *wallet.QueryAccountID {
				filtered = append(filtered, a)
			}
		}
		candidates = filtered
	} else if previous.Kind == "wallet" && previous.QueriedAccountID != nil {
		preferred := *previous.QueriedAccountID
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ID == preferred && candidates[j].ID != preferred })
	} else if snapshot := snapshots[id]; snapshot != nil && snapshot.WalletIdentity == upstreamBalanceWalletIdentity(*wallet) && snapshot.NextCandidateID != 0 {
		cursor := snapshot.NextCandidateID
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ID > cursor && candidates[j].ID <= cursor })
	}
	if len(candidates) == 0 {
		return &previous, nil
	}
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}
	item := UpstreamBalanceItem{WalletID: id, Status: "failed", Kind: "unknown"}
	var queried *Account
	var preferredFailure *Account
	var preferredFailureItem UpstreamBalanceItem
	var lastCandidateID int64
	for _, a := range candidates {
		if ctx.Err() != nil {
			break
		}
		latest, loadErr := s.GetConfig(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		if latest.Version != config.Version {
			return nil, ErrUpstreamBalanceConflict
		}
		// Re-read credentials immediately before requesting, without touching the account.
		current, loadErr := s.accounts.GetByID(ctx, a.ID)
		if loadErr != nil || upstreamBalanceAccountSite(current) != wallet.SiteURL {
			continue
		}
		candidate := s.query(ctx, current, wallet.SiteURL)
		lastCandidateID = current.ID
		candidate.WalletID = id
		candidate.QueriedAccountID = &current.ID
		if previous.Kind == "wallet" && previous.QueriedAccountID != nil && current.ID == *previous.QueriedAccountID && candidate.Status == "failed" {
			preferredFailure, preferredFailureItem = current, candidate
		}
		// Prefer a classified result over an opaque failure when no wallet key exists.
		if queried == nil || candidate.Kind == "wallet" || (item.Kind == "unknown" && candidate.Kind != "unknown") {
			item, queried = candidate, current
		}
		if candidate.Status == "ok" && candidate.Kind == "wallet" {
			break
		}
	}
	if queried == nil {
		return &previous, nil
	}
	if preferredFailure != nil && (item.Status != "ok" || item.Kind != "wallet") {
		queried, item = preferredFailure, preferredFailureItem
	}
	completed := s.now()
	item.LastAttemptAt = &completed
	if item.Status == "ok" {
		item.LastSuccessAt = &completed
	} else if item.Status == "failed" && previous.Status != "unconfigured" && previous.QueriedAccountID != nil && *previous.QueriedAccountID == queried.ID && snapshots[id] != nil && snapshots[id].AccountIdentity == upstreamBalanceAccountIdentity(queried) {
		item.Balance, item.Kind, item.Currency = previous.Balance, previous.Kind, previous.Currency
		item.LastSuccessAt, item.FreshUntil = previous.LastSuccessAt, previous.FreshUntil
	}
	applyUpstreamBalanceTiming(&item, upstreamBalanceInterval(config))
	item.AccountCount = len(upstreamBalanceWalletAccounts(*wallet, index))
	item.LowBalance = item.Status == "ok" && item.Kind == "wallet" && item.Balance != nil && *item.Balance < wallet.Threshold
	snapshot := &UpstreamBalanceSnapshot{Item: item, WalletIdentity: upstreamBalanceWalletIdentity(*wallet), AccountIdentity: upstreamBalanceAccountIdentity(queried), NextCandidateID: lastCandidateID}
	// Persist with a short independent deadline so timeout results can be recorded.
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancelPersist()
	if err := s.repo.SaveSnapshot(persistCtx, config.Version, queried, snapshot); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *UpstreamBalanceService) query(ctx context.Context, account *Account, site string) UpstreamBalanceItem {
	fail := func(code string) UpstreamBalanceItem {
		return UpstreamBalanceItem{Status: "failed", Kind: "unknown", ErrorCode: code}
	}
	base, err := s.tester.validateUpstreamBaseURL(site)
	if err != nil {
		return fail("invalid_base_url")
	}
	if upstreamBillingProbeTargetIsOfficialAPI(base) {
		return UpstreamBalanceItem{Status: "unsupported", Kind: "unknown", ErrorCode: "unsupported"}
	}
	proxyURL := ""
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return fail("proxy_unavailable")
		}
		proxyURL = account.Proxy.URL()
	}
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(queryCtx), http.MethodGet, base+"/v1/usage", nil)
	if err != nil {
		return fail("invalid_base_url")
	}
	account.ApplyHeaderOverrides(req.Header)
	// Authentication comes from the selected account, never arbitrary overrides.
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	deleteHeaderAllForms(req.Header, "Accept")
	req.Header.Set("Accept", "application/json")
	var profile *tlsfingerprint.Profile
	if s.tester.tlsFPProfileService != nil {
		profile = s.tester.tlsFPProfileService.ResolveTLSProfile(account)
	}
	resp, err := s.upstream.DoWithTLS(req, proxyURL, account.ID, 2, profile)
	if err != nil {
		return fail("request_failed")
	}
	if resp == nil || resp.Body == nil {
		return fail("empty_response")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return UpstreamBalanceItem{Status: "unsupported", Kind: "unknown", ErrorCode: "unsupported"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail("http_error")
	}
	const maxBytes = 256 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fail("response_read_failed")
	}
	if len(body) > maxBytes {
		return fail("response_too_large")
	}
	return parseUpstreamBalanceUsage(body)
}

func parseUpstreamBalanceUsage(body []byte) UpstreamBalanceItem {
	invalid := UpstreamBalanceItem{Status: "failed", Kind: "unknown", ErrorCode: "invalid_response"}
	var data struct {
		Mode         string          `json:"mode"`
		IsValid      *bool           `json:"isValid"`
		Balance      *float64        `json:"balance"`
		Unit         string          `json:"unit"`
		Subscription json.RawMessage `json:"subscription"`
		PlanName     string          `json:"planName"`
	}
	if json.Unmarshal(body, &data) != nil || data.IsValid == nil || !*data.IsValid {
		return invalid
	}
	if data.Mode == "quota_limited" {
		return UpstreamBalanceItem{Status: "unsupported", Kind: "key_quota", ErrorCode: "key_quota_only"}
	}
	if data.Mode != "unrestricted" && data.Mode != "" {
		return invalid
	}
	if len(data.Subscription) > 0 && string(data.Subscription) != "null" {
		return UpstreamBalanceItem{Status: "unsupported", Kind: "subscription", ErrorCode: "subscription_only"}
	}
	if data.Balance == nil {
		if data.Mode == "unrestricted" && data.PlanName != "" {
			return UpstreamBalanceItem{Status: "unsupported", Kind: "subscription", ErrorCode: "subscription_only"}
		}
		return invalid
	}
	if math.IsNaN(*data.Balance) || math.IsInf(*data.Balance, 0) || data.Unit != "USD" {
		return invalid
	}
	return UpstreamBalanceItem{Status: "ok", Kind: "wallet", Balance: data.Balance, Currency: data.Unit}
}
