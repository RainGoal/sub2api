package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fallbackUserRepo struct {
	UserRepository
	user *User
}

func (r *fallbackUserRepo) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type fallbackGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

type fallbackKeyRepo struct {
	APIKeyRepository
	key    *APIKey
	fields APIKeyUpdateFields
}

func (r *fallbackKeyRepo) Create(_ context.Context, key *APIKey) error {
	key.ID = 10
	r.key = key
	return nil
}
func (r *fallbackKeyRepo) GetByID(context.Context, int64) (*APIKey, error) {
	key := *r.key
	return &key, nil
}
func (r *fallbackKeyRepo) Update(_ context.Context, key *APIKey, fields APIKeyUpdateFields) error {
	r.key, r.fields = key, fields
	return nil
}

func (r *fallbackGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

func (r *fallbackGroupRepo) GetByID(ctx context.Context, id int64) (*Group, error) {
	return r.GetByIDLite(ctx, id)
}

func fallbackServiceFixture() (*APIKeyService, *APIKey, *fallbackUserRepo, *fallbackGroupRepo) {
	primaryID, fallbackID := int64(1), int64(2)
	users := &fallbackUserRepo{user: &User{ID: 7, Status: StatusActive}}
	groups := &fallbackGroupRepo{groups: map[int64]*Group{
		1: {ID: 1, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard},
		2: {ID: 2, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard},
	}}
	svc := &APIKeyService{cfg: &config.Config{}, userRepo: users, groupRepo: groups}
	key := &APIKey{ID: 10, UserID: 7, GroupID: &primaryID, FallbackGroupID: &fallbackID, Status: StatusActive}
	return svc, key, users, groups
}

func TestAPIKeyFallbackResolveFreshEligibility(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIKeyService, *APIKey, *fallbackUserRepo, *fallbackGroupRepo)
		want   error
	}{
		{"eligible", func(*APIKeyService, *APIKey, *fallbackUserRepo, *fallbackGroupRepo) {}, nil},
		{"missing primary", func(_ *APIKeyService, k *APIKey, _ *fallbackUserRepo, _ *fallbackGroupRepo) { k.GroupID = nil }, ErrAPIKeyFallbackInvalid},
		{"same group", func(_ *APIKeyService, k *APIKey, _ *fallbackUserRepo, _ *fallbackGroupRepo) {
			k.FallbackGroupID = k.GroupID
		}, ErrAPIKeyFallbackInvalid},
		{"disabled user", func(_ *APIKeyService, _ *APIKey, u *fallbackUserRepo, _ *fallbackGroupRepo) {
			u.user.Status = StatusDisabled
		}, ErrGroupNotAllowed},
		{"revoked backup permission", func(_ *APIKeyService, _ *APIKey, u *fallbackUserRepo, _ *fallbackGroupRepo) {
			u.user.RestrictPublicGroups = true
			u.user.AllowedGroups = []int64{1}
		}, ErrGroupNotAllowed},
		{"revoked primary permission", func(_ *APIKeyService, _ *APIKey, u *fallbackUserRepo, _ *fallbackGroupRepo) {
			u.user.RestrictPublicGroups = true
			u.user.AllowedGroups = []int64{2}
		}, ErrGroupNotAllowed},
		{"deleted backup", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) { delete(g.groups, 2) }, ErrGroupNotFound},
		{"disabled backup", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[2].Status = StatusDisabled
		}, ErrAPIKeyFallbackInvalid},
		{"disabled primary", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[1].Status = StatusDisabled
		}, ErrAPIKeyFallbackInvalid},
		{"subscription backup", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[2].SubscriptionType = SubscriptionTypeSubscription
		}, ErrAPIKeyFallbackInvalid},
		{"subscription primary", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[1].SubscriptionType = SubscriptionTypeSubscription
		}, ErrAPIKeyFallbackInvalid},
		{"different platform", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[2].Platform = PlatformAnthropic
		}, ErrAPIKeyFallbackInvalid},
		{"unsupported platform", func(_ *APIKeyService, _ *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[1].Platform = PlatformGemini
			g.groups[2].Platform = PlatformGemini
		}, ErrAPIKeyFallbackInvalid},
		{"legacy primary route", func(_ *APIKeyService, k *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[1].FallbackGroupID = k.FallbackGroupID
		}, ErrAPIKeyFallbackInvalid},
		{"legacy backup route", func(_ *APIKeyService, k *APIKey, _ *fallbackUserRepo, g *fallbackGroupRepo) {
			g.groups[2].FallbackGroupIDOnInvalidRequest = k.GroupID
		}, ErrAPIKeyFallbackInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, key, users, groups := fallbackServiceFixture()
			tt.mutate(svc, key, users, groups)
			got, err := svc.ResolveFallbackGroup(context.Background(), key)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Same(t, groups.groups[2], got)
			}
		})
	}
}

