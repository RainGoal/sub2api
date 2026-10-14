package config

import "fmt"

// RequestAuditConfig bounds optional audit work independently of the gateway.
// Capture enablement and retention are administrator settings stored in PostgreSQL.
type RequestAuditConfig struct {
	BufferBytes          int64 `mapstructure:"buffer_bytes"`
	MaxEvents            int   `mapstructure:"max_events"`
	WriteTimeoutMS       int   `mapstructure:"write_timeout_ms"`
	DBMaxConnections     int   `mapstructure:"db_max_connections"`
	PolicyMaxConnections int   `mapstructure:"policy_max_connections"`
	BanLookupTimeoutMS   int   `mapstructure:"ban_lookup_timeout_ms"`
}

func (c RequestAuditConfig) validate() error {
	if c.BufferBytes < 0 || (c.BufferBytes > 0 && c.BufferBytes < 1024*1024) {
		return fmt.Errorf("request_audit.buffer_bytes must be zero (default) or at least 1 MiB")
	}
	if c.MaxEvents < 0 || c.MaxEvents > 65536 {
		return fmt.Errorf("request_audit.max_events must be between 0 and 65536")
	}
	if c.WriteTimeoutMS < 0 || c.WriteTimeoutMS > 10000 {
		return fmt.Errorf("request_audit.write_timeout_ms must be between 0 and 10000")
	}
	if c.DBMaxConnections < 0 || c.DBMaxConnections > 16 || c.PolicyMaxConnections < 0 || c.PolicyMaxConnections > 16 {
		return fmt.Errorf("request_audit connection limits must be between 0 and 16")
	}
	if c.BanLookupTimeoutMS < 0 || c.BanLookupTimeoutMS > 1000 {
		return fmt.Errorf("request_audit.ban_lookup_timeout_ms must be between 0 and 1000")
	}
	return nil
}
