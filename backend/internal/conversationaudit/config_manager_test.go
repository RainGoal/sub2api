package conversationaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type configSettingRepositoryStub struct {
	mu     sync.Mutex
	values map[string]string
}

func (r *configSettingRepositoryStub) Get(_ context.Context, _ string) (*service.Setting, error) {
	return nil, nil
}
func (r *configSettingRepositoryStub) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}
func (r *configSettingRepositoryStub) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	r.values[key] = value
	r.mu.Unlock()
	return nil
}
func (r *configSettingRepositoryStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}
func (r *configSettingRepositoryStub) SetMultiple(ctx context.Context, values map[string]string) error {
	for key, value := range values {
		if err := r.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}
func (r *configSettingRepositoryStub) GetAll(_ context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *configSettingRepositoryStub) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type configRuntimeStub struct {
	mu      sync.Mutex
	applied []ActiveConfig
}

func (*configRuntimeStub) Lifecycle() string      { return "disabled" }
func (*configRuntimeStub) EffectiveEnabled() bool { return false }
func (r *configRuntimeStub) ApplyConfig(config ActiveConfig) {
	r.mu.Lock()
	r.applied = append(r.applied, config)
	r.mu.Unlock()
}

func TestConfigManagerReloadAppliesCompleteSnapshotChange(t *testing.T) {
	stored := defaultStoredConfig()
	stored.ConfigVersion = 2
	repository := &configSettingRepositoryStub{values: map[string]string{}}
	repository.values[SettingKeyConversationAudit] = mustStoredConfigJSON(t, stored)
	manager := NewConfigManager(nil, repository, nil, nil, nil)
	runtime := &configRuntimeStub{}
	manager.SetRuntime(runtime)
	require.NoError(t, manager.Reload(context.Background()))

	stored.RetentionDays = 8
	repository.values[SettingKeyConversationAudit] = mustStoredConfigJSON(t, stored)
	require.NoError(t, manager.Reload(context.Background()))
	active, ok := manager.Active()
	require.True(t, ok)
	require.Equal(t, 8, active.RetentionDays)
	runtime.mu.Lock()
	require.Len(t, runtime.applied, 2)
	runtime.mu.Unlock()
}

func TestConfigManagerSaveRejectsStaleVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := NewConfigManager(db, nil, nil, nil, nil)
	current := defaultStoredConfig()
	current.ConfigVersion = 2

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT value FROM settings`).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(mustStoredConfigJSON(t, current)))
	mock.ExpectRollback()

	request := defaultUpdateConfigRequest()
	require.ErrorIs(t, func() error {
		_, err := manager.Save(context.Background(), request, 9)
		return err
	}(), ErrConfigConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestConfigManagerSaveUsesCASAndInstallsNextVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := NewConfigManager(db, nil, nil, nil, nil)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock($1)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT value FROM settings`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO settings`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	public, err := manager.Save(context.Background(), defaultUpdateConfigRequest(), 9)
	require.NoError(t, err)
	require.Equal(t, int64(2), public.ConfigVersion)
	require.Equal(t, int64(9), public.UpdatedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func defaultUpdateConfigRequest() UpdateConfigRequest {
	return UpdateConfigRequest{
		ExpectedConfigVersion: 1, RetentionDays: DefaultRetentionDays,
		RequestMaxBytes: DefaultRequestMaxBytes, ResponseMaxBytes: DefaultResponseMaxBytes,
		MemoryBudgetBytes: DefaultMemoryBudgetBytes, WorkerCount: DefaultWorkerCount,
		QueueCapacity: DefaultQueueCapacity,
	}
}

func mustStoredConfigJSON(t *testing.T, config storedConfig) string {
	t.Helper()
	value, err := json.Marshal(config)
	require.NoError(t, err)
	return string(value)
}