func TestAPIKeyFallbackNeedsNoGlobalConfiguration(t *testing.T) {
	svc, key, _, groups := fallbackServiceFixture()
	svc.cfg = nil
	group, err := svc.ResolveFallbackGroup(context.Background(), key)
	require.NoError(t, err)
	require.Same(t, groups.groups[2], group)
}

func TestAPIKeyFallbackUnconfiguredSkipsRepositories(t *testing.T) {
	svc := &APIKeyService{}
	group, err := svc.ResolveFallbackGroup(context.Background(), &APIKey{})
	require.NoError(t, err)
	require.Nil(t, group)
}

func TestAPIKeyFallbackCreateAndUpdatePersistenceFields(t *testing.T) {
	svc, key, users, _ := fallbackServiceFixture()
	repo := &fallbackKeyRepo{}
	svc.apiKeyRepo = repo
	created, err := svc.Create(context.Background(), key.UserID, CreateAPIKeyRequest{Name: "fallback", GroupID: key.GroupID, FallbackGroupID: key.FallbackGroupID})
	require.NoError(t, err)
	require.Equal(t, key.FallbackGroupID, created.FallbackGroupID)
	created.QuotaUsed, created.Usage5h = 11, 4
	_, err = svc.Update(context.Background(), created.ID, created.UserID, UpdateAPIKeyRequest{FallbackGroupIDSet: true})
	require.NoError(t, err)
	require.Nil(t, repo.key.FallbackGroupID)
	require.Equal(t, APIKeyUpdateFields{FallbackGroupID: true}, repo.fields)
	require.Equal(t, 11.0, repo.key.QuotaUsed)
	require.Equal(t, 4.0, repo.key.Usage5h)
	users.user.RestrictPublicGroups = true
	users.user.AllowedGroups = []int64{1}
	_, err = svc.Update(context.Background(), created.ID, created.UserID, UpdateAPIKeyRequest{FallbackGroupIDSet: true, FallbackGroupID: key.FallbackGroupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Nil(t, repo.key.FallbackGroupID)
	_, err = svc.Update(context.Background(), created.ID, 999, UpdateAPIKeyRequest{FallbackGroupIDSet: true})
	require.ErrorIs(t, err, ErrInsufficientPerms)
}

func TestAPIKeyFallbackRejectsChangedAuthenticatedPlatform(t *testing.T) {
	svc, key, _, groups := fallbackServiceFixture()
	key.Group = &Group{Platform: PlatformAnthropic}
	group, err := svc.ResolveFallbackGroup(context.Background(), key)
	require.ErrorIs(t, err, ErrAPIKeyFallbackInvalid)
	require.Nil(t, group)
	key.Group.Platform = groups.groups[1].Platform
	group, err = svc.ResolveFallbackGroup(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, group)
}

func TestAPIKeyFallbackUpdateTriStateAndSwitch(t *testing.T) {
	svc, key, _, groups := fallbackServiceFixture()
	oldID := *key.FallbackGroupID
	require.NoError(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{}))
	require.Equal(t, oldID, *key.FallbackGroupID)
	require.NoError(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{FallbackGroupIDSet: true, FallbackGroupID: &oldID}))
	newID := int64(3)
	require.ErrorIs(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{FallbackGroupIDSet: true, FallbackGroupID: &newID}), ErrGroupNotFound)
	require.ErrorIs(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{GroupID: &newID}), ErrGroupNotFound)
	groups.groups[newID] = &Group{ID: newID, Status: StatusActive, Platform: PlatformOpenAI, SubscriptionType: SubscriptionTypeStandard}
	require.NoError(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{FallbackGroupIDSet: true, FallbackGroupID: &newID}))
	require.Equal(t, newID, *key.FallbackGroupID)
	require.NoError(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{FallbackGroupIDSet: true}))
	require.Nil(t, key.FallbackGroupID)
	require.NoError(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{FallbackGroupIDSet: true, FallbackGroupID: &oldID}))
	require.ErrorIs(t, svc.applyFallbackUpdate(context.Background(), key, UpdateAPIKeyRequest{GroupID: &oldID}), ErrAPIKeyFallbackInvalid)
}

func TestAPIKeyFallbackSnapshotRoundTripAndLegacyVersion(t *testing.T) {
	svc, key, users, _ := fallbackServiceFixture()
	key.User = users.user
	snapshot := svc.snapshotFromAPIKey(context.Background(), key)
	require.Equal(t, key.FallbackGroupID, snapshot.FallbackGroupID)
	restored, used, err := svc.applyAuthCacheEntry("key", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, key.FallbackGroupID, restored.FallbackGroupID)
	snapshot.Version = 24
	_, used, err = svc.applyAuthCacheEntry("key", &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.False(t, used)
}
