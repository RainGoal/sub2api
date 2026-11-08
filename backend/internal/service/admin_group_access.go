package service

import (
	"context"
	"errors"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// saveGroupWithAccessGrants keeps the visibility change and additive grants in
// one transaction. Existing user permissions and rate overrides are untouched.
func (s *adminServiceImpl) saveGroupWithAccessGrants(ctx context.Context, group *Group, userIDs []int64) ([]int64, error) {
	ids, err := normalizeGroupAccessGrants(group, userIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, s.groupRepo.Update(ctx, group)
	}
	if s.entClient == nil {
		return nil, errors.New("group access grants require a database transaction")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)
	// Lock live users so a concurrent soft deletion cannot grant stale access.
	users, err := tx.User.Query().Where(user.IDIn(ids...), user.DeletedAtIsNil()).
		ForShare().Select(user.FieldID).All(opCtx)
	if err != nil {
		return nil, fmt.Errorf("validate group access users: %w", err)
	}
	if len(users) != len(ids) {
		return nil, infraerrors.BadRequest("INVALID_GRANT_USER_IDS", "one or more selected users no longer exist")
	}
	if err := s.groupRepo.Update(opCtx, group); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := s.userRepo.AddGroupToAllowedGroups(opCtx, id, group.ID); err != nil {
			return nil, fmt.Errorf("grant group access: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

func normalizeGroupAccessGrants(group *Group, userIDs []int64) ([]int64, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	if !group.IsExclusive || group.SubscriptionType != SubscriptionTypeStandard {
		return nil, infraerrors.BadRequest("GROUP_ACCESS_GRANTS_NOT_SUPPORTED", "access grants require an exclusive standard group")
	}
	seen := make(map[int64]struct{}, len(userIDs))
	ids := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if id <= 0 {
			return nil, infraerrors.BadRequest("INVALID_GRANT_USER_IDS", "selected user IDs must be positive")
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
