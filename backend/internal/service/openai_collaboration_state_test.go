package service

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func changeCollaborationTestGroup(c *gin.Context, groupID int64) {
	key := *getAPIKeyFromContext(c)
	key.GroupID = &groupID
	key.Group = &Group{ID: groupID, Platform: PlatformOpenAI}
	c.Set("api_key", &key)
}

func TestCollaborationAttemptGroupSwitchKeepsBudgetWithoutContextLeak(t *testing.T) {
	ctx := context.Background()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	settings := enabledCollaborationTestSettings()
	settings.GroupIDs = []int64{7, 8}
	require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(ctx, settings))
	c := newCollaborationTestContext(body, 11)
	account := newOpenAIRejectedFieldTestAccount()
	first := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	require.NotNil(t, first)
	first.aliases["old_alias"] = openAICollaborationTool{Name: "spawn_agent"}
	first.knownPlaintext = map[string]bool{"old_proof": true}
	first.knownOmissions = map[string]bool{"old_omission": true}
	first.omissions = []openAICollaborationOmission{{MessageID: "old_message"}}
	first.recoveryUsed, first.stopFailover, first.completed = true, true, true
	noteCollaborationUpstreamAttempt(c)
	changeCollaborationTestGroup(c, 8)
	second := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	require.NotNil(t, second)
	require.NotSame(t, first, second)
	require.NotEqual(t, first.scope, second.scope)
	require.True(t, second.policy.Enabled)
	require.True(t, second.recoveryUsed)
	require.True(t, second.stopFailover)
	require.False(t, second.completed)
	require.Empty(t, second.aliases)
	require.Empty(t, second.knownPlaintext)
	require.Empty(t, second.knownOmissions)
	require.Empty(t, second.omissions)
	require.Equal(t, 1, collaborationPriorUpstreamAttempts(c))
	noteCollaborationUpstreamAttempt(c)
	require.Equal(t, 2, collaborationPriorUpstreamAttempts(c))
}

func TestCollaborationAttemptUnconfiguredGroupCannotTransformAndKeepsBudget(t *testing.T) {
	ctx := context.Background()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	account := newOpenAIRejectedFieldTestAccount()
	first := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	first.recoveryUsed = true
	noteCollaborationUpstreamAttempt(c)
	changeCollaborationTestGroup(c, 8)
	require.Nil(t, svc.beginCollaborationAttempt(ctx, c, account, body, false))
	require.Nil(t, collaborationAttempt(c))
	unchanged, err := prepareOpenAICollaborationBody(body, collaborationAttempt(c))
	require.NoError(t, err)
	require.Equal(t, body, unchanged)
	noteCollaborationUpstreamAttempt(c)
	changeCollaborationTestGroup(c, 7)
	resumed := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	require.NotNil(t, resumed)
	require.True(t, resumed.recoveryUsed)
	require.Equal(t, 2, collaborationPriorUpstreamAttempts(c))
}

func TestCollaborationAttemptMissingSessionStillSeparatesGroups(t *testing.T) {
	ctx := context.Background()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	c.Request.Header.Del("session-id")
	c.Request.Header.Del("thread-id")
	account := newOpenAIRejectedFieldTestAccount()
	first := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	require.NotNil(t, first)
	require.Empty(t, first.scope)
	changeCollaborationTestGroup(c, 8)
	require.Nil(t, svc.beginCollaborationAttempt(ctx, c, account, body, false))
}

func TestCollaborationAttemptSameScopeRetainsBudgetAndNewTurnResetsIt(t *testing.T) {
	ctx := context.Background()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	account := newOpenAIRejectedFieldTestAccount()
	first := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	first.recoveryUsed, first.stopFailover = true, true
	noteCollaborationUpstreamAttempt(c)
	require.Same(t, first, svc.beginCollaborationAttempt(ctx, c, account, body, false))
	nextTurn := svc.beginCollaborationAttempt(ctx, c, account, body, true)
	require.NotSame(t, first, nextTurn)
	require.False(t, nextTurn.recoveryUsed)
	require.False(t, nextTurn.stopFailover)
	require.Equal(t, 0, collaborationPriorUpstreamAttempts(c))
}

func TestCollaborationAttemptUnconfiguredSendCountsWithoutAdapter(t *testing.T) {
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	changeCollaborationTestGroup(c, 8)
	require.Nil(t, svc.beginCollaborationAttempt(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body, false))
	noteCollaborationUpstreamAttempt(c)
	require.Nil(t, collaborationAttempt(c))
	require.Equal(t, 1, collaborationPriorUpstreamAttempts(c))
	changeCollaborationTestGroup(c, 7)
	configured := svc.beginCollaborationAttempt(context.Background(), c, newOpenAIRejectedFieldTestAccount(), body, false)
	require.NotNil(t, configured)
	require.Equal(t, 1, configured.upstreamAttempts)
	noteCollaborationUpstreamAttempt(c)
	require.Equal(t, 2, collaborationPriorUpstreamAttempts(c))
}

func TestCollaborationOmissionsRequireBranchAndCompletedTurn(t *testing.T) {
	for _, branch := range []string{"", "child-thread"} {
		for _, success := range []bool{false, true} {
			ctx := context.Background()
			body := []byte(collaborationMixedRequest)
			svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
			c := newCollaborationTestContext(body, 11)
			c.Request.Header.Set("thread-id", branch)
			a := svc.beginCollaborationAttempt(ctx, c, newOpenAIRejectedFieldTestAccount(), body, false)
			a.completed = success
			a.omissions = []openAICollaborationOmission{{MessageID: "amsg_old", Digest: "digest"}}
			a.finish(true)
			entry := svc.collaborationCache.read(a.scope + ":" + branch)
			require.Equal(t, branch != "" && success, entry.omissions[collaborationOmissionKey("amsg_old", "digest")])
		}
	}
}

func TestCollaborationAttemptWithoutBranchIgnoresHistoricalOmissions(t *testing.T) {
	ctx := context.Background()
	body := []byte(collaborationMixedRequest)
	svc := configureCollaborationTestService(t, &httpUpstreamRecorder{}, true)
	c := newCollaborationTestContext(body, 11)
	c.Request.Header.Del("thread-id")
	account := newOpenAIRejectedFieldTestAccount()
	first := svc.beginCollaborationAttempt(ctx, c, account, body, false)
	require.NotEmpty(t, first.scope)
	svc.collaborationCache.update(first.scope+":", func(entry *openAICollaborationCacheEntry) {
		entry.omissions[collaborationOmissionKey("amsg_old", collaborationHash("old-opaque"))] = true
	})
	second := svc.beginCollaborationAttempt(ctx, c, account, body, true)
	require.Empty(t, second.knownOmissions)
	unchanged, err := applyRememberedCollaborationOmissions(body, second)
	require.NoError(t, err)
	require.Equal(t, body, unchanged)
}
