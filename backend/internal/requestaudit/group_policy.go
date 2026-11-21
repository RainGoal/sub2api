package requestaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

const GroupPoliciesKey = "request_audit_group_policies"

type GroupPolicy struct {
	GroupID                 int64 `json:"group_id"`
	InboundHeaders          bool  `json:"inbound_headers"`
	InboundBody             bool  `json:"inbound_body"`
	UpstreamRequestHeaders  bool  `json:"upstream_request_headers"`
	UpstreamRequestBody     bool  `json:"upstream_request_body"`
	UpstreamResponseHeaders bool  `json:"upstream_response_headers"`
	UpstreamResponseBody    bool  `json:"upstream_response_body"`
}

func (p GroupPolicy) hasCapturePart() bool {
	return p.InboundHeaders || p.InboundBody || p.UpstreamRequestHeaders ||
		p.UpstreamRequestBody || p.UpstreamResponseHeaders || p.UpstreamResponseBody
}

type GroupPolicyConfig struct {
	GroupOnly bool          `json:"group_only"`
	Rules     []GroupPolicy `json:"rules"`
	Version   int64         `json:"version"`
}

type groupPolicySnapshot struct {
	groupOnly bool
	version   int64
	rules     map[int64]*GroupPolicy
}

func validateGroupPolicyConfig(cfg GroupPolicyConfig) error {
	seen := make(map[int64]struct{}, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if rule.GroupID <= 0 {
			return failure(http.StatusBadRequest, "INVALID_ARGUMENT", "Group IDs must be positive")
		}
		if _, duplicate := seen[rule.GroupID]; duplicate {
			return failure(http.StatusBadRequest, "INVALID_ARGUMENT", "A group can have only one audit rule")
		}
		if cfg.GroupOnly && !rule.hasCapturePart() {
			return failure(http.StatusBadRequest, "INVALID_ARGUMENT", "Select at least one audit part for each group")
		}
		seen[rule.GroupID] = struct{}{}
	}
	return nil
}

func decodeGroupPolicies(raw string, queryErr error) (GroupPolicyConfig, error) {
	if errors.Is(queryErr, sql.ErrNoRows) {
		return GroupPolicyConfig{Rules: []GroupPolicy{}, Version: 1}, nil
	}
	if queryErr != nil {
		return GroupPolicyConfig{}, queryErr
	}
	var cfg GroupPolicyConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return GroupPolicyConfig{}, err
	}
	if cfg.Version < 1 {
		return GroupPolicyConfig{}, errors.New("invalid stored audit group policy version")
	}
	if cfg.Rules == nil {
		cfg.Rules = []GroupPolicy{}
	}
	return cfg, validateGroupPolicyConfig(cfg)
}

func (m *Manager) cacheGroupPolicies(cfg GroupPolicyConfig) {
	snapshot := &groupPolicySnapshot{groupOnly: cfg.GroupOnly, version: cfg.Version, rules: make(map[int64]*GroupPolicy, len(cfg.Rules))}
	for _, rule := range cfg.Rules {
		snapshot.rules[rule.GroupID] = &rule
	}
	for current := m.groupPolicies.Load(); current == nil || current.version <= cfg.Version; current = m.groupPolicies.Load() {
		if m.groupPolicies.CompareAndSwap(current, snapshot) {
			return
		}
	}
}

func (m *Manager) GetGroupPolicies(ctx context.Context) (GroupPolicyConfig, error) {
	var raw string
	err := m.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, GroupPoliciesKey).Scan(&raw)
	return decodeGroupPolicies(raw, err)
}

func (m *Manager) UpdateGroupPolicies(ctx context.Context, next GroupPolicyConfig, expectedVersion int64) (GroupPolicyConfig, error) {
	if err := validateGroupPolicyConfig(next); err != nil {
		return GroupPolicyConfig{}, err
	}
	for _, rule := range next.Rules {
		if (rule.UpstreamRequestHeaders || rule.UpstreamRequestBody || rule.UpstreamResponseHeaders ||
			rule.UpstreamResponseBody) && !m.UpstreamCaptureAvailable() {
			return GroupPolicyConfig{}, failure(http.StatusBadRequest, "UPSTREAM_AUDIT_KEY_REQUIRED",
				"Configure a stable TOTP_ENCRYPTION_KEY before enabling upstream capture")
		}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return GroupPolicyConfig{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, GroupPoliciesKey); err != nil {
		return GroupPolicyConfig{}, err
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, GroupPoliciesKey).Scan(&raw)
	current, err := decodeGroupPolicies(raw, err)
	if err != nil {
		return GroupPolicyConfig{}, err
	}
	if expectedVersion != current.Version {
		return GroupPolicyConfig{}, failure(http.StatusConflict, "CONFIG_VERSION_CONFLICT", "configuration was updated by another administrator")
	}
	next.Version = current.Version + 1
	if next.Rules == nil {
		next.Rules = []GroupPolicy{}
	}
	data, err := json.Marshal(next)
	if err != nil {
		return GroupPolicyConfig{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, GroupPoliciesKey, string(data)); err != nil {
		return GroupPolicyConfig{}, err
	}
	if err = tx.Commit(); err != nil {
		return GroupPolicyConfig{}, err
	}
	m.cacheGroupPolicies(next)
	return next, nil
}

func (m *Manager) refreshGroupPolicies(ctx context.Context) {
	loadCtx, cancel := context.WithTimeout(ctx, m.writeTimeout)
	defer cancel()
	cfg, err := m.GetGroupPolicies(loadCtx)
	if err != nil {
		// An unknown policy cannot authorize collection. Keep an already known policy.
		m.groupPolicies.CompareAndSwap(nil, &groupPolicySnapshot{groupOnly: true})
		m.warn("group policy refresh failed; retaining the last known setting")
		return
	}
	m.cacheGroupPolicies(cfg)
}
