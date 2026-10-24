//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/iotest"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func TestVideoTaskIdentityRandomFailurePreventsPublicID(t *testing.T) {
	id, err := newSeedanceVideoPublicTaskID(bytes.NewReader(make([]byte, 16)))
	require.NoError(t, err)
	require.Equal(t, "vid_00000000000000000000000000000000", id)
	for _, source := range []struct {
		name string
		err  error
	}{{"entropy unavailable", errors.New("entropy unavailable")}} {
		t.Run(source.name, func(t *testing.T) {
			id, err := newSeedanceVideoPublicTaskID(iotest.ErrReader(source.err))
			require.Error(t, err)
			require.Empty(t, id)
		})
	}
	_, err = newSeedanceVideoPublicTaskID(bytes.NewReader(make([]byte, 15)))
	require.Error(t, err)
}

func TestVideoTaskPublicIdentityKeepsLegacyAndSeparatesSupplierID(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{StateID: "internal-state", TaskID: "supplier-42"}
	require.Equal(t, "supplier-42", pending.PublicID())
	pending.IDVersion = SeedanceVideoTaskIdentityVersion
	pending.StateID = "vid_00112233445566778899aabbccddeeff"
	require.Equal(t, pending.StateID, pending.PublicID())
	require.Equal(t, "supplier-42", pending.UpstreamID())
	require.NoError(t, ValidateSeedanceVideoTaskIdentity(pending))
	pending.IDVersion = 2
	require.Error(t, ValidateSeedanceVideoTaskIdentity(pending))
}

func TestVideoTaskExecutionRestoresOriginalRouteWithoutMutatingAccount(t *testing.T) {
	capability, ok := videoprovider.LookupVideoModelCapability("omni-fast", nil)
	require.True(t, ok)
	pending := &SeedanceVideoPendingBilling{AccountID: 20, ExecutionSnapshot: &VideoTasksExecutionSnapshot{
		Version: 1, Protocol: "video_tasks_json_v1", BaseURL: "https://original.example/v1",
		UpstreamModel: "omni-fast", FieldProfile: string(capability.Profile), Capability: &capability,
	}}
	account := &Account{ID: 20, Credentials: map[string]any{"api_key": "original-secret", "base_url": "https://changed.example/v1"}}
	frozen, err := AccountForSeedanceVideoTask(pending, account)
	require.NoError(t, err)
	require.Equal(t, "https://original.example/v1", frozen.Credentials["base_url"])
	require.Equal(t, "original-secret", frozen.Credentials["api_key"])
	require.Equal(t, "https://changed.example/v1", account.Credentials["base_url"])
	require.Nil(t, account.Extra)
	pending.ExecutionSnapshot.BaseURL = "https://secret@example.com/v1"
	_, err = AccountForSeedanceVideoTask(pending, account)
	require.Error(t, err)
}

type identityStudioReader struct{ task *SeedanceVideoPendingBilling }

func TestVideoTaskExecutionRestoresLegacyPublicMapping(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{AccountID: 20, Model: "studio-video",
		ExecutionSnapshot: &VideoTasksExecutionSnapshot{Version: 1, Protocol: "bblabu_v1",
			BaseURL: "https://original.example/v1", UpstreamModel: "Seedance-2.0"}}
	account := mappedLegacyVideoAccount("fflink_v1", "seedance-2.5")
	account.ID = pending.AccountID
	frozen, err := AccountForSeedanceVideoTask(pending, account)
	require.NoError(t, err)
	upstream, err := frozen.ResolveSeedanceUpstreamModel(pending.Model)
	require.NoError(t, err)
	require.Equal(t, "Seedance-2.0", upstream)
	require.Equal(t, "seedance-2.5", account.GetModelMapping()[pending.Model])
	require.Equal(t, videoprovider.ProviderBBLabuV1, frozen.GetVideoProviderID())
}

func (r identityStudioReader) GetByProviderTask(context.Context, string, int64, int64) (*SeedanceVideoPendingBilling, error) {
	return r.task, nil
}

func TestStudioVideoReaderRejectsNewTaskSupplierIDAlias(t *testing.T) {
	pending := &SeedanceVideoPendingBilling{IDVersion: 1, StateID: "vid_00112233445566778899aabbccddeeff", TaskID: "supplier-42"}
	_, err := readStudioVideo(context.Background(), identityStudioReader{pending}, pending.TaskID, 7, 8)
	require.ErrorIs(t, err, ErrSeedanceVideoTaskNotFound)
	_, err = readStudioVideo(context.Background(), identityStudioReader{pending}, pending.StateID, 7, 8)
	require.NoError(t, err)
}
