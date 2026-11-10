package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SettingKeyModelPlazaMonitor      = "model_plaza_monitor_config"
	modelPlazaMonitorDefaultInterval = 300
	modelPlazaMonitorMaxInterval     = 86400
	modelPlazaMonitorHistoryLimit    = 18
)

var (
	ErrModelPlazaMonitorConfig   = infraerrors.BadRequest("MODEL_PLAZA_MONITOR_INVALID_CONFIG", "invalid model plaza monitor configuration")
	ErrModelPlazaMonitorConflict = infraerrors.Conflict("MODEL_PLAZA_MONITOR_CONFIG_CONFLICT", "model plaza monitor configuration has changed; reload before saving")
	ErrModelPlazaMonitorDisabled = infraerrors.BadRequest("MODEL_PLAZA_MONITOR_DISABLED", "model plaza monitor is disabled")
)

type ModelPlazaMonitorModelConfig struct {
	Platform        string `json:"platform"`
	Name            string `json:"name"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	APIMode         string `json:"api_mode,omitempty"`
}

type ModelPlazaMonitorGroupConfig struct {
	GroupID         int64                          `json:"group_id"`
	Enabled         bool                           `json:"enabled"`
	IntervalSeconds int                            `json:"interval_seconds"`
	ProbeAPIKeyID   int64                          `json:"probe_api_key_id"`
	Models          []ModelPlazaMonitorModelConfig `json:"models"`
}

type ModelPlazaMonitorConfig struct {
	Version                int                            `json:"version"`
	Enabled                bool                           `json:"enabled"`
	DefaultIntervalSeconds int                            `json:"default_interval_seconds"`
	Groups                 []ModelPlazaMonitorGroupConfig `json:"groups"`
}

type ModelPlazaMonitorCandidateModel struct {
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Supported bool   `json:"supported"`
}

type ModelPlazaMonitorCandidateGroup struct {
	ID       int64                             `json:"id"`
	Name     string                            `json:"name"`
	Platform string                            `json:"platform"`
	Models   []ModelPlazaMonitorCandidateModel `json:"models"`
}

type ModelPlazaMonitorCandidateKey struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	GroupID int64  `json:"group_id"`
}

type ModelPlazaMonitorCandidates struct {
	Groups  []ModelPlazaMonitorCandidateGroup `json:"groups"`
	APIKeys []ModelPlazaMonitorCandidateKey   `json:"api_keys"`
}

type ModelPlazaMonitorPoint struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
	LatencyMs *int64    `json:"latency_ms,omitempty"`
}

type ModelPlazaMonitorStatus struct {
	GroupID         int64                    `json:"group_id"`
	Platform        string                   `json:"platform"`
	Model           string                   `json:"model"`
	IntervalSeconds int                      `json:"interval_seconds"`
	Status          string                   `json:"status"`
	LastCheckedAt   *time.Time               `json:"last_checked_at"`
	NextCheckAt     *time.Time               `json:"next_check_at"`
	Timeline        []ModelPlazaMonitorPoint `json:"timeline"`
}

type ModelPlazaMonitorStatusResponse struct {
	Enabled    bool                      `json:"enabled"`
	ComputedAt time.Time                 `json:"computed_at"`
	Items      []ModelPlazaMonitorStatus `json:"items"`
}

// ModelPlazaMonitorTarget contains only references, never credential material.
type ModelPlazaMonitorTarget struct {
	GroupID         int64
	Platform        string
	Model           string
	APIMode         string
	IntervalSeconds int
	APIKeyID        int64
	Signature       string
}

type ModelPlazaMonitorState struct {
	ID            int64
	Target        ModelPlazaMonitorTarget
	Failures      int
	Status        string
	LastCheckedAt *time.Time
	NextCheckAt   time.Time
	LeaseToken    string
}

type ModelPlazaMonitorCheck struct {
	Status         string
	CheckedAt      time.Time
	LatencyMs      *int64
	Reason         string
	ServiceFailure bool
}

// Claim/Complete use durable leases: schedulers and manual runs across processes
// cannot charge for the same target concurrently. SaveConfig also reconciles
// active target rows so disabling a target invalidates an in-flight result.
type ModelPlazaMonitorRepository interface {
	SaveConfig(ctx context.Context, config ModelPlazaMonitorConfig, targets []ModelPlazaMonitorTarget) error
	ListStates(ctx context.Context, groupID int64) ([]ModelPlazaMonitorState, error)
	Claim(ctx context.Context, target ModelPlazaMonitorTarget, now time.Time, leaseToken string) (*ModelPlazaMonitorState, error)
	Complete(ctx context.Context, state ModelPlazaMonitorState, result ModelPlazaMonitorCheck) error
	History(ctx context.Context, stateID int64, limit int) ([]ModelPlazaMonitorPoint, error)
}
