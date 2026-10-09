package service

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUpstreamBalanceIntervalJSONCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, raw        string
		seconds, minutes int
		invalid          bool
	}{
		{"legacy minutes", `{"interval_minutes":7}`, 420, 7, false},
		{"new seconds", `{"interval_seconds":15}`, 15, 5, false},
		{"seconds win", `{"interval_seconds":15,"interval_minutes":30}`, 15, 5, false},
		{"seconds ignore minute range", `{"interval_seconds":301,"interval_minutes":2}`, 301, 6, false},
		{"minimum", `{"interval_seconds":10}`, 10, 5, false},
		{"maximum", `{"interval_seconds":86400}`, 86400, 1440, false},
		{"zero seconds present", `{"interval_seconds":0,"interval_minutes":30}`, 0, 0, true},
		{"case insensitive zero", `{"INTERVAL_SECONDS":0,"interval_minutes":30}`, 0, 0, true},
		{"too short", `{"interval_seconds":9}`, 0, 0, true},
		{"too long", `{"interval_seconds":86401}`, 0, 0, true},
		{"negative", `{"interval_seconds":-1,"interval_minutes":30}`, 0, 0, true},
		{"fraction", `{"interval_seconds":15.5}`, 0, 0, true},
		{"null", `{"interval_seconds":null,"interval_minutes":30}`, 0, 0, true},
		{"legacy below minimum", `{"interval_minutes":4}`, 0, 0, true},
		{"legacy above maximum", `{"interval_minutes":1441}`, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultUpstreamBalanceConfig()
			err := json.Unmarshal([]byte(tc.raw), c)
			if err == nil {
				err = NormalizeUpstreamBalanceInterval(c)
			}
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.seconds, c.IntervalSeconds)
			require.Equal(t, tc.minutes, c.IntervalMinutes)
		})
	}
}

func TestUpstreamBalanceSaveSecondsAndLegacyMinuteInput(t *testing.T) {
	s, _, _, _, _ := balanceFixture(t, 0)
	for _, raw := range []string{`{"version":1,"interval_seconds":15,"wallets":[]}`, `{"version":2,"interval_minutes":7,"wallets":[]}`} {
		var c UpstreamBalanceConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &c))
		saved, err := s.SaveConfig(context.Background(), &c)
		require.NoError(t, err)
		if c.IntervalSeconds == 0 {
			require.Equal(t, 420, saved.IntervalSeconds)
		} else {
			require.Equal(t, 15, saved.IntervalSeconds)
		}
	}
	for _, seconds := range []int{-1, 9, 86401} {
		c, err := s.GetConfig(context.Background())
		require.NoError(t, err)
		c.IntervalSeconds = seconds
		_, err = s.SaveConfig(context.Background(), c)
		require.ErrorIs(t, err, ErrUpstreamBalanceInvalid)
	}
}

func TestUpstreamBalanceRunnerUsesExactSecondIntervalsWithoutEarlyAccountScans(t *testing.T) {
	for _, seconds := range []int{10, 15, 30} {
		t.Run((time.Duration(seconds) * time.Second).String(), func(t *testing.T) {
			s, repo, accounts, http, _ := balanceFixture(t, 1)
			repo.config.Enabled = true
			repo.config.IntervalSeconds = seconds
			now := s.now()
			s.now = func() time.Time { return now }
			wait, err := s.runDue(context.Background())
			require.NoError(t, err)
			require.Equal(t, time.Duration(seconds)*time.Second, wait)
			require.Len(t, http.requests, 1)
			require.EqualValues(t, 1, accounts.listCalls.Load())
			now = now.Add(time.Duration(seconds-1) * time.Second)
			wait, err = s.runDue(context.Background())
			require.NoError(t, err)
			require.Equal(t, time.Second, wait)
			require.Len(t, http.requests, 1)
			require.EqualValues(t, 1, accounts.listCalls.Load())
			now = now.Add(time.Second)
			wait, err = s.runDue(context.Background())
			require.NoError(t, err)
			require.Equal(t, time.Duration(seconds)*time.Second, wait)
			require.Len(t, http.requests, 2)
			require.EqualValues(t, 2, accounts.listCalls.Load())
		})
	}
}

func TestUpstreamBalanceIntervalChangeRecomputesDueAndFreshness(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 1)
	base := s.now()
	now := base
	s.now = func() time.Time { return now }
	item, err := s.Refresh(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, base.Add(30*time.Minute), *item.NextRefreshAt)
	now = base.Add(20 * time.Second)
	c, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	c.Enabled = true
	c.IntervalSeconds = 15
	_, err = s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	list, err := s.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, base.Add(15*time.Second), *list.Items[0].NextRefreshAt)
	require.Equal(t, base.Add(30*time.Second), *list.Items[0].FreshUntil)
	// Projection did not rewrite the old stored snapshot merely to display it.
	require.Equal(t, base.Add(30*time.Minute), *repo.snapshots[id].Item.NextRefreshAt)
	require.NoError(t, s.RunDue(context.Background()))
	require.Len(t, http.requests, 2)
	c, err = s.GetConfig(context.Background())
	require.NoError(t, err)
	c.IntervalSeconds = 1800
	_, err = s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	list, err = s.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, now.Add(30*time.Minute), *list.Items[0].NextRefreshAt)
	require.Equal(t, now.Add(time.Hour), *list.Items[0].FreshUntil)
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	require.Len(t, http.requests, 2)
}

