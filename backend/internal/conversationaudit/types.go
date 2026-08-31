package conversationaudit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const CanonicalVersion = 1

type RecordState string

const (
	RecordStateCapturing RecordState = "capturing"
	RecordStateFinalized RecordState = "finalized"
)

type OutcomeStatus string

const (
	OutcomeCompleted OutcomeStatus = "completed"
	OutcomeError     OutcomeStatus = "error"
	OutcomeTimeout   OutcomeStatus = "timeout"
	OutcomePartial   OutcomeStatus = "partial"
	OutcomeCancelled OutcomeStatus = "cancelled"
	OutcomeUnknown   OutcomeStatus = "unknown"
)

type CaptureStatus string

const (
	CaptureComplete     CaptureStatus = "complete"
	CaptureTruncated    CaptureStatus = "truncated"
	CaptureMetadataOnly CaptureStatus = "metadata_only"
	CaptureDegraded     CaptureStatus = "degraded"
)

type PayloadSide string

const (
	PayloadSideRequest  PayloadSide = "request"
	PayloadSideResponse PayloadSide = "response"
)

type TransportMode string

const (
	TransportHTTP      TransportMode = "http"
	TransportSSE       TransportMode = "sse"
	TransportWebSocket TransportMode = "websocket"
)

type ContentItem struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Name         string `json:"name,omitempty"`
	Arguments    string `json:"arguments,omitempty"`
	Content      string `json:"content,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	EncodedBytes int64  `json:"encoded_bytes,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
	ID           string `json:"id,omitempty"`
	URL          string `json:"url,omitempty"`
	OmittedBytes int64  `json:"omitted_bytes,omitempty"`
}

type Message struct {
	Role    string        `json:"role"`
	Content []ContentItem `json:"content"`
}

type CanonicalError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type CanonicalConversation struct {
	Version         int             `json:"version"`
	Messages        []Message       `json:"messages"`
	Error           *CanonicalError `json:"error,omitempty"`
	OmittedMessages int             `json:"omitted_messages,omitempty"`
	OmittedBytes    int64           `json:"omitted_bytes,omitempty"`
	Truncated       bool            `json:"truncated"`
}

type BeginInput struct {
	AuditID         uuid.UUID
	CreatedAt       time.Time
	RequestID       string
	SessionID       string
	UserID          int64
	UserName        string
	APIKeyID        int64
	APIKeyName      string
	Protocol        string
	InboundEndpoint string
	RequestedModel  string
	TransportMode   TransportMode
}

type MetadataPatch struct {
	GroupID        *int64
	GroupName      string
	AccountID      *int64
	AccountName    string
	EffectiveModel string
}

type ResponseEvent struct {
	Message Message
	Error   *CanonicalError
}

type FinishResult struct {
	CompletedAt    time.Time
	OutcomeStatus  OutcomeStatus
	CaptureStatus  CaptureStatus
	HTTPStatus     int
	ErrorCode      string
	DegradedReason string
}

// Recorder is the only gateway-facing dependency. Implementations must never
// turn audit failures into gateway failures.
type Recorder interface {
	Begin(context.Context, BeginInput) Session
}

type Session interface {
	Annotate(MetadataPatch)
	SetRequestBody(protocol string, body []byte)
	SetRequest(CanonicalConversation)
	Observe(ResponseEvent)
	Finish(FinishResult)
}

type noopRecorder struct{}
type noopSession struct{}

var sharedNoopSession Session = noopSession{}

func NoopRecorder() Recorder { return noopRecorder{} }

func (noopRecorder) Begin(context.Context, BeginInput) Session { return sharedNoopSession }
func (noopSession) Annotate(MetadataPatch)                     {}
func (noopSession) SetRequestBody(string, []byte)              {}
func (noopSession) SetRequest(CanonicalConversation)           {}
func (noopSession) Observe(ResponseEvent)                      {}
func (noopSession) Finish(FinishResult)                        {}
