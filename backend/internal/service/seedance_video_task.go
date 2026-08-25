package service

import (
	"context"
	"errors"
	"time"
)

const (
	SeedanceVideoSettlementPending    = "pending"
	SeedanceVideoSettlementProcessing = "processing"
	SeedanceVideoSettlementSettled    = "settled"
	SeedanceVideoSettlementReleased   = "released"
)

var ErrSeedanceVideoTaskNotFound = errors.New("seedance video task not found")

// SeedanceVideoTaskRepository is the durable accounting boundary for the
// fork-owned provider. Implementations must use database-level claims so the
// recovery worker is safe when multiple Sub2API replicas are running.
type SeedanceVideoTaskRepository interface {
	Create(ctx context.Context, pending *SeedanceVideoPendingBilling) error
	AssignAccount(ctx context.Context, stateID string, accountID int64, providerID string) error
	BindProviderTask(ctx context.Context, stateID, taskID, upstreamStatus string, dueAt time.Time) error
	GetByProviderTask(ctx context.Context, taskID string, userID, apiKeyID int64) (*SeedanceVideoPendingBilling, error)
	ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]*SeedanceVideoPendingBilling, error)
	ClaimSettlement(ctx context.Context, taskID string, userID, apiKeyID int64, lease time.Duration) (bool, error)
	Reschedule(ctx context.Context, stateID, upstreamStatus string, dueAt time.Time, lastError string) error
	MarkSettled(ctx context.Context, stateID string, actualCost float64) error
	MarkReleased(ctx context.Context, stateID string) error
}

func (s *OpenAIGatewayService) SetSeedanceVideoTaskRepository(repo SeedanceVideoTaskRepository) {
	if s != nil {
		s.seedanceVideoTaskRepo = repo
	}
}
