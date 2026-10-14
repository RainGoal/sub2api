// Package requestaudit implements independent, best-effort gateway request
// capture and manually managed session policy without gateway dependencies.
package requestaudit

import (
	"fmt"
	"net/http"
	"time"
)

const (
	PartHeaders    = "headers"
	PartBody       = "body"
	PartTrailers   = "trailers"
	ChunkBytes     = 256 * 1024
	MaxPayloadRead = 256 * 1024
	ConfigKey      = "request_audit_config"
)

type Error struct {
	Status   int               `json:"-"`
	Code     string            `json:"reason"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func failure(status int, code, message string) error {
	return &Error{Status: status, Code: code, Message: message}
}

type Config struct {
	Enabled           bool      `json:"enabled"`
	RetentionDays     int       `json:"retention_days"`
	SessionBanEnabled bool      `json:"session_ban_enabled"`
	Version           int64     `json:"version"`
	UpdatedAt         time.Time `json:"updated_at"`
	UpdatedBy         int64     `json:"updated_by"`
}

func DefaultConfig() Config { return Config{RetentionDays: 3, Version: 1} }

type ConfigUpdate struct {
	Enabled           bool  `json:"enabled"`
	RetentionDays     int   `json:"retention_days"`
	SessionBanEnabled bool  `json:"session_ban_enabled"`
	ExpectedVersion   int64 `json:"expected_version"`
	UpdatedBy         int64 `json:"-"`
}

type RequestMeta struct {
	ID               string
	Kind             string
	ReceivedAt       time.Time
	Method           string
	RoutePattern     string
	Protocol         string
	ClientRequestID  string
	ConnectionID     string
	MessageSeq       int64
	MessageType      string
	Headers          http.Header
	Host             string
	RequestURI       string
	ContentLength    int64
	TransferEncoding []string
	HeaderSource     string
}

type Identity struct {
	UserID            int64  `json:"user_id,omitempty"`
	APIKeyID          int64  `json:"api_key_id,omitempty"`
	GroupID           int64  `json:"group_id,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	SessionSource     string `json:"session_source,omitempty"`
	SessionResolution string `json:"session_resolution,omitempty"`
	Model             string `json:"model,omitempty"`
}

type Completion struct {
	HTTPStatus      int    `json:"http_status"`
	DurationMS      int64  `json:"duration_ms"`
	Outcome         string `json:"outcome"`
	UpstreamSummary string `json:"upstream_summary,omitempty"`
}

type PartManifest struct {
	ExpectedChunks int    `json:"expected_chunks"`
	ExpectedBytes  int64  `json:"expected_bytes"`
	Complete       bool   `json:"complete"`
	Reason         string `json:"reason,omitempty"`
}

type Record struct {
	ID              string    `json:"id"`
	ReceivedAt      time.Time `json:"received_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Kind            string    `json:"kind"`
	InstanceID      string    `json:"instance_id"`
	Method          string    `json:"method"`
	RoutePattern    string    `json:"route_pattern"`
	Protocol        string    `json:"protocol"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	ConnectionID    string    `json:"connection_id,omitempty"`
	MessageSeq      int64     `json:"message_seq,omitempty"`
	MessageType     string    `json:"message_type,omitempty"`
	Identity
	Completion
	CaptureState      string                  `json:"capture_state"`
	CaptureReason     string                  `json:"capture_reason,omitempty"`
	TerminalManifest  map[string]PartManifest `json:"terminal_manifest"`
	ObservedBodyBytes int64                   `json:"observed_body_bytes"`
	StoredBodyBytes   int64                   `json:"stored_body_bytes"`
	FinishedAt        *time.Time              `json:"finished_at,omitempty"`
	LastEventAt       time.Time               `json:"last_event_at"`
}

type ListFilter struct {
	From         time.Time
	To           time.Time
	UserID       int64
	APIKeyID     int64
	SessionID    string
	Kind         string
	CaptureState string
	HTTPStatus   int
	Cursor       string
	Limit        int
}

type ListResult struct {
	Items      []*Record `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

type PayloadResult struct {
	Bytes        []byte `json:"-"`
	TotalBytes   int64  `json:"part_bytes"`
	HasMore      bool   `json:"has_more"`
	CaptureState string `json:"capture_state"`
	Partial      bool   `json:"partial"`
}

type SessionBan struct {
	ID            string     `json:"id"`
	APIKeyID      int64      `json:"api_key_id"`
	SessionID     string     `json:"session_id"`
	PublicReason  string     `json:"public_reason"`
	InternalNote  string     `json:"internal_note,omitempty"`
	SourceAuditID string     `json:"source_audit_id,omitempty"`
	SessionSource string     `json:"session_source"`
	CreatedBy     int64      `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RevokedBy     *int64     `json:"revoked_by,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	State         string     `json:"state"`
}

type CreateBanInput struct {
	SourceAuditID string     `json:"source_audit_id"`
	PublicReason  string     `json:"public_reason"`
	InternalNote  string     `json:"internal_note"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CreatedBy     int64      `json:"-"`
}

type BanFilter struct {
	APIKeyID      int64
	SessionID     string
	SourceAuditID string
	State         string
	Cursor        string
	Limit         int
}

type BanList struct {
	Items      []*SessionBan `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

type Status struct {
	InstanceID         string     `json:"instance_id"`
	Running            bool       `json:"running"`
	ConfigKnown        bool       `json:"config_known"`
	Config             Config     `json:"config"`
	QueueBytes         int64      `json:"queue_bytes"`
	BufferBytes        int64      `json:"buffer_bytes"`
	QueueEvents        int        `json:"queue_events"`
	AcceptedRequests   int64      `json:"accepted_requests"`
	DroppedRequests    int64      `json:"dropped_requests"`
	DroppedEvents      int64      `json:"dropped_events"`
	WriteFailures      int64      `json:"write_failures"`
	WrittenEvents      int64      `json:"written_events"`
	BanCheckFailures   int64      `json:"ban_check_failures"`
	LastWriteAt        *time.Time `json:"last_write_at,omitempty"`
	LastCleanupAt      *time.Time `json:"last_cleanup_at,omitempty"`
	CleanupDeleted     int64      `json:"cleanup_deleted"`
	CleanupFailures    int64      `json:"cleanup_failures"`
	WriterBackoffUntil *time.Time `json:"writer_backoff_until,omitempty"`
}
