package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelPlazaMonitorSaveRejectsOutdatedVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyModelPlazaMonitor, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT value FROM settings .* FOR UPDATE").WithArgs(service.SettingKeyModelPlazaMonitor).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"version":8}`))
	mock.ExpectRollback()
	err = NewModelPlazaMonitorRepository(db).SaveConfig(context.Background(), service.ModelPlazaMonitorConfig{Version: 7}, nil)
	require.ErrorIs(t, err, service.ErrModelPlazaMonitorConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelPlazaMonitorClaimAlreadyLeasedReturnsNoWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	target := service.ModelPlazaMonitorTarget{GroupID: 3, Platform: "openai", Model: "gpt-4o", Signature: "cfg"}
	mock.ExpectQuery("UPDATE model_plaza_monitor_targets SET lease_token=.*next_check_at<=.*lease_until IS NULL OR lease_until<").
		WithArgs("lease", now.Add(time.Minute), target.GroupID, target.Platform, target.Model, target.Signature, now).
		WillReturnError(sql.ErrNoRows)
	state, err := NewModelPlazaMonitorRepository(db).Claim(context.Background(), target, now, "lease")
	require.NoError(t, err)
	require.Nil(t, state)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelPlazaMonitorCompleteDiscardsInvalidatedWorker(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	state := service.ModelPlazaMonitorState{ID: 4, Target: service.ModelPlazaMonitorTarget{Signature: "old"}, LeaseToken: "old-lease"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT failures .* signature=.*lease_token=.*lease_until>NOW.* FOR UPDATE").
		WithArgs(state.ID, state.Target.Signature, state.LeaseToken).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	err = NewModelPlazaMonitorRepository(db).Complete(context.Background(), state, service.ModelPlazaMonitorCheck{Status: "operational"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelPlazaMonitorCompleteFailureThresholdAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name         string
		previous     int
		failure      bool
		inputStatus  string
		wantStatus   string
		wantFailures int
	}{
		{"first failure warns", 0, true, "unknown", "degraded", 1},
		{"second failure unavailable", 1, true, "unknown", "unavailable", 2},
		{"success recovers", 2, false, "operational", "operational", 0},
		{"invalid credentials remain unknown", 2, false, "unknown", "unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			checked := time.Now().UTC()
			state := service.ModelPlazaMonitorState{ID: 4, Target: service.ModelPlazaMonitorTarget{Signature: "cfg", IntervalSeconds: 90}, LeaseToken: "lease"}
			check := service.ModelPlazaMonitorCheck{Status: tc.inputStatus, ServiceFailure: tc.failure, CheckedAt: checked, Reason: "reason"}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT failures .* FOR UPDATE").WithArgs(state.ID, state.Target.Signature, state.LeaseToken).
				WillReturnRows(sqlmock.NewRows([]string{"failures"}).AddRow(tc.previous))
			mock.ExpectExec("UPDATE model_plaza_monitor_targets SET status=").
				WithArgs(state.ID, tc.wantStatus, tc.wantFailures, checked, checked.Add(90*time.Second)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO model_plaza_monitor_checks").
				WithArgs(state.ID, tc.wantStatus, checked, nil, "reason").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("DELETE FROM model_plaza_monitor_checks").WithArgs(state.ID, checked.Add(-24*time.Hour)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			require.NoError(t, NewModelPlazaMonitorRepository(db).Complete(context.Background(), state, check))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestModelPlazaMonitorHistoryReturnsChronologicalPoints(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT status,checked_at,latency_ms .* ORDER BY checked_at DESC,id DESC LIMIT").WithArgs(int64(4), 18).
		WillReturnRows(sqlmock.NewRows([]string{"status", "checked_at", "latency_ms"}).
			AddRow("operational", now, 120).AddRow("degraded", now.Add(-time.Minute), nil))
	points, err := NewModelPlazaMonitorRepository(db).History(context.Background(), 4, 0)
	require.NoError(t, err)
	require.Len(t, points, 2)
	require.Equal(t, "degraded", points[0].Status)
	require.Nil(t, points[0].LatencyMs)
	require.Equal(t, "operational", points[1].Status)
	require.Equal(t, int64(120), *points[1].LatencyMs)
	require.NoError(t, mock.ExpectationsWereMet())
}
