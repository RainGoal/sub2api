package requestaudit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestNULModelIsNotIndexedAndOriginalPayloadIsPreserved(t *testing.T) {
	body := []byte(`{"model":"bad\u0000model","messages":[]}`)
	var decoded struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Contains(t, decoded.Model, "\x00")
	m := captureTestManager()
	i := BeginIngress(m, RequestMeta{})
	i.SetModel(decoded.Model)
	require.Empty(t, i.Identity().Model)
	i.Capture().Append(PartBody, body)
	i.Capture().EndPart(PartBody, true, "")
	i.Finish(400, time.Now(), "failed", nil)
	var captured []byte
	for _, event := range takeEvents(m) {
		if event.identity != nil {
			require.Empty(t, event.identity.Model)
		}
		if event.kind == "chunk" && event.part == PartBody {
			captured = append(captured, event.raw...)
		}
	}
	require.Equal(t, body, captured)
}

func TestStorageOmitsInvalidSummariesWithoutChangingInputOrPayload(t *testing.T) {
	original := auditEvent{
		meta:       &RequestMeta{Method: "POST", Protocol: "HTTP/1.1", ClientRequestID: "bad\x00id", MessageType: string([]byte{0xff})},
		identity:   &Identity{SessionID: "bad\x00session", SessionSource: "header", Model: string([]byte{0xff}), DeviceType: "codex", DeviceID: "bad\x00device", DeviceSource: "header", DeviceResolution: string([]byte{0xff})},
		completion: &Completion{Outcome: "failed", UpstreamSummary: "bad\x00summary"}, raw: []byte{0, 0xff, 'a'},
	}
	safe := storageEvent(original)
	require.Empty(t, safe.meta.ClientRequestID)
	require.Empty(t, safe.meta.MessageType)
	require.Empty(t, safe.identity.SessionID, "invalid identity must not be changed into another usable session")
	require.Empty(t, safe.identity.Model)
	require.Empty(t, safe.identity.DeviceID)
	require.Empty(t, safe.identity.DeviceResolution)
	require.Equal(t, "header", safe.identity.DeviceSource)
	require.Empty(t, safe.completion.UpstreamSummary)
	require.Equal(t, "POST", safe.meta.Method)
	require.Equal(t, "header", safe.identity.SessionSource)
	require.Equal(t, "failed", safe.completion.Outcome)
	require.Equal(t, "bad\x00session", original.identity.SessionID)
	require.Equal(t, "bad\x00device", original.identity.DeviceID)
	require.Equal(t, []byte{0, 0xff, 'a'}, safe.raw)
}

func TestPersistSanitizesSummariesAtDatabaseBoundary(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	raw := []byte{0, 0xff, 1}
	mock.ExpectBegin()
	expectRetention(mock)
	mock.ExpectExec(`UPDATE request_audits SET updated_at`).WithArgs(id, int64(1), int64(2), int64(3), "", "header", "resolved", "", "", "", "", "", 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO request_audit_payload_chunks`).WithArgs(id, PartBody, 0, raw, len(raw), 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE request_audits SET updated_at`).WithArgs(id, PartBody, len(raw)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err := m.persistBatch(context.Background(), []auditEvent{
		{kind: "bind", id: id, identity: &Identity{UserID: 1, APIKeyID: 2, GroupID: 3, SessionID: "bad\x00session", SessionSource: "header", SessionResolution: "resolved", Model: string([]byte{0xff})}},
		{kind: "chunk", id: id, part: PartBody, raw: raw},
	})
	require.NoError(t, err)
}

func TestRejectedRequestDoesNotRollbackHealthyRequestsOrStartBackoff(t *testing.T) {
	for _, code := range []pq.ErrorCode{"22021", "23514"} {
		t.Run(string(code), func(t *testing.T) {
			m, mock := mockManager(t)
			good, bad, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
			mock.ExpectBegin()
			expectRetention(mock)
			mock.ExpectExec(`INSERT INTO request_audits`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`UPDATE request_audits SET updated_at`).WillReturnError(&pq.Error{Code: code})
			mock.ExpectRollback()
			mock.ExpectBegin()
			expectRetention(mock)
			mock.ExpectExec(`INSERT INTO request_audits`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`INSERT INTO request_audits`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			now := time.Now()
			batch := []auditEvent{
				{kind: "begin", id: good, meta: &RequestMeta{ReceivedAt: now}, createdAt: now, queued: 512},
				{kind: "bind", id: bad, identity: &Identity{}, createdAt: now, queued: 512},
				{kind: "chunk", id: bad, part: PartBody, raw: []byte("discard with failed request"), createdAt: now, queued: 512},
				{kind: "begin", id: other, meta: &RequestMeta{ReceivedAt: now}, createdAt: now, queued: 512},
			}
			m.queueBytes.Store(2048)
			m.queueEvents.Store(4)
			m.flush(context.Background(), batch)
			require.EqualValues(t, 2, m.writtenEvents.Load())
			require.EqualValues(t, 2, m.droppedEvents.Load())
			require.EqualValues(t, 2, m.writeFailures.Load())
			require.Zero(t, m.circuitUntil.Load(), "a data error must not disable unrelated capture")
			require.Zero(t, m.queueBytes.Load())
			require.Zero(t, m.queueEvents.Load())
		})
	}
}

func TestOnlyRowValueErrorsAreIsolated(t *testing.T) {
	for _, code := range []pq.ErrorCode{"08006", "22000", "23000", "25P02", "40001", "42P01", "53100", "53200", "57014"} {
		cause := &pq.Error{Code: code}
		got := isolateEventError("request", cause)
		var rejected *rejectedEventError
		require.False(t, errors.As(got, &rejected), "SQLSTATE %s must remain a batch failure", code)
		require.Same(t, cause, got)
	}
}

func TestConfigurationAndCommitErrorsAreNotMistakenForBadRequests(t *testing.T) {
	for _, phase := range []string{"configuration", "commit"} {
		t.Run(phase, func(t *testing.T) {
			m, mock := mockManager(t)
			mock.ExpectBegin()
			if phase == "configuration" {
				mock.ExpectExec(`SELECT pg_advisory_xact_lock_shared`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`SELECT COALESCE`).WillReturnError(&pq.Error{Code: "22P02"})
				mock.ExpectRollback()
			} else {
				expectRetention(mock)
				mock.ExpectExec(`INSERT INTO request_audits`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit().WillReturnError(&pq.Error{Code: "23514"})
			}
			batch := []auditEvent{{kind: "begin", id: uuid.NewString(), meta: &RequestMeta{ReceivedAt: time.Now()}, queued: 512, createdAt: time.Now()}}
			m.queueBytes.Store(512)
			m.queueEvents.Store(1)
			m.flush(context.Background(), batch)
			require.Zero(t, m.writtenEvents.Load())
			require.EqualValues(t, 1, m.writeFailures.Load())
			require.Greater(t, m.circuitUntil.Load(), time.Now().UnixNano())
			require.Zero(t, m.queueBytes.Load())
			require.Zero(t, m.queueEvents.Load())
		})
	}
}
