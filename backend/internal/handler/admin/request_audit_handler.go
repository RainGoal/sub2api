package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestAuditAdminStore is deliberately independent from gateway collection.
type RequestAuditAdminStore interface {
	GetConfig(context.Context) (requestaudit.Config, error)
	UpdateConfig(context.Context, requestaudit.ConfigUpdate) (requestaudit.Config, error)
	Status() requestaudit.Status
	List(context.Context, requestaudit.ListFilter) (requestaudit.ListResult, error)
	Get(context.Context, string) (*requestaudit.Record, error)
	ReadPayload(context.Context, string, string, int64, int64, bool) (requestaudit.PayloadResult, error)
	ListBans(context.Context, requestaudit.BanFilter) (requestaudit.BanList, error)
	GetBan(context.Context, string) (*requestaudit.SessionBan, error)
	CreateBan(context.Context, requestaudit.CreateBanInput) (*requestaudit.SessionBan, error)
	RevokeBan(context.Context, string, int64) (*requestaudit.SessionBan, error)
}

type RequestAuditHandler struct {
	store RequestAuditAdminStore
	reads chan struct{}
}

func NewRequestAuditHandler(manager *requestaudit.Manager) *RequestAuditHandler {
	h := &RequestAuditHandler{reads: make(chan struct{}, 2)}
	if manager != nil {
		h.store = manager
	}
	return h
}

func (h *RequestAuditHandler) authorize(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return false
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	if role != "admin" {
		response.Forbidden(c, "Administrator access required")
		return false
	}
	if h == nil || h.store == nil {
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Request audit storage is unavailable", "AUDIT_STORAGE_UNAVAILABLE", nil)
		return false
	}
	return true
}

func requestAuditError(c *gin.Context, err error) {
	var auditErr *requestaudit.Error
	if errors.As(err, &auditErr) {
		response.ErrorWithDetails(c, auditErr.Status, auditErr.Message, auditErr.Code, auditErr.Metadata)
		return
	}
	response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Request audit storage is unavailable", "AUDIT_STORAGE_UNAVAILABLE", nil)
}

func requestAuditInvalid(c *gin.Context, message string) {
	response.ErrorWithDetails(c, http.StatusBadRequest, message, "INVALID_ARGUMENT", nil)
}

func requestAuditID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		requestAuditInvalid(c, "Invalid record ID")
		return "", false
	}
	return id.String(), true
}

func requestAuditPositive(c *gin.Context, key string, fallback int64) (int64, bool) {
	value := c.Query(key)
	if value == "" {
		return fallback, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		requestAuditInvalid(c, "Invalid "+key)
		return 0, false
	}
	return n, true
}

func requestAuditLimit(c *gin.Context) (int, bool) {
	n, ok := requestAuditPositive(c, "limit", 50)
	if ok && n > 100 {
		requestAuditInvalid(c, "limit must not exceed 100")
		return 0, false
	}
	return int(n), ok
}

func (h *RequestAuditHandler) List(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	limit, ok := requestAuditLimit(c)
	if !ok {
		return
	}
	user, ok := requestAuditPositive(c, "user_id", 0)
	if !ok {
		return
	}
	key, ok := requestAuditPositive(c, "api_key_id", 0)
	if !ok {
		return
	}
	filter := requestaudit.ListFilter{Limit: limit, UserID: user, APIKeyID: key,
		SessionID: c.Query("session_id"), Kind: c.Query("kind"), CaptureState: c.Query("capture_state"), Cursor: c.Query("cursor")}
	for name, dst := range map[string]*time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := c.Query(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				requestAuditInvalid(c, "Invalid "+name)
				return
			}
			*dst = parsed
		}
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && filter.From.After(filter.To) {
		requestAuditInvalid(c, "from must precede to")
		return
	}
	if raw := c.Query("http_status"); raw != "" {
		status, err := strconv.Atoi(raw)
		if err != nil || status < 100 || status > 599 {
			requestAuditInvalid(c, "Invalid http_status")
			return
		}
		filter.HTTPStatus = status
	}
	result, err := h.store.List(c.Request.Context(), filter)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) Get(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	result, err := h.store.Get(c.Request.Context(), id)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) GetConfig(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	result, err := h.store.GetConfig(c.Request.Context())
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) UpdateConfig(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	var input struct {
		Enabled           *bool  `json:"enabled" binding:"required"`
		SessionBanEnabled *bool  `json:"session_ban_enabled" binding:"required"`
		RetentionDays     int    `json:"retention_days" binding:"min=1,max=30"`
		ExpectedVersion   *int64 `json:"expected_version" binding:"required"`
	}
	if c.ShouldBindJSON(&input) != nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		requestAuditInvalid(c, "Invalid audit configuration")
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	result, err := h.store.UpdateConfig(c.Request.Context(), requestaudit.ConfigUpdate{Enabled: *input.Enabled, SessionBanEnabled: *input.SessionBanEnabled, RetentionDays: input.RetentionDays, ExpectedVersion: *input.ExpectedVersion, UpdatedBy: subject.UserID})
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"enabled": result.Enabled, "blocking_enabled": result.SessionBanEnabled, "config_version": result.Version})
	response.Success(c, result)
}

