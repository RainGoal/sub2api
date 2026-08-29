package conversationaudit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	appconfig "github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const (
	ErrorCodeInvalidConfig             = "conversation_audit_invalid_config"
	ErrorCodeConfigConflict            = "conversation_audit_config_conflict"
	ErrorCodeEncryptionUnavailable     = "conversation_audit_encryption_unavailable"
	ErrorCodeRecordNotFound            = "conversation_audit_record_not_found"
	ErrorCodePayloadUnavailable        = "conversation_audit_payload_unavailable"
	ErrorCodePayloadDecodeFailed       = "conversation_audit_payload_decode_failed"
	ErrorCodeInvalidCursor             = "conversation_audit_invalid_cursor"
	ErrorCodeQueryLimitExceeded        = "conversation_audit_query_limit_exceeded"
	ErrorCodeDeleteTimeRangeRequired   = "conversation_audit_delete_time_range_required"
	ErrorCodeDeleteConfirmationInvalid = "conversation_audit_delete_confirmation_invalid"
	ErrorCodeRecordBusy                = "conversation_audit_record_busy"

	deleteTokenVersion = 1
	deleteTokenTTL     = 5 * time.Minute
)

type AdminService struct {
	repository *Repository
	manager    *ConfigManager
	capture    *CaptureService
	keyring    KeyProvider
	cursors    *CursorCodec
	tokenKey   []byte
	now        func() time.Time
}

