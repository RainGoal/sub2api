package conversationaudit

import (
	"time"

	"github.com/google/uuid"
)

const DefaultJobDeadline = 2 * time.Minute

type RecordWrite struct {
	AuditID         uuid.UUID
	CreatedAt       time.Time
	CompletedAt     *time.Time
	MutableUntil    *time.Time
	OwnerInstanceID string
	LeaseExpiresAt  *time.Time

	RequestID   string
	SessionID   string
	UserID      int64
	UserName    string
	APIKeyID    int64
	APIKeyName  string
	GroupID     *int64
	GroupName   string
	AccountID   *int64
	AccountName string

	Protocol        string
	InboundEndpoint string
	RequestedModel  string
	EffectiveModel  string
	TransportMode   TransportMode
	HTTPStatus      *int
	ErrorCode       string
	RecordState     RecordState
	OutcomeStatus   *OutcomeStatus
	CaptureStatus   CaptureStatus
	DegradedReason  string

	Payload *StoredPayload
}

type StoredPayload struct {
	Side            PayloadSide
	CodecVersion    int16
	KeyID           string
	Data            []byte
	OriginalBytes   int64
	StoredBytes     int64
	CompressedBytes int64
	EncryptedBytes  int64
	Truncated       bool
	OmittedMessages int
	OmittedBytes    int64
}

type WriteJob struct {
	Record         RecordWrite
	Side           PayloadSide
	Canonical      *CanonicalConversation
	CanonicalStats CanonicalStats
	RawPayload     []byte
	RawSegments    [][]byte
	Protocol       string
	MaxBytes       int
	RawTruncated   bool
	Deadline       time.Time
	reservedBytes  int64
}

func (j *WriteJob) normalize(now time.Time) {
	if j.Deadline.IsZero() {
		j.Deadline = now.Add(DefaultJobDeadline)
	}
	j.Record.CreatedAt = NormalizeCreatedAt(j.Record.CreatedAt)
}

func NormalizeCreatedAt(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}