func (h *RequestAuditHandler) Status(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	response.Success(c, h.store.Status())
}

func (h *RequestAuditHandler) ListBans(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	limit, ok := requestAuditLimit(c)
	if !ok {
		return
	}
	key, ok := requestAuditPositive(c, "api_key_id", 0)
	if !ok {
		return
	}
	state := c.Query("state")
	if state != "" && state != "active" && state != "expired" && state != "revoked" {
		requestAuditInvalid(c, "Invalid ban state")
		return
	}
	filter := requestaudit.BanFilter{Limit: limit, APIKeyID: key, SessionID: c.Query("session_id"), SourceAuditID: c.Query("source_audit_id"), State: state, Cursor: c.Query("cursor")}
	result, err := h.store.ListBans(c.Request.Context(), filter)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) GetBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	result, err := h.store.GetBan(c.Request.Context(), id)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *RequestAuditHandler) CreateBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	var input requestaudit.CreateBanInput
	if c.ShouldBindJSON(&input) != nil {
		requestAuditInvalid(c, "Invalid session ban")
		return
	}
	if _, err := uuid.Parse(input.SourceAuditID); err != nil {
		requestAuditInvalid(c, "Invalid source_audit_id")
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	input.CreatedBy = subject.UserID
	result, err := h.store.CreateBan(c.Request.Context(), input)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": input.SourceAuditID, "ban_id": result.ID})
	response.Created(c, result)
}

func (h *RequestAuditHandler) RevokeBan(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	result, err := h.store.RevokeBan(c.Request.Context(), id, subject.UserID)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"ban_id": id})
	response.Success(c, result)
}

const requestAuditReadChunk = int64(256 * 1024)

