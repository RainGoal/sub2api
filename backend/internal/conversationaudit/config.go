package conversationaudit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SettingKeyConversationAudit = "conversation_audit_config"
	ConfigInvalidationChannel   = "conversation_audit:config:invalidate"

	DefaultRetentionDays                 = 7
	DefaultRequestMaxBytes               = 1 << 20
	DefaultResponseMaxBytes              = 1 << 20
	DefaultMemoryBudgetBytes             = 256 << 20
	DefaultWorkerCount                   = 2
	DefaultQueueCapacity                 = 2048
	MinRetentionDays                     = 1
	MaxRetentionDays                     = 365
	MinPayloadMaxBytes                   = 4096
	MaxPayloadMaxBytes                   = 4 << 20
	MinMemoryBudgetBytes                 = 64 << 20
	MaxMemoryBudgetBytes                 = int64(2) << 30
	MinWorkerCount                       = 1
	MaxWorkerCount                       = 8
	MinQueueCapacity                     = 128
	MaxQueueCapacity                     = 8192
	conversationAuditConfigLockKey int64 = 615739204788301103
)

var (
	ErrInvalidConfig         = errors.New("conversation audit configuration is invalid")
	ErrConfigConflict        = errors.New("conversation audit configuration conflict")
	ErrEncryptionUnavailable = errors.New("conversation audit encryption is unavailable")
)

type ActiveConfig struct {
	Enabled           bool
	RetentionDays     int
	RequestMaxBytes   int
	ResponseMaxBytes  int
	MemoryBudgetBytes int64
	WorkerCount       int
	QueueCapacity     int
	ConfigVersion     int64
	UpdatedAt         time.Time
	UpdatedBy         int64
}

type storedConfig struct {
	Enabled           bool      `json:"enabled"`
	RetentionDays     int       `json:"retention_days"`
	RequestMaxBytes   int       `json:"request_max_bytes"`
	ResponseMaxBytes  int       `json:"response_max_bytes"`
	MemoryBudgetBytes int64     `json:"memory_budget_bytes"`
	WorkerCount       int       `json:"worker_count"`
	QueueCapacity     int       `json:"queue_capacity"`
	ConfigVersion     int64     `json:"config_version"`
	UpdatedAt         time.Time `json:"updated_at"`
	UpdatedBy         int64     `json:"updated_by"`
}

type PublicConfig struct {
	Enabled           bool      `json:"enabled"`
	EffectiveEnabled  bool      `json:"effective_enabled"`
	Lifecycle         string    `json:"lifecycle"`
	EncryptionReady   bool      `json:"encryption_ready"`
	RetentionDays     int       `json:"retention_days"`
	RequestMaxBytes   int       `json:"request_max_bytes"`
	ResponseMaxBytes  int       `json:"response_max_bytes"`
	MemoryBudgetBytes int64     `json:"memory_budget_bytes"`
	WorkerCount       int       `json:"worker_count"`
	QueueCapacity     int       `json:"queue_capacity"`
	ConfigVersion     int64     `json:"config_version"`
	UpdatedAt         time.Time `json:"updated_at"`
	UpdatedBy         int64     `json:"updated_by"`
}

type UpdateConfigRequest struct {
	ExpectedConfigVersion int64 `json:"expected_config_version" binding:"required"`
	Enabled               bool  `json:"enabled"`
	RetentionDays         int   `json:"retention_days"`
	RequestMaxBytes       int   `json:"request_max_bytes"`
	ResponseMaxBytes      int   `json:"response_max_bytes"`
	MemoryBudgetBytes     int64 `json:"memory_budget_bytes"`
	WorkerCount           int   `json:"worker_count"`
	QueueCapacity         int   `json:"queue_capacity"`
}

func defaultStoredConfig() storedConfig {
	return storedConfig{
		Enabled: false, RetentionDays: DefaultRetentionDays,
		RequestMaxBytes: DefaultRequestMaxBytes, ResponseMaxBytes: DefaultResponseMaxBytes,
		MemoryBudgetBytes: DefaultMemoryBudgetBytes, WorkerCount: DefaultWorkerCount,
		QueueCapacity: DefaultQueueCapacity, ConfigVersion: 1,
	}
}

func parseStoredConfig(raw string) (storedConfig, error) {
	config := defaultStoredConfig()
	if strings.TrimSpace(raw) == "" {
		return config, nil
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return storedConfig{}, fmt.Errorf("%w: decode stored value", ErrInvalidConfig)
	}
	normalizeStoredConfig(&config)
	if err := validateStoredConfig(config); err != nil {
		return storedConfig{}, err
	}
	return config, nil
}

func normalizeStoredConfig(config *storedConfig) {
	if config.ConfigVersion < 1 {
		config.ConfigVersion = 1
	}
	if config.RetentionDays == 0 {
		config.RetentionDays = DefaultRetentionDays
	}
	if config.RequestMaxBytes == 0 {
		config.RequestMaxBytes = DefaultRequestMaxBytes
	}
	if config.ResponseMaxBytes == 0 {
		config.ResponseMaxBytes = DefaultResponseMaxBytes
	}
	if config.MemoryBudgetBytes == 0 {
		config.MemoryBudgetBytes = DefaultMemoryBudgetBytes
	}
	if config.WorkerCount == 0 {
		config.WorkerCount = DefaultWorkerCount
	}
	if config.QueueCapacity == 0 {
		config.QueueCapacity = DefaultQueueCapacity
	}
}

func validateStoredConfig(config storedConfig) error {
	switch {
	case config.RetentionDays < MinRetentionDays || config.RetentionDays > MaxRetentionDays:
		return fmt.Errorf("%w: retention_days must be between 1 and 365", ErrInvalidConfig)
	case config.RequestMaxBytes < MinPayloadMaxBytes || config.RequestMaxBytes > MaxPayloadMaxBytes:
		return fmt.Errorf("%w: request_max_bytes is out of range", ErrInvalidConfig)
	case config.ResponseMaxBytes < MinPayloadMaxBytes || config.ResponseMaxBytes > MaxPayloadMaxBytes:
		return fmt.Errorf("%w: response_max_bytes is out of range", ErrInvalidConfig)
	case config.MemoryBudgetBytes < MinMemoryBudgetBytes || config.MemoryBudgetBytes > MaxMemoryBudgetBytes:
		return fmt.Errorf("%w: memory_budget_bytes is out of range", ErrInvalidConfig)
	case config.WorkerCount < MinWorkerCount || config.WorkerCount > MaxWorkerCount:
		return fmt.Errorf("%w: worker_count is out of range", ErrInvalidConfig)
	case config.QueueCapacity < MinQueueCapacity || config.QueueCapacity > MaxQueueCapacity:
		return fmt.Errorf("%w: queue_capacity is out of range", ErrInvalidConfig)
	}
	return nil
}

func activeFromStored(config storedConfig) ActiveConfig {
	return ActiveConfig(config)
}
