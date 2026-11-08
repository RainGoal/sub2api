//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type groupAccessGroupRepo struct {
	GroupRepository
	t             *testing.T
	group         *Group
	updateErr     error
	updates       int
	transactional bool
}

func (r *groupAccessGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	copy := *r.group
	return &copy, nil
}

func (r *groupAccessGroupRepo) Update(ctx context.Context, _ *Group) error {
	r.updates++
	require.Equal(r.t, r.transactional, dbent.TxFromContext(ctx) != nil)
	return r.updateErr
}

type groupAccessUserRepo struct {
	UserRepository
	t          *testing.T
	grants     []int64
	failUserID int64
}

func (r *groupAccessUserRepo) AddGroupToAllowedGroups(ctx context.Context, userID, groupID int64) error {
	require.NotNil(r.t, dbent.TxFromContext(ctx))
	require.Equal(r.t, int64(17), groupID)
	r.grants = append(r.grants, userID)
	if userID == r.failUserID {
		return errors.New("forced grant failure")
	}
	return nil
}

type groupAccessCache struct {
	APIKeyAuthCacheInvalidator
	groups       []int64
	users        []int64
	onInvalidate func()
}

func (c *groupAccessCache) InvalidateAuthCacheByGroupID(_ context.Context, id int64) {
	if c.onInvalidate != nil {
		c.onInvalidate()
	}
	c.groups = append(c.groups, id)
}

func (c *groupAccessCache) InvalidateAuthCacheByUserID(_ context.Context, id int64) {
	if c.onInvalidate != nil {
		c.onInvalidate()
	}
	c.users = append(c.users, id)
}

func TestUpdateGroupAccessGrantsTransaction(t *testing.T) {
	for _, tc := range []struct {
		name             string
		alreadyExclusive bool
		missingUser      bool
		updateFailure    bool
		grantFailure     bool
		commitFailure    bool
	}{
		{name: "public to exclusive grants deduplicated users"},
		{name: "already exclusive supports additional grants", alreadyExclusive: true},
		{name: "missing or deleted user rolls back", missingUser: true},
		{name: "group update failure rolls back", updateFailure: true},
		{name: "grant failure rolls back", grantFailure: true},
		{name: "commit failure does not invalidate caches", commitFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			groups := &groupAccessGroupRepo{t: t, transactional: true, group: &Group{
				ID: 17, Name: "public", Platform: PlatformAnthropic, RateMultiplier: 1,
				SubscriptionType: SubscriptionTypeStandard, IsExclusive: tc.alreadyExclusive,
			}}
			users := &groupAccessUserRepo{t: t}
			if tc.updateFailure {
				groups.updateErr = errors.New("forced update failure")
			}
			if tc.grantFailure {
				users.failUserID = 3
			}
			cache := &groupAccessCache{}
			svc := &adminServiceImpl{groupRepo: groups, userRepo: users, authCacheInvalidator: cache,
				entClient: dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))}
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"id"}).AddRow(int64(2))
			if !tc.missingUser {
				rows.AddRow(int64(3))
			}
			mock.ExpectQuery(`SELECT .* FROM "users" .*FOR SHARE`).WithArgs(int64(2), int64(3)).WillReturnRows(rows)
			failed := tc.missingUser || tc.updateFailure || tc.grantFailure || tc.commitFailure
			switch {
			case tc.commitFailure:
				mock.ExpectCommit().WillReturnError(errors.New("forced commit failure"))
			case failed:
				mock.ExpectRollback()
			default:
				mock.ExpectCommit()
				cache.onInvalidate = func() { require.NoError(t, mock.ExpectationsWereMet(), "invalidate only after commit") }
			}
			input := &UpdateGroupInput{GrantUserIDs: []int64{2, 3, 2}}
			if !tc.alreadyExclusive {
				exclusive := true
				input.IsExclusive = &exclusive
			}
			group, err := svc.UpdateGroup(context.Background(), 17, input)
			if failed {
				require.Error(t, err)
				require.Nil(t, group)
				require.Empty(t, cache.groups)
				require.Empty(t, cache.users)
				if tc.missingUser {
					require.Equal(t, 400, infraerrors.Code(err))
					require.Zero(t, groups.updates)
					require.Empty(t, users.grants)
				}
			} else {
				require.NoError(t, err)
				require.True(t, group.IsExclusive)
				require.Equal(t, []int64{2, 3}, users.grants)
				require.Equal(t, []int64{17}, cache.groups)
				require.Equal(t, []int64{2, 3}, cache.users)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateGroupAccessGrantsCompatibilityAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		exclusive        bool
		subscriptionType string
		ids              []int64
		wantError        bool
	}{
		{name: "omitted grants retain public update", subscriptionType: SubscriptionTypeStandard},
		{name: "empty grants retain subscription update", subscriptionType: SubscriptionTypeSubscription, ids: []int64{}},
		{name: "public group cannot receive grants", subscriptionType: SubscriptionTypeStandard, ids: []int64{2}, wantError: true},
		{name: "subscription cannot receive grants", exclusive: true, subscriptionType: SubscriptionTypeSubscription, ids: []int64{2}, wantError: true},
		{name: "zero user ID rejected", exclusive: true, subscriptionType: SubscriptionTypeStandard, ids: []int64{0}, wantError: true},
		{name: "negative user ID rejected", exclusive: true, subscriptionType: SubscriptionTypeStandard, ids: []int64{-2}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &groupAccessGroupRepo{t: t, group: &Group{ID: 17, Name: "group", Platform: PlatformAnthropic,
				RateMultiplier: 1, SubscriptionType: tc.subscriptionType, IsExclusive: tc.exclusive}}
			svc := &adminServiceImpl{groupRepo: repo}
			_, err := svc.UpdateGroup(context.Background(), 17, &UpdateGroupInput{GrantUserIDs: tc.ids})
			if tc.wantError {
				require.Equal(t, 400, infraerrors.Code(err))
				require.Zero(t, repo.updates)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, repo.updates)
			}
		})
	}
}