func TestUpstreamBalanceDisabledAndLongIntervalsWaitOneMinute(t *testing.T) {
	s, repo, accounts, http, _ := balanceFixture(t, 1)
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	require.Zero(t, repo.snapshotReads.Load())
	require.Zero(t, accounts.listCalls.Load())
	require.Empty(t, http.requests)
	repo.config.Enabled = true
	wait, err = s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	require.Len(t, http.requests, 1)
	for range 3 {
		wait, err = s.runDue(context.Background())
		require.NoError(t, err)
		require.Equal(t, time.Minute, wait)
	}
	require.EqualValues(t, 1, accounts.listCalls.Load())
	require.Len(t, http.requests, 1)
}

func TestUpstreamBalanceUnsupportedBackoffUsesSeconds(t *testing.T) {
	s, repo, _, http, _ := balanceFixture(t, 1)
	repo.config.Enabled = true
	repo.config.IntervalSeconds = 15
	http.hook = func(*nethttp.Request) *nethttp.Response {
		return balanceResponse(`{"mode":"quota_limited","isValid":true}`)
	}
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	base := s.now()
	s.now = func() time.Time { return base.Add(59 * time.Second) }
	wait, err = s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Second, wait)
	require.Len(t, http.requests, 1)
}

func TestUpstreamBalanceRunnerRetriesOrphansWithoutDelayingHealthyWallets(t *testing.T) {
	s, repo, accounts, http, _ := balanceFixture(t, 1)
	repo.config.Enabled = true
	repo.config.IntervalSeconds = 15
	repo.config.Wallets = append(repo.config.Wallets, UpstreamBalanceWallet{ID: uuid.NewString(), SiteURL: "https://deleted.example", SingleAccount: true, Enabled: true})
	base := s.now()
	now := base
	s.now = func() time.Time { return now }
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, wait)
	require.Len(t, http.requests, 1)
	now = base.Add(14 * time.Second)
	wait, err = s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Second, wait)
	require.EqualValues(t, 1, accounts.listCalls.Load())
	now = base.Add(15 * time.Second)
	wait, err = s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, wait)
	require.Len(t, http.requests, 2)
}

func TestUpstreamBalanceSaveWakesRunnerWithoutWaitingForOldInterval(t *testing.T) {
	s, _, _, http, _ := balanceFixture(t, 1)
	entered := make(chan struct{}, 1)
	http.hook = func(*nethttp.Request) *nethttp.Response {
		entered <- struct{}{}
		return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":1,"unit":"USD"}`)
	}
	s.Start()
	s.Start()
	c, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	c.Enabled = true
	c.IntervalSeconds = 15
	_, err = s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("configuration save did not wake runner")
	}
	s.Stop()
	s.Stop()
	require.Len(t, http.requests, 1)
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	require.Len(t, http.requests, 1)
}

func TestUpstreamBalanceRunnerDoesNotReenterAndStopCancelsRequest(t *testing.T) {
	s, repo, accounts, http, _ := balanceFixture(t, 1)
	repo.config.Enabled = true
	repo.config.IntervalSeconds = 10
	entered := make(chan struct{}, 1)
	http.hook = func(req *nethttp.Request) *nethttp.Response {
		entered <- struct{}{}
		<-req.Context().Done()
		return balanceResponse(`{"mode":"unrestricted","isValid":true,"balance":1,"unit":"USD"}`)
	}
	done := make(chan struct{})
	go func() { _ = s.RunDue(context.Background()); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("runner request did not start")
	}
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, time.Minute, wait)
	require.EqualValues(t, 1, accounts.listCalls.Load())
	s.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}
	require.Len(t, http.requests, 1)
}

type balancePeerSnapshotLock struct {
	balanceLeaderLock
	hook func(string)
}

func (l *balancePeerSnapshotLock) TryAcquireLeaderLock(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
	l.hook(key)
	return true, nil
}

func TestUpstreamBalanceWalletLockRechecksPeerSnapshotForTenSecondCadence(t *testing.T) {
	s, repo, _, http, id := balanceFixture(t, 1)
	repo.config.Enabled = true
	repo.config.IntervalSeconds = 10
	s.lockCache = &balancePeerSnapshotLock{hook: func(key string) {
		if !strings.HasPrefix(key, "custom:upstream_balance:wallet:") {
			return
		}
		a := repo.accounts.values[1]
		now := s.now()
		balance := 1.0
		repo.mu.Lock()
		repo.snapshots[id] = &UpstreamBalanceSnapshot{
			WalletIdentity: upstreamBalanceWalletIdentity(repo.config.Wallets[0]), AccountIdentity: upstreamBalanceAccountIdentity(a),
			Item: UpstreamBalanceItem{WalletID: id, Status: "ok", Kind: "wallet", QueriedAccountID: &a.ID, Balance: &balance, LastAttemptAt: &now, LastSuccessAt: &now},
		}
		repo.mu.Unlock()
	}}
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, wait)
	require.Empty(t, http.requests)
}

func TestUpstreamBalancePeriodicBatchReadsAccountsOnce(t *testing.T) {
	s, repo, accounts, http, _ := balanceFixture(t, 2)
	repo.config.Enabled = true
	repo.config.IntervalSeconds = 15
	w := repo.config.Wallets[0]
	w.SingleAccount = false
	w.AccountIDs = []int64{1}
	second := w
	second.ID = uuid.NewString()
	second.AccountIDs = []int64{2}
	repo.config.Wallets = []UpstreamBalanceWallet{w, second}
	wait, err := s.runDue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, wait)
	require.Len(t, http.requests, 2)
	require.EqualValues(t, 1, accounts.listCalls.Load())
}
