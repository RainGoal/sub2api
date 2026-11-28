package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type announcementRepoStub struct {
	item       *Announcement
	publicList func(time.Time, int) []Announcement
}

func (s *announcementRepoStub) Create(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (s *announcementRepoStub) GetByID(_ context.Context, _ int64) (*Announcement, error) {
	if s.item == nil {
		return nil, ErrAnnouncementNotFound
	}
	item := *s.item
	return &item, nil
}

func (s *announcementRepoStub) Update(_ context.Context, a *Announcement) error {
	s.item = a
	return nil
}

func (*announcementRepoStub) Delete(context.Context, int64) error {
	return nil
}

func (*announcementRepoStub) List(context.Context, pagination.PaginationParams, AnnouncementListFilters) ([]Announcement, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (*announcementRepoStub) ListActive(context.Context, time.Time) ([]Announcement, error) {
	return nil, nil
}

func (s *announcementRepoStub) ListPublicActive(_ context.Context, now time.Time, limit int) ([]Announcement, error) {
	return s.publicList(now, limit), nil
}

func TestAnnouncementServiceListPublicFiltersVisibility(t *testing.T) {
	repo := &announcementRepoStub{publicList: func(now time.Time, limit int) []Announcement {
		require.Equal(t, 20, limit)
		future := now.Add(time.Hour)
		return []Announcement{
			{ID: 1, Status: AnnouncementStatusActive, IsPublic: true, StartsAt: &now, EndsAt: &future},
			{ID: 2, Status: AnnouncementStatusActive},
			{ID: 3, Status: AnnouncementStatusDraft, IsPublic: true},
			{ID: 4, Status: AnnouncementStatusActive, IsPublic: true, EndsAt: &now},
			{ID: 5, Status: AnnouncementStatusActive, IsPublic: true, StartsAt: &future},
			{ID: 6, Status: AnnouncementStatusActive, IsPublic: true, Targeting: announcementTestTargeting()},
		}
	}}
	svc := NewAnnouncementService(repo, nil, nil, nil)

	items, err := svc.ListPublic(context.Background(), 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 1, items[0].ID)

	repo.publicList = func(_ time.Time, limit int) []Announcement {
		require.Equal(t, 50, limit)
		return nil
	}
	items, err = svc.ListPublic(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []Announcement{}, items)
}

func TestAnnouncementServiceRejectsPublicTargetingOnCreate(t *testing.T) {
	repo := &announcementRepoStub{}
	svc := NewAnnouncementService(repo, nil, nil, nil)

	_, err := svc.Create(context.Background(), &CreateAnnouncementInput{
		Title:     "Announcement",
		Content:   "Content",
		IsPublic:  true,
		Targeting: announcementTestTargeting(),
	})
	require.ErrorIs(t, err, ErrAnnouncementPublicTargetingConflict)
	require.Nil(t, repo.item)
}

func TestAnnouncementServiceChecksFinalPublicTargetingOnUpdate(t *testing.T) {
	ctx := context.Background()
	targeting := announcementTestTargeting()
	repo := &announcementRepoStub{item: &Announcement{ID: 1, Targeting: targeting}}
	svc := NewAnnouncementService(repo, nil, nil, nil)

	public := true
	_, err := svc.Update(ctx, 1, &UpdateAnnouncementInput{IsPublic: &public})
	require.ErrorIs(t, err, ErrAnnouncementPublicTargetingConflict)
	require.False(t, repo.item.IsPublic)

	item, err := svc.Update(ctx, 1, &UpdateAnnouncementInput{IsPublic: &public, Targeting: &AnnouncementTargeting{}})
	require.NoError(t, err)
	require.True(t, item.IsPublic)
	require.Empty(t, item.Targeting.AnyOf)

	_, err = svc.Update(ctx, 1, &UpdateAnnouncementInput{Targeting: &targeting})
	require.ErrorIs(t, err, ErrAnnouncementPublicTargetingConflict)
	require.Empty(t, repo.item.Targeting.AnyOf)

	private := false
	item, err = svc.Update(ctx, 1, &UpdateAnnouncementInput{IsPublic: &private, Targeting: &targeting})
	require.NoError(t, err)
	require.False(t, item.IsPublic)
	require.Equal(t, targeting, item.Targeting)
}

func announcementTestTargeting() AnnouncementTargeting {
	return AnnouncementTargeting{AnyOf: []AnnouncementConditionGroup{
		{AllOf: []AnnouncementCondition{{Type: AnnouncementConditionTypeBalance, Operator: AnnouncementOperatorGT, Value: 10}}},
	}}
}

func TestAnnouncementServiceCreateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)

	_, err := svc.Create(context.Background(), &CreateAnnouncementInput{
		Title:      "公告",
		Content:    "内容",
		Status:     AnnouncementStatusActive,
		NotifyMode: AnnouncementNotifyModePopup,
		StartsAt:   &now,
		EndsAt:     &now,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}

func TestAnnouncementServiceUpdateRejectsEqualStartEndTimes(t *testing.T) {
	repo := &announcementRepoStub{
		item: &Announcement{
			ID:         1,
			Title:      "公告",
			Content:    "内容",
			Status:     AnnouncementStatusActive,
			NotifyMode: AnnouncementNotifyModePopup,
		},
	}
	svc := NewAnnouncementService(repo, nil, nil, nil)
	now := time.Unix(1776790020, 0)
	startsAt := &now
	endsAt := &now

	_, err := svc.Update(context.Background(), 1, &UpdateAnnouncementInput{
		StartsAt: &startsAt,
		EndsAt:   &endsAt,
	})
	require.ErrorIs(t, err, ErrAnnouncementInvalidSchedule)
}
