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
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				_ = s.RunDue(s.ctx)
			}
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
	if s == nil || s.repo == nil {
		return nil
	}
	if !s.cycleMu.TryLock() {
		return nil
	}
	defer s.cycleMu.Unlock()
	config, err := s.GetConfig(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled || len(config.Wallets) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	release, acquired := s.acquireLock(ctx, "custom:upstream_balance:runner", 4*time.Minute)
	if !acquired {
		return nil
	}
	defer release()
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, w := range list.Config.Wallets {
		enabled[w.ID] = w.Enabled
	}
	items := list.Items
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].NextRefreshAt == nil {
			return items[j].NextRefreshAt != nil
		}
		if items[j].NextRefreshAt == nil {
			return false
		}
		return items[i].NextRefreshAt.Before(*items[j].NextRefreshAt)
	})
	queue := make(chan string, 10)
	for _, item := range items {
		if enabled[item.WalletID] && item.AccountCount > 0 && item.ErrorCode != "query_account_unavailable" && (item.NextRefreshAt == nil || !s.now().Before(*item.NextRefreshAt)) {
			queue <- item.WalletID
			if len(queue) == cap(queue) {
				break
			}
		}
	}
	close(queue)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range queue {
				if ctx.Err() != nil {
					return
				}
				_, _ = s.refresh(ctx, id, true)
			}
		}()
	}
	wg.Wait()
	return nil
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
