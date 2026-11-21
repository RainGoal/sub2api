package requestaudit

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUpstreamPayloadReadCrossesEncryptedChunks(t *testing.T) {
	m, mock := mockManager(t)
	option, err := WithDerivedUpstreamEncryptionKey(strings.Repeat("ab", 32))
	require.NoError(t, err)
	option(m)
	id := uuid.NewString()
	manifest := jsonBytes(PartManifest{ExpectedChunks: 2, ExpectedBytes: 6, Complete: true})
	rows := sqlmock.NewRows([]string{"manifest", "seq", "byte_count", "codec", "payload"})
	for i, raw := range []string{"abc", "def"} {
		event := auditEvent{id: id, attemptNo: 1, part: UpstreamRequestHeaders, seq: i, raw: []byte(raw)}
		encoded, err := m.sealUpstream(event)
		require.NoError(t, err)
		rows.AddRow(manifest, i, len(raw), codecRaw, encoded)
	}
	replica := NewManager(nil)
	replicaOption, err := WithDerivedUpstreamEncryptionKey(strings.Repeat("ab", 32))
	require.NoError(t, err)
	replicaOption(replica)
	shared := []byte("shared key check")
	sharedEvent := auditEvent{id: id, attemptNo: 1, part: UpstreamRequestHeaders,
		seq: 0, raw: shared}
	ciphertext, err := m.sealUpstream(sharedEvent)
	require.NoError(t, err)
	cleartext, err := replica.openUpstream(id, 1, UpstreamRequestHeaders, 0, codecRaw, len(shared), ciphertext)
	require.NoError(t, err)
	require.Equal(t, shared, cleartext)
	mock.ExpectQuery(`SELECT a.parts`).WithArgs(id, 1, UpstreamRequestHeaders).WillReturnRows(rows)
	result, err := m.ReadAttemptPayload(context.Background(), id, 1, UpstreamRequestHeaders, 2, 3, false)
	require.NoError(t, err)
	require.Equal(t, []byte("cde"), result.Bytes)
	require.EqualValues(t, 6, result.TotalBytes)
	require.True(t, result.HasMore)
	require.False(t, result.Partial)
}

func TestUpstreamPayloadEmptyAndExpired(t *testing.T) {
	m, mock := mockManager(t)
	id := uuid.NewString()
	expect := func() *sqlmock.ExpectedQuery {
		return mock.ExpectQuery(`SELECT a.parts`).WithArgs(id, 1, UpstreamRequestBody)
	}
	columns := []string{"manifest", "seq", "byte_count", "codec", "payload"}
	expect().WillReturnRows(sqlmock.NewRows(columns).AddRow(jsonBytes(PartManifest{Complete: true}), nil, nil, nil, nil))
	result, err := m.ReadAttemptPayload(context.Background(), id, 1, UpstreamRequestBody, 0, 1, false)
	require.NoError(t, err)
	require.Empty(t, result.Bytes)
	require.False(t, result.Partial)
	expect().WillReturnRows(sqlmock.NewRows(columns))
	_, err = m.ReadAttemptPayload(context.Background(), id, 1, UpstreamRequestBody, 0, 1, true)
	var auditErr *Error
	require.ErrorAs(t, err, &auditErr)
	require.Equal(t, "AUDIT_NOT_FOUND", auditErr.Code)
}
