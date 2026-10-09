package service

import (
	"context"
	"sort"
	"sync"
	"time"
)

func (s *UpstreamBalanceService) Start() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-timer.C:
			case <-s.wake:
			}
			wait, err := s.runDue(s.ctx)
			if err != nil {
				wait = time.Minute
			}
			timer.Reset(wait)
		}
	}()
}

func (s *UpstreamBalanceService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *UpstreamBalanceService) RunDue(ctx context.Context) error {
	_, err := s.runDue(ctx)
	return err
}

func (s *UpstreamBalanceService) runDue(ctx context.Context) (time.Duration, error) {
	if s == nil || s.repo == nil {
		return time.Minute, nil
	}
	if !s.cycleMu.TryLock() {
		return time.Minute, nil
	}
	defer s.cycleMu.Unlock()
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return time.Minute, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	config, err := s.GetConfig(ctx)
	if err != nil {
		return time.Minute, err
	}
	if !config.Enabled || len(config.Wallets) == 0 {
		return time.Minute, nil
	}
	if s.runnerVersion != config.Version {
		s.runnerDefer = map[string]time.Time{}
		s.runnerVersion = config.Version
	}
	snapshots, err := s.repo.GetSnapshots(ctx)
	if err != nil {
		return time.Minute, err
	}
	due, wait := s.dueWallets(config, snapshots)
	if len(due) == 0 {
		return wait, nil
	}
	release, acquired := s.acquireLock(ctx, "custom:upstream_balance:runner", 4*time.Minute)
	if !acquired {
		return time.Minute, nil
	}
	defer release()
	// A peer may have completed a batch after the first read but before our lock.
	snapshots, err = s.repo.GetSnapshots(ctx)
	if err != nil {
		return time.Minute, err
	}
	due, wait = s.dueWallets(config, snapshots)
	if len(due) == 0 {
		return wait, nil
	}
	index, err := s.accountIndex(ctx)
	if err != nil {
		return time.Minute, err
	}
	state := &upstreamBalanceRefreshState{config: config, index: index}
	queue := make(chan string, 10)
	for _, w := range due {
		item := upstreamBalanceItem(w, index, snapshots[w.ID], s.now(), upstreamBalanceInterval(config))
		if item.AccountCount == 0 || item.ErrorCode == "query_account_unavailable" {
			s.runnerDefer[w.ID] = s.now().Add(time.Minute)
		} else {
			queue <- w.ID
			if len(queue) == cap(queue) {
				break
			}
		}
	}
	close(queue)
	results := make(chan string, 10)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range queue {
				if ctx.Err() != nil {
					return
				}
				item, refreshErr := s.refreshWithState(ctx, id, true, state)
				if refreshErr != nil || item == nil || item.LastAttemptAt == nil {
					results <- id
				}
			}
		}()
	}
	wg.Wait()
	close(results)
	for id := range results {
		s.runnerDefer[id] = s.now().Add(10 * time.Second)
	}
	snapshots, err = s.repo.GetSnapshots(ctx)
	if err != nil {
		return time.Minute, err
	}
	_, wait = s.dueWallets(config, snapshots)
	return wait, nil
}

// Only the monitor's own snapshots are inspected between due batches. An orphan
// wallet is retried once a minute without making healthy second-based wallets wait.
func (s *UpstreamBalanceService) dueWallets(config *UpstreamBalanceConfig, snapshots map[string]*UpstreamBalanceSnapshot) ([]UpstreamBalanceWallet, time.Duration) {
	now := s.now()
	next := now.Add(time.Minute)
	due := []UpstreamBalanceWallet{}
	for _, w := range config.Wallets {
		if !w.Enabled {
			continue
		}
		at := now
		if snapshot := snapshots[w.ID]; snapshot != nil && snapshot.WalletIdentity == upstreamBalanceWalletIdentity(w) {
			item := snapshot.Item
			applyUpstreamBalanceTiming(&item, upstreamBalanceInterval(config))
			if item.NextRefreshAt != nil {
				at = *item.NextRefreshAt
			}
		}
		if deferred := s.runnerDefer[w.ID]; deferred.After(at) {
			at = deferred
		}
		if !now.Before(at) {
			due = append(due, w)
		}
		if at.Before(next) {
			next = at
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		left, right := snapshots[due[i].ID], snapshots[due[j].ID]
		if left == nil || left.Item.LastAttemptAt == nil {
			return right != nil && right.Item.LastAttemptAt != nil
		}
		if right == nil || right.Item.LastAttemptAt == nil {
			return false
		}
		return left.Item.LastAttemptAt.Before(*right.Item.LastAttemptAt)
	})
	wait := next.Sub(now)
	if wait <= 0 {
		wait = 10 * time.Second
	}
	return due, min(time.Minute, wait)
}

// This optional monitor fails closed when Redis is unavailable. It must never
// reserve a PostgreSQL connection for an advisory lock while doing HTTP work.
func (s *UpstreamBalanceService) acquireLock(ctx context.Context, key string, ttl time.Duration) (func(), bool) {
	if s.lockCache == nil {
		return nil, false
	}
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	acquired, err := s.lockCache.TryAcquireLeaderLock(lockCtx, key, s.owner, ttl)
	if err != nil || !acquired {
		return nil, false
	}
	return func() {
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelRelease()
		_ = s.lockCache.ReleaseLeaderLock(releaseCtx, key, s.owner)
	}, true
}
