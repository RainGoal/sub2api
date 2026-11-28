package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type publicAnnouncementRepoStub struct {
	service.AnnouncementRepository
	limit int
	items []service.Announcement
}

func (r *publicAnnouncementRepoStub) ListPublicActive(_ context.Context, _ time.Time, limit int) ([]service.Announcement, error) {
	r.limit = limit
	return r.items, nil
}

func TestAnnouncementHandlerListPublic(t *testing.T) {
	actorID := int64(3)
	createdAt := time.Date(2026, time.November, 25, 14, 0, 0, 0, time.UTC)
	repo := &publicAnnouncementRepoStub{items: []service.Announcement{{
		ID:         12,
		Title:      "Announcement",
		Content:    "## Markdown",
		Status:     service.AnnouncementStatusActive,
		NotifyMode: service.AnnouncementNotifyModePopup,
		IsPublic:   true,
		CreatedBy:  &actorID,
		UpdatedBy:  &actorID,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	}}}
	h := NewAnnouncementHandler(service.NewAnnouncementService(repo, nil, nil, nil))
	r := gin.New()
	r.GET("/api/v1/announcements/public", h.ListPublic)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/announcements/public?limit=51", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 50, repo.limit)
	require.JSONEq(t, `{"code":0,"message":"success","data":[{
		"id":12,"title":"Announcement","content":"## Markdown",
		"starts_at":null,"ends_at":null,
		"created_at":"2026-11-25T14:00:00Z","updated_at":"2026-11-25T14:00:00Z"
	}]}`, w.Body.String())

	repo.items = nil
	for _, query := range []string{"", "?limit=-2", "?limit=invalid"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/announcements/public"+query, nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, 20, repo.limit)
		require.JSONEq(t, `{"code":0,"message":"success","data":[]}`, w.Body.String())
	}
}
