package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/lib/pq"
)

type requestAuditRuntime struct {
	manager  *requestaudit.Manager
	writerDB *sql.DB
	policyDB *sql.DB
}

func provideRequestAuditRuntime(cfg *config.Config) (*requestAuditRuntime, error) {
	writerDB, err := newRequestAuditDB(cfg, cfg.RequestAudit.DBMaxConnections)
	if err != nil {
		return nil, err
	}
	policyDB, err := newRequestAuditDB(cfg, cfg.RequestAudit.PolicyMaxConnections)
	if err != nil {
		_ = writerDB.Close()
		return nil, err
	}

	options := []requestaudit.Option{requestaudit.WithPolicyDB(policyDB)}
	if cfg.RequestAudit.BufferBytes > 0 {
		options = append(options, requestaudit.WithBufferBytes(cfg.RequestAudit.BufferBytes))
	}
	if cfg.RequestAudit.MaxEvents > 0 {
		options = append(options, requestaudit.WithMaxEvents(cfg.RequestAudit.MaxEvents))
	}
	if cfg.RequestAudit.WriteTimeoutMS > 0 {
		options = append(options, requestaudit.WithWriteTimeout(time.Duration(cfg.RequestAudit.WriteTimeoutMS)*time.Millisecond))
	}
	if cfg.RequestAudit.BanLookupTimeoutMS > 0 {
		options = append(options, requestaudit.WithBanLookupTimeout(time.Duration(cfg.RequestAudit.BanLookupTimeoutMS)*time.Millisecond))
	}
	manager := requestaudit.NewManager(writerDB, options...)
	if err := manager.Start(context.Background()); err != nil {
		// A request-audit fault must not prevent normal gateway startup.
		slog.Error("request audit unavailable at startup", "error", err)
	}
	return &requestAuditRuntime{manager: manager, writerDB: writerDB, policyDB: policyDB}, nil
}

func provideRequestAuditManager(runtime *requestAuditRuntime) *requestaudit.Manager {
	if runtime == nil {
		return nil
	}
	return runtime.manager
}

func newRequestAuditDB(cfg *config.Config, limit int) (*sql.DB, error) {
	connector, err := pq.NewConnector(cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	if limit <= 0 {
		limit = 2
	}
	db.SetMaxOpenConns(limit)
	db.SetMaxIdleConns(limit)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

func (runtime *requestAuditRuntime) Stop(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	var stopErr error
	if runtime.manager != nil {
		stopErr = runtime.manager.Stop(ctx)
	}
	if runtime.writerDB != nil {
		_ = runtime.writerDB.Close()
	}
	if runtime.policyDB != nil {
		_ = runtime.policyDB.Close()
	}
	return stopErr
}
