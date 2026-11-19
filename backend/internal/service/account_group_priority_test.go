//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type accountGroupPriorityRepoStub struct {
	accountRepoStub
	groups []AccountGroupPriorityInput
}

func (r *accountGroupPriorityRepoStub) BindGroupsWithPriority(_ context.Context, _ int64, groups []AccountGroupPriorityInput) error {
	r.groups = append([]AccountGroupPriorityInput(nil), groups...)
	return nil
}

func TestAccountGroupPriorityInputsValidateUniqueBindings(t *testing.T) {
	require.NoError(t, validateAccountGroupPriorityInputs([]AccountGroupPriorityInput{{GroupID: 1, Priority: 1}, {GroupID: 2, Priority: 9}}))
	require.NoError(t, validateAccountGroupPriorityInputs([]AccountGroupPriorityInput{{GroupID: 1, Priority: 0}}))
	require.Error(t, validateAccountGroupPriorityInputs([]AccountGroupPriorityInput{{GroupID: 0, Priority: 1}}))
	require.Error(t, validateAccountGroupPriorityInputs([]AccountGroupPriorityInput{{GroupID: 1, Priority: -1}}))
	require.Error(t, validateAccountGroupPriorityInputs([]AccountGroupPriorityInput{{GroupID: 1, Priority: 1}, {GroupID: 1, Priority: 2}}))
}

func TestBindGroupsWithPriorityUsesPriorityRepository(t *testing.T) {
	groups := []AccountGroupPriorityInput{{GroupID: 4, Priority: 8}, {GroupID: 9, Priority: 2}}
	repo := &accountGroupPriorityRepoStub{}
	require.NoError(t, bindGroupsWithPriority(context.Background(), repo, 5, groups))
	require.Equal(t, groups, repo.groups)
}

func TestSchedulingPriorityForGroup(t *testing.T) {
	account := &Account{Priority: 50, AccountGroups: []AccountGroup{{GroupID: 3, Priority: 1}, {GroupID: 7, Priority: 9}}}
	require.Equal(t, 1, account.SchedulingPriorityForGroup(3))
	require.Equal(t, 9, account.SchedulingPriorityForGroup(7))
	require.Equal(t, 50, account.SchedulingPriorityForGroup(11))
	require.Zero(t, (*Account)(nil).SchedulingPriorityForGroup(3))
}