type RecordPage struct {
	Items      []RecordMetadata `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type PayloadDetail struct {
	Available bool                   `json:"available"`
	ErrorCode string                 `json:"error_code,omitempty"`
	Payload   *CanonicalConversation `json:"payload,omitempty"`
}

type RecordDetailView struct {
	Metadata RecordMetadata `json:"metadata"`
	Request  PayloadDetail  `json:"request"`
	Response PayloadDetail  `json:"response"`
}

type DeletePreviewView struct {
	MatchedCount      int       `json:"matched_count"`
	HasMore           bool      `json:"has_more"`
	OperationType     string    `json:"operation_type"`
	EligibilityCutoff time.Time `json:"eligibility_cutoff"`
	ExpiresAt         time.Time `json:"expires_at"`
	FilterHash        string    `json:"filter_hash"`
	ConfirmationToken string    `json:"confirmation_token,omitempty"`
}

type DeleteResult struct {
	DeletedRecords int64 `json:"deleted_records"`
}

type deleteTokenClaims struct {
	Version           int          `json:"v"`
	ActorID           int64        `json:"actor_id"`
	ExpiresAt         int64        `json:"expires_at"`
	EligibilityCutoff time.Time    `json:"eligibility_cutoff"`
	Filter            ListFilter   `json:"filter"`
	HighWater         RecordCursor `json:"high_water"`
	LowWater          RecordCursor `json:"low_water"`
}

func NewAdminService(repository *Repository, manager *ConfigManager, capture *CaptureService, cfg *appconfig.Config) *AdminService {
	secret := ""
	if cfg != nil {
		secret = cfg.JWT.Secret
	}
	cursorKey := deriveAdminSigningKey(secret, "cursor")
	tokenKey := deriveAdminSigningKey(secret, "delete")
	cursors, _ := NewCursorCodec(cursorKey)
	service := &AdminService{
		repository: repository, manager: manager, capture: capture,
		cursors: cursors, tokenKey: tokenKey, now: time.Now,
	}
	if cfg != nil {
		if keyring, err := cfg.ConversationAudit.ParseKeyring(); err == nil && keyring.Configured() {
			service.keyring = keyring
		}
	}
	return service
}

func (s *AdminService) Config() (PublicConfig, error) {
	if s == nil || s.manager == nil {
		return PublicConfig{}, errors.New("conversation audit configuration is unavailable")
	}
	return s.manager.Public()
}

func (s *AdminService) SaveConfig(ctx context.Context, request UpdateConfigRequest, actorID int64) (PublicConfig, error) {
	if s == nil || s.manager == nil {
		return PublicConfig{}, errors.New("conversation audit configuration is unavailable")
	}
	return s.manager.Save(ctx, request, actorID)
}

func (s *AdminService) Runtime() RuntimeState {
	if s == nil || s.capture == nil {
		return RuntimeState{Lifecycle: "disabled"}
	}
	return s.capture.Runtime()
}

func (s *AdminService) List(ctx context.Context, filter ListFilter, cursor string) (RecordPage, error) {
	if s == nil || s.repository == nil {
		return RecordPage{}, errors.New("conversation audit database is unavailable")
	}
	if strings.TrimSpace(cursor) != "" {
		decoded, err := s.cursors.Decode(strings.TrimSpace(cursor))
		if err != nil {
			return RecordPage{}, ErrInvalidCursor
		}
		filter.Cursor = &decoded
	}
	items, next, err := s.repository.List(ctx, filter)
	if err != nil {
		return RecordPage{}, err
	}
	page := RecordPage{Items: items}
	if next != nil {
		page.NextCursor, err = s.cursors.Encode(*next)
		if err != nil {
			return RecordPage{}, err
		}
	}
	return page, nil
}

func (s *AdminService) Detail(ctx context.Context, day time.Time, auditID uuid.UUID) (RecordDetailView, error) {
	if s == nil || s.repository == nil {
		return RecordDetailView{}, errors.New("conversation audit database is unavailable")
	}
	detail, err := s.repository.Detail(ctx, day, auditID)
	if err != nil {
		return RecordDetailView{}, err
	}
	view := RecordDetailView{Metadata: detail.Metadata}
	if detail.Request == nil {
		view.Request.ErrorCode = ErrorCodePayloadUnavailable
	}
	if detail.Response == nil {
		view.Response.ErrorCode = ErrorCodePayloadUnavailable
	}
	if detail.Request == nil && detail.Response == nil {
		return view, nil
	}
	if s.keyring == nil {
		markEncodedSidesDecodeFailed(&view, detail)
		return view, nil
	}
	codec, err := NewPayloadCodec(s.keyring, MaxPayloadMaxBytes, 1)
	if err != nil {
		markEncodedSidesDecodeFailed(&view, detail)
		return view, nil
	}
	defer codec.Close()
	identity := RecordIdentity{AuditID: detail.Metadata.AuditID, CreatedAt: detail.Metadata.CreatedAt}
	if detail.Request != nil {
		payload, decodeErr := codec.Decode(identity, PayloadSideRequest, *detail.Request)
		if decodeErr != nil {
			view.Request.ErrorCode = ErrorCodePayloadDecodeFailed
		} else {
			view.Request.Available = true
			view.Request.Payload = &payload
		}
	}
	if detail.Response != nil {
		payload, decodeErr := codec.Decode(identity, PayloadSideResponse, *detail.Response)
		if decodeErr != nil {
			view.Response.ErrorCode = ErrorCodePayloadDecodeFailed
		} else {
			view.Response.Available = true
			view.Response.Payload = &payload
		}
	}
	return view, nil
}

func (s *AdminService) DeleteOne(ctx context.Context, day time.Time, auditID uuid.UUID) error {
	if s == nil || s.repository == nil {
		return errors.New("conversation audit database is unavailable")
	}
	return s.repository.DeleteOne(ctx, day, auditID, s.now().UTC())
}

func (s *AdminService) PreviewDelete(ctx context.Context, filter ListFilter, actorID int64) (DeletePreviewView, error) {
	if s == nil || s.repository == nil || actorID <= 0 {
		return DeletePreviewView{}, errors.New("conversation audit delete preview is unavailable")
	}
	now := s.now().UTC()
	cutoff := now.Add(-deleteEligibilityAge)
	preview, err := s.repository.PreviewDelete(ctx, filter, cutoff)
	if err != nil {
		return DeletePreviewView{}, err
	}
	view := DeletePreviewView{
		MatchedCount: preview.MatchedCount, HasMore: preview.HasMore, OperationType: "rows",
		EligibilityCutoff: cutoff, ExpiresAt: now.Add(deleteTokenTTL), FilterHash: deleteFilterHash(filter),
	}
	if preview.MatchedCount == 0 || preview.HighWater == nil || preview.LowWater == nil {
		return view, nil
	}
	claims := deleteTokenClaims{
		Version: deleteTokenVersion, ActorID: actorID, ExpiresAt: view.ExpiresAt.Unix(),
		EligibilityCutoff: cutoff, Filter: normalizedDeleteFilter(filter),
		HighWater: *preview.HighWater, LowWater: *preview.LowWater,
	}
	view.ConfirmationToken, err = s.encodeDeleteToken(claims)
	if err != nil {
		return DeletePreviewView{}, err
	}
	return view, nil
}

func (s *AdminService) DeleteByToken(ctx context.Context, token string, actorID int64) (DeleteResult, error) {
	claims, err := s.decodeDeleteToken(token, actorID)
	if err != nil {
		return DeleteResult{}, err
	}
	deleted, err := s.repository.DeleteByScope(ctx, DeleteScope{
		Filter: claims.Filter, EligibilityCutoff: claims.EligibilityCutoff,
		HighWater: claims.HighWater, LowWater: claims.LowWater,
	}, s.now().UTC())
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{DeletedRecords: deleted}, nil
}

func (s *AdminService) encodeDeleteToken(claims deleteTokenClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.tokenKey)
	_, _ = mac.Write(payload)
	signed := append(payload, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(signed), nil
}

func (s *AdminService) decodeDeleteToken(token string, actorID int64) (deleteTokenClaims, error) {
	if s == nil || actorID <= 0 || len(token) == 0 || len(token) > 4096 {
		return deleteTokenClaims{}, ErrInvalidDeleteConfirmation
	}
	signed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(signed) <= sha256.Size {
		return deleteTokenClaims{}, ErrInvalidDeleteConfirmation
	}
	payload, signature := signed[:len(signed)-sha256.Size], signed[len(signed)-sha256.Size:]
	mac := hmac.New(sha256.New, s.tokenKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return deleteTokenClaims{}, ErrInvalidDeleteConfirmation
	}
	var claims deleteTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return deleteTokenClaims{}, ErrInvalidDeleteConfirmation
	}
	if claims.Version != deleteTokenVersion || claims.ActorID != actorID ||
		claims.ExpiresAt < s.now().UTC().Unix() || claims.EligibilityCutoff.IsZero() ||
		claims.HighWater.AuditID == uuid.Nil || claims.LowWater.AuditID == uuid.Nil ||
		!cursorAtOrAfter(claims.HighWater, claims.LowWater) ||
		validateListFilter(claims.Filter) != nil {
		return deleteTokenClaims{}, ErrInvalidDeleteConfirmation
	}
	return claims, nil
}

func cursorAtOrAfter(high, low RecordCursor) bool {
	highAt := NormalizeCreatedAt(high.CreatedAt)
	lowAt := NormalizeCreatedAt(low.CreatedAt)
	if highAt.After(lowAt) {
		return true
	}
	if highAt.Before(lowAt) {
		return false
	}
	return bytes.Compare(high.AuditID[:], low.AuditID[:]) >= 0
}

var ErrInvalidDeleteConfirmation = errors.New("conversation audit delete confirmation is invalid")

func normalizedDeleteFilter(filter ListFilter) ListFilter {
	filter.Start = filter.Start.UTC()
	filter.End = filter.End.UTC()
	filter.Cursor = nil
	filter.Limit = 0
	return filter
}

func deleteFilterHash(filter ListFilter) string {
	payload, _ := json.Marshal(normalizedDeleteFilter(filter))
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:16])
}

func deriveAdminSigningKey(secret, domain string) []byte {
	digest := sha256.Sum256([]byte("sub2api:conversation-audit:" + domain + ":" + secret))
	return digest[:]
}

func markEncodedSidesDecodeFailed(view *RecordDetailView, detail RecordDetail) {
	if detail.Request != nil {
		view.Request.ErrorCode = ErrorCodePayloadDecodeFailed
	}
	if detail.Response != nil {
		view.Response.ErrorCode = ErrorCodePayloadDecodeFailed
	}
}