func (h *RequestAuditHandler) Payload(c *gin.Context) {
	if !h.authorize(c) {
		return
	}
	id, ok := requestAuditID(c)
	if !ok {
		return
	}
	part := c.DefaultQuery("part", "body")
	if part != "headers" && part != "body" && part != "trailers" {
		requestAuditInvalid(c, "Invalid payload part")
		return
	}
	mode := c.DefaultQuery("mode", "preview")
	if mode != "preview" && mode != "raw" {
		requestAuditInvalid(c, "Invalid payload mode")
		return
	}
	select {
	case h.reads <- struct{}{}:
		defer func() { <-h.reads }()
	default:
		response.ErrorWithDetails(c, http.StatusTooManyRequests, "Too many active payload reads", "AUDIT_READ_BUSY", nil)
		return
	}
	readCtx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	c.Request = c.Request.WithContext(readCtx)
	allowPartial, err := strconv.ParseBool(c.DefaultQuery("allow_partial", "false"))
	if err != nil {
		requestAuditInvalid(c, "Invalid allow_partial")
		return
	}
	offset, err := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	if err != nil || offset < 0 {
		requestAuditInvalid(c, "Invalid offset")
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"audit_id": id, "part": part, "offset": offset, "result": mode})
	if mode == "raw" {
		h.rawPayload(c, id, part, offset, allowPartial)
		return
	}
	limit, err := strconv.ParseInt(c.DefaultQuery("limit", "65536"), 10, 64)
	if err != nil || limit < 1 || limit > requestAuditReadChunk {
		requestAuditInvalid(c, "Invalid preview limit")
		return
	}
	reveal, err := strconv.ParseBool(c.DefaultQuery("reveal_sensitive", "false"))
	if err != nil {
		requestAuditInvalid(c, "Invalid reveal_sensitive")
		return
	}
	// Masking must happen before pagination; a partial JSON envelope cannot be safely redacted.
	readOffset, readLimit := offset, limit
	if part != "body" && !reveal {
		readOffset, readLimit = 0, requestAuditReadChunk
	}
	result, err := h.store.ReadPayload(c.Request.Context(), id, part, readOffset, readLimit, allowPartial)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	data, redacted := result.Bytes, false
	if part != "body" && !reveal {
		if result.HasMore || !json.Valid(data) {
			response.ErrorWithDetails(c, http.StatusConflict, "Header preview requires explicit sensitive reveal for this payload", "AUDIT_HEADER_PREVIEW_UNAVAILABLE", nil)
			return
		}
		data, err = redactRequestAuditHeaders(data)
		if err != nil {
			requestAuditInvalid(c, "Header envelope cannot be previewed")
			return
		}
		redacted = true
		result.TotalBytes = int64(len(data))
		if offset > result.TotalBytes {
			response.ErrorWithDetails(c, 416, "Invalid payload range", "INVALID_PAYLOAD_RANGE", nil)
			return
		}
		end := offset + min(limit, result.TotalBytes-offset)
		data = data[offset:end]
		result.HasMore = end < result.TotalBytes
	}
	encoding, content := "utf8", string(data)
	if !utf8.Valid(data) {
		encoding, content = "base64", base64.StdEncoding.EncodeToString(data)
	}
	middleware.SetAuditExtra(c, map[string]any{"returned_bytes": len(data), "reveal_sensitive": reveal})
	response.Success(c, gin.H{"content": content, "encoding": encoding, "offset": offset, "returned_bytes": len(data), "part_bytes": result.TotalBytes, "has_more": result.HasMore, "capture_state": result.CaptureState, "partial": result.Partial, "redacted": redacted})
}

func redactRequestAuditHeaders(data []byte) ([]byte, error) {
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	var redact func(map[string]any)
	redact = func(values map[string]any) {
		for key, value := range values {
			lower := strings.ToLower(key)
			if lower == "request_uri" {
				if rawURI, text := value.(string); text {
					values[key] = redactRequestAuditURI(rawURI)
				} else {
					// A binary URI envelope cannot be safely parsed for secret queries.
					values[key] = "[REDACTED]"
				}
			} else if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "set-cookie" || strings.Contains(lower, "api-key") || strings.Contains(lower, "api_key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
				if list, ok := value.([]any); ok {
					for i := range list {
						list[i] = "[REDACTED]"
					}
					values[key] = list
				} else {
					values[key] = "[REDACTED]"
				}
			} else if nested, ok := value.(map[string]any); ok {
				redact(nested)
			}
		}
	}
	redact(envelope)
	return json.MarshalIndent(envelope, "", "  ")
}

