package conversationaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	appconfig "github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

type configRuntime interface {
	Lifecycle() string
	EffectiveEnabled() bool
	ApplyConfig(ActiveConfig)
}

type configSnapshot struct {
	stored   storedConfig
	active   ActiveConfig
	loadedAt time.Time
}

type ConfigManager struct {
	db         *sql.DB
	settings   service.SettingRepository
	redis      *redis.Client
	repository *Repository
	deployment appconfig.ConversationAuditConfig
	now        func() time.Time

	snapshot  atomic.Pointer[configSnapshot]
	runtimeMu sync.RWMutex
	runtime   configRuntime

	stateMu     sync.RWMutex
	lastError   string
	lastErrorAt *time.Time
	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewConfigManager(db *sql.DB, settings service.SettingRepository, redisClient *redis.Client, repository *Repository, cfg *appconfig.Config) *ConfigManager {
	manager := &ConfigManager{db: db, settings: settings, redis: redisClient, repository: repository, now: time.Now}
	if cfg != nil {
		manager.deployment = cfg.ConversationAudit
	}
	return manager
}

func (m *ConfigManager) SetRuntime(runtime configRuntime) {
	m.runtimeMu.Lock()
	m.runtime = runtime
	m.runtimeMu.Unlock()
}

func (m *ConfigManager) Start(ctx context.Context) error {
	if m == nil {
		return errors.New("conversation audit config manager is unavailable")
	}
	m.lifecycleMu.Lock()
	if m.cancel != nil {
		m.lifecycleMu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.lifecycleMu.Unlock()
	loadErr := m.Reload(runCtx)
	m.wg.Add(1)
	go m.refreshLoop(runCtx)
	if m.redis != nil {
		m.wg.Add(1)
		go m.subscribeLoop(runCtx)
	}
	return loadErr
}

func (m *ConfigManager) Shutdown() {
	if m == nil {
		return
	}
	m.lifecycleMu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

func (m *ConfigManager) Reload(ctx context.Context) error {
	if m == nil || m.settings == nil {
		return errors.New("conversation audit settings are unavailable")
	}
	values, err := m.settings.GetMultiple(ctx, []string{SettingKeyConversationAudit})
	if err != nil {
		m.recordError("config_load_failed")
		return err
	}
	stored, err := parseStoredConfig(values[SettingKeyConversationAudit])
	if err != nil {
		m.recordError("config_invalid")
		return err
	}
	previous := m.snapshot.Load()
	if previous != nil && previous.stored == stored {
		return nil
	}
	m.install(stored)
	m.clearError()
	return nil
}

func (m *ConfigManager) Active() (ActiveConfig, bool) {
	if m == nil {
		return ActiveConfig{}, false
	}
	snapshot := m.snapshot.Load()
	if snapshot == nil {
		return ActiveConfig{}, false
	}
	return snapshot.active, true
}

func (m *ConfigManager) Public() (PublicConfig, error) {
	if m == nil || m.snapshot.Load() == nil {
		return PublicConfig{}, errors.New("conversation audit configuration is unavailable")
	}
	snapshot := m.snapshot.Load()
	_, keyErr := m.deployment.ParseKeyring()
	lifecycle, effective := "disabled", false
	m.runtimeMu.RLock()
	if m.runtime != nil {
		lifecycle = m.runtime.Lifecycle()
		effective = m.runtime.EffectiveEnabled()
	}
	m.runtimeMu.RUnlock()
	return publicFromStored(snapshot.stored, lifecycle, effective, keyErr == nil && m.keyringConfigured()), nil
}

func (m *ConfigManager) Save(ctx context.Context, request UpdateConfigRequest, actorID int64) (PublicConfig, error) {
	if m == nil || m.db == nil {
		return PublicConfig{}, errors.New("conversation audit configuration persistence is unavailable")
	}
	if request.ExpectedConfigVersion < 1 {
		return PublicConfig{}, ErrConfigConflict
	}
	next := storedConfig{
		Enabled: request.Enabled, RetentionDays: request.RetentionDays,
		RequestMaxBytes: request.RequestMaxBytes, ResponseMaxBytes: request.ResponseMaxBytes,
		MemoryBudgetBytes: request.MemoryBudgetBytes, WorkerCount: request.WorkerCount,
		QueueCapacity: request.QueueCapacity,
	}
	if err := validateStoredConfig(next); err != nil {
		return PublicConfig{}, err
	}
	if request.Enabled {
		keyring, err := m.deployment.ParseKeyring()
		if err != nil || !keyring.Configured() {
			return PublicConfig{}, ErrEncryptionUnavailable
		}
		if m.repository == nil {
			return PublicConfig{}, errors.New("conversation audit database is unavailable")
		}
		if err := m.repository.EnsurePartitions(ctx, m.now()); err != nil {
			return PublicConfig{}, errors.New("conversation audit partitions are unavailable")
		}
	}
	m.runtimeMu.RLock()
	lifecycle := "disabled"
	if m.runtime != nil {
		lifecycle = m.runtime.Lifecycle()
	}
	m.runtimeMu.RUnlock()
	if lifecycle == "disabling" {
		return PublicConfig{}, ErrConfigConflict
	}

	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return PublicConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, conversationAuditConfigLockKey); err != nil {
		return PublicConfig{}, err
	}
	current := defaultStoredConfig()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, SettingKeyConversationAudit).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublicConfig{}, err
	}
	if err == nil {
		current, err = parseStoredConfig(raw)
		if err != nil {
			return PublicConfig{}, err
		}
	}
	if current.ConfigVersion != request.ExpectedConfigVersion {
		return PublicConfig{}, ErrConfigConflict
	}
	if current.Enabled && (current.WorkerCount != next.WorkerCount || current.QueueCapacity != next.QueueCapacity) {
		return PublicConfig{}, ErrConfigConflict
	}
	next.ConfigVersion = current.ConfigVersion + 1
	next.UpdatedAt = m.now().UTC()
	next.UpdatedBy = actorID
	rawNext, err := json.Marshal(next)
	if err != nil {
		return PublicConfig{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`,
		SettingKeyConversationAudit, string(rawNext)); err != nil {
		return PublicConfig{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublicConfig{}, err
	}
	m.install(next)
	if m.redis != nil {
		_ = m.redis.Publish(ctx, ConfigInvalidationChannel, strconv.FormatInt(next.ConfigVersion, 10)).Err()
	}
	return m.Public()
}

func (m *ConfigManager) RuntimeError() (string, *time.Time) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	var at *time.Time
	if m.lastErrorAt != nil {
		value := *m.lastErrorAt
		at = &value
	}
	return m.lastError, at
}

func (m *ConfigManager) install(stored storedConfig) {
	active := activeFromStored(stored)
	m.snapshot.Store(&configSnapshot{stored: stored, active: active, loadedAt: m.now().UTC()})
	m.runtimeMu.RLock()
	runtime := m.runtime
	m.runtimeMu.RUnlock()
	if runtime != nil {
		runtime.ApplyConfig(active)
	}
}

func (m *ConfigManager) keyringConfigured() bool {
	keyring, err := m.deployment.ParseKeyring()
	return err == nil && keyring.Configured()
}

func (m *ConfigManager) refreshLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.Reload(ctx)
		}
	}
}

func (m *ConfigManager) subscribeLoop(ctx context.Context) {
	defer m.wg.Done()
	pubsub := m.redis.Subscribe(ctx, ConfigInvalidationChannel)
	defer func() { _ = pubsub.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			_ = m.Reload(ctx)
		}
	}
}

func (m *ConfigManager) recordError(code string) {
	now := m.now().UTC()
	m.stateMu.Lock()
	m.lastError = code
	m.lastErrorAt = &now
	m.stateMu.Unlock()
}

func (m *ConfigManager) clearError() {
	m.stateMu.Lock()
	m.lastError = ""
	m.lastErrorAt = nil
	m.stateMu.Unlock()
}

func publicFromStored(stored storedConfig, lifecycle string, effective, encryptionReady bool) PublicConfig {
	return PublicConfig{
		Enabled: stored.Enabled, EffectiveEnabled: effective, Lifecycle: lifecycle,
		EncryptionReady: encryptionReady, RetentionDays: stored.RetentionDays,
		RequestMaxBytes: stored.RequestMaxBytes, ResponseMaxBytes: stored.ResponseMaxBytes,
		MemoryBudgetBytes: stored.MemoryBudgetBytes, WorkerCount: stored.WorkerCount,
		QueueCapacity: stored.QueueCapacity, ConfigVersion: stored.ConfigVersion,
		UpdatedAt: stored.UpdatedAt, UpdatedBy: stored.UpdatedBy,
	}
}