func redactRequestAuditURI(raw string) string {
	if raw == "" {
		return raw
	}
	uri, err := url.ParseRequestURI(raw)
	if err != nil {
		return "[REDACTED]"
	}
	query, err := url.ParseQuery(uri.RawQuery)
	if err != nil {
		// ParseQuery may return only part of malformed input; never preview it.
		return "[REDACTED]"
	}
	// Reuse RedactAuditQuery's key policy, but decode parameter names before
	// matching and retain every duplicate value and all nonsensitive fields.
	names := make(map[string]any, len(query))
	for key := range query {
		names[key] = nil
	}
	masked := logredact.RedactMap(names, "api_key", "apikey", "token", "secret", "key")
	changed := false
	if uri.User != nil {
		// Absolute-form request targets may carry credentials outside the query.
		uri.User = url.User("[REDACTED]")
		changed = true
	}
	for key, values := range query {
		if masked[key] != nil {
			for i := range values {
				values[i] = "[REDACTED]"
			}
			changed = true
		}
	}
	if !changed {
		return raw
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func (h *RequestAuditHandler) rawPayload(c *gin.Context, id, part string, offset int64, allowPartial bool) {
	first, err := h.store.ReadPayload(c.Request.Context(), id, part, 0, 1, allowPartial)
	if err != nil {
		requestAuditError(c, err)
		return
	}
	total := first.TotalBytes
	start, end, ranged, err := requestAuditRange(c.GetHeader("Range"), c.Query("limit"), offset, total)
	if err != nil {
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", total))
		response.ErrorWithDetails(c, http.StatusRequestedRangeNotSatisfiable, "Invalid payload range", "INVALID_PAYLOAD_RANGE", nil)
		return
	}
	length := end - start
	// Validate the first actual block before committing HTTP headers.
	var chunk requestaudit.PayloadResult
	if length > 0 {
		chunk, err = h.store.ReadPayload(c.Request.Context(), id, part, start, min(length, requestAuditReadChunk), allowPartial)
		if err != nil {
			requestAuditError(c, err)
			return
		}
		if len(chunk.Bytes) == 0 {
			requestAuditError(c, errors.New("missing payload"))
			return
		}
	}
	// Only audit downloads get a write deadline; gateway SSE/WS stay unchanged.
	controller := http.NewResponseController(c.Writer)
	deadline, ok := c.Request.Context().Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		requestAuditError(c, err)
		return
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	completed, returned := false, int64(0)
	defer func() {
		if !completed {
			middleware.SetAuditExtra(c, map[string]any{"result": "stream_failed", "returned_bytes": returned})
		}
	}()
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"request-%s-%s.bin\"", id, part))
	c.Header("Content-Length", strconv.FormatInt(length, 10))
	c.Header("Accept-Ranges", "bytes")
	c.Header("X-Audit-Capture-State", first.CaptureState)
	if first.Partial {
		c.Header("X-Audit-Partial", "true")
	}
	if ranged {
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end-1, total))
		c.Status(http.StatusPartialContent)
	} else {
		c.Status(http.StatusOK)
	}
	c.Writer.WriteHeaderNow()
	for position := start; position < end; {
		if int64(len(chunk.Bytes)) > end-position || len(chunk.Bytes) == 0 {
			panic(http.ErrAbortHandler)
		}
		written, writeErr := c.Writer.Write(chunk.Bytes)
		returned += int64(written)
		if writeErr != nil || written != len(chunk.Bytes) {
			panic(http.ErrAbortHandler)
		}
		position += int64(written)
		if position < end {
			chunk, err = h.store.ReadPayload(c.Request.Context(), id, part, position, min(end-position, requestAuditReadChunk), allowPartial)
			if err != nil {
				middleware.SetAuditExtra(c, map[string]any{"result": "stream_failed"})
				panic(http.ErrAbortHandler)
			}
		}
	}
	completed = true
	middleware.SetAuditExtra(c, map[string]any{"offset": start, "returned_bytes": length, "result": "downloaded"})
}

// requestAuditRange returns a half-open byte interval and rejects multi-range reads.
func requestAuditRange(header, limit string, offset, total int64) (int64, int64, bool, error) {
	invalid := errors.New("invalid range")
	if offset < 0 || offset > total {
		return 0, 0, false, invalid
	}
	if header != "" {
		if offset != 0 || limit != "" || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") || total == 0 {
			return 0, 0, false, invalid
		}
		parts := strings.Split(strings.TrimPrefix(header, "bytes="), "-")
		if len(parts) != 2 {
			return 0, 0, false, invalid
		}
		if parts[0] == "" {
			suffix, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil || suffix <= 0 {
				return 0, 0, false, invalid
			}
			return max(int64(0), total-suffix), total, true, nil
		}
		start, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || start < 0 || start >= total {
			return 0, 0, false, invalid
		}
		end := total
		if parts[1] != "" {
			last, parseErr := strconv.ParseInt(parts[1], 10, 64)
			if parseErr != nil || last < start {
				return 0, 0, false, invalid
			}
			if last < total-1 {
				end = last + 1
			}
		}
		return start, end, true, nil
	}
	end := total
	if limit != "" {
		n, err := strconv.ParseInt(limit, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, invalid
		}
		end = offset + min(n, total-offset)
	}
	if total > 0 && offset == total {
		return 0, 0, false, invalid
	}
	return offset, end, offset != 0 || end != total, nil
}
