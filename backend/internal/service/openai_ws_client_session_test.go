package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type wsClientSessionHarness struct {
	t        *testing.T
	upstream *stagedPassthroughConn
	client   *coderws.Conn
	turns    <-chan *OpenAIForwardResult
}

func newWSClientSessionHarness(t *testing.T) *wsClientSessionHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 10
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 10
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 10
	turns := make(chan *OpenAIForwardResult, 16)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, newPassthroughLifecycleService(cfg, upstream), passthroughLifecycleAccount(), func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{
			InitialRequestModel: "public-first",
			MapRequestModel:     func(_ int, model string) (string, error) { return "mapped-" + model, nil },
			AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
				if result != nil && err == nil {
					turns <- result
				}
			},
		}
	})
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"channel-first"}`)
	t.Cleanup(func() {
		cancel()
		_ = client.CloseNow()
		select {
		case <-serverErr:
		case <-time.After(3 * time.Second):
			t.Error("session regression relay did not stop")
		}
		server.Close()
	})
	h := &wsClientSessionHarness{t: t, upstream: upstream, client: client, turns: turns}
	first := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	require.Equal(t, "mapped-public-first", gjson.GetBytes(first, "model").String())
	h.complete("resp_first", "public-first", "public-first")
	return h
}

func (h *wsClientSessionHarness) submit(payload string) []byte {
	h.t.Helper()
	writeWSClientModelPrivacyFrame(h.t, h.client, coderws.MessageText, payload)
	// Receiving the upstream write before any ACK also verifies no ACK wait
	// has been added to the normal forwarding path.
	return requirePassthroughUpstreamWrite(h.t, h.upstream, 3*time.Second)
}

func (h *wsClientSessionHarness) emit(payload string) []byte {
	h.t.Helper()
	h.upstream.Send(payload)
	return readWSClientModelPrivacyFrame(h.t, h.client, coderws.MessageText)
}

func (h *wsClientSessionHarness) create(payload, wantRoutingModel string) {
	h.t.Helper()
	out := h.submit(payload)
	require.Equal(h.t, "mapped-"+wantRoutingModel, gjson.GetBytes(out, "model").String())
}

func (h *wsClientSessionHarness) complete(id, publicModel, billingModel string) {
	h.t.Helper()
	frame := h.emit(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"private-observed","usage":{"input_tokens":17,"output_tokens":5}}}`, id))
	for {
		select {
		case result := <-h.turns:
			if result.Usage.InputTokens == 0 && result.Usage.OutputTokens == 0 {
				// The existing relay may settle a bare control error separately.
				// Keep that behavior and verify it has no generated usage here.
				require.Equal(h.t, OpenAIUsage{}, result.Usage)
				require.Equal(h.t, billingModel, result.Model)
				require.Equal(h.t, "mapped-"+billingModel, result.UpstreamModel)
				continue
			}
			require.Equal(h.t, id, result.RequestID)
			require.Equal(h.t, billingModel, result.Model)
			require.Equal(h.t, "mapped-"+billingModel, result.UpstreamModel)
			require.Equal(h.t, "private-observed", result.UpstreamResponseModel)
			require.Equal(h.t, 17, result.Usage.InputTokens)
			require.Equal(h.t, 5, result.Usage.OutputTokens)
			require.Equal(h.t, publicModel, gjson.GetBytes(frame, "response.model").String())
			return
		case <-time.After(3 * time.Second):
			h.t.Fatal("session regression response was not settled")
		}
	}
}

func TestPassthroughClientModelPrivacy_RejectedSessionDefaults(t *testing.T) {
	for _, tc := range []struct{ name, update, rejection string }{
		{"correlated", `{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`, `{"type":"error","error":{"type":"invalid_request_error","event_id":"set_two","message":"private-model rejected"}}`},
		{"unambiguous_without_id", `{"type":"session.update","session":{"model":"public-second"}}`, `{"type":"error","error":{"type":"invalid_request_error","message":"private-model rejected"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newWSClientSessionHarness(t)
			h.submit(tc.update)
			rejection := h.emit(tc.rejection)
			require.NotContains(t, string(rejection), "private-model")
			h.create(`{"type":"response.create"}`, "public-second")
			h.complete("resp_after_reject", "public-first", "public-second")
		})
	}
}

func (h *wsClientSessionHarness) created(id, publicModel string) {
	h.t.Helper()
	frame := h.emit(fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"private-observed"}}`, id))
	require.Equal(h.t, publicModel, gjson.GetBytes(frame, "response.model").String())
}

func (h *wsClientSessionHarness) acknowledge(publicModel string) {
	h.t.Helper()
	frame := h.emit(`{"type":"session.updated","session":{"model":"private-session"}}`)
	require.Equal(h.t, publicModel, gjson.GetBytes(frame, "session.model").String())
}

func TestPassthroughClientModelPrivacy_SessionConfirmationOrder(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_config","session":{"instructions":"keep this text"}}`)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.acknowledge("public-first")
	h.acknowledge("public-second")
	h.acknowledge("public-third")
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_after_acks", "public-third", "public-third")
}

func TestPassthroughClientModelPrivacy_SessionConfirmedFallback(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.acknowledge("public-second")
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_three","message":"update rejected"}}`)
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_confirmed_fallback", "public-second", "public-third")
}

func TestPassthroughClientModelPrivacy_RejectedOlderSessionCannotReplaceNewer(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_two","message":"update rejected"}}`)
	h.acknowledge("public-third")
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_newer_session", "public-third", "public-third")
}

func TestPassthroughClientModelPrivacy_PipelinedSessionSnapshots(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.create(`{"type":"response.create"}`, "public-second")
	h.created("resp_pipeline", "public-second")
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.acknowledge("public-second")
	h.acknowledge("public-third")
	h.complete("resp_pipeline", "public-second", "public-second")
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_after_pipeline", "public-third", "public-third")
}

func TestPassthroughClientModelPrivacy_LateSessionResolutionKeepsVisibleTurn(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_without_ack", "public-second", "public-second")
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.create(`{"type":"response.create"}`, "public-third")
	h.created("resp_visible", "public-third")
	h.acknowledge("public-second") // ACK belongs to the earlier version.
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_three","message":"update rejected"}}`)
	h.complete("resp_visible", "public-third", "public-third")
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_after_late_reject", "public-second", "public-third")
}

func TestPassthroughClientModelPrivacy_ExplicitTurnDoesNotReplaceSessionDefault(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_two","message":"update rejected"}}`)
	h.create(`{"type":"response.create","model":"public-explicit"}`, "public-explicit")
	h.complete("resp_explicit", "public-explicit", "public-explicit")
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_after_explicit", "public-first", "public-second")
}

func TestPassthroughClientModelPrivacy_ActiveErrorCannotRejectSessionUpdate(t *testing.T) {
	for _, rejection := range []string{
		`{"type":"error","error":{"type":"invalid_request_error","message":"response rejected"}}`,
		`{"type":"error","error":{"type":"invalid_request_error","event_id":"response_event","message":"response rejected"}}`,
	} {
		t.Run(rejection, func(t *testing.T) {
			h := newWSClientSessionHarness(t)
			h.create(`{"type":"response.create","event_id":"response_event","model":"public-active"}`, "public-active")
			h.created("resp_active", "public-active")
			h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
			h.emit(rejection)
			h.complete("resp_active", "public-active", "public-active")
			h.create(`{"type":"response.create"}`, "public-second")
			h.complete("resp_after_active_error", "public-second", "public-second")
		})
	}
}

func TestPassthroughClientModelPrivacy_AmbiguousSessionErrorKeepsDefault(t *testing.T) {
	for _, tc := range []struct{ name, extra, rejection, model string }{
		{"unknown_event", "", `{"type":"error","error":{"event_id":"unrelated","message":"rejected"}}`, "public-second"},
		{"response_id", "", `{"type":"error","response_id":"resp_first","error":{"event_id":"set_two","message":"rejected"}}`, "public-second"},
		{"other_parameter", "", `{"type":"error","error":{"param":"input","message":"rejected"}}`, "public-second"},
		{"other_control", `{"type":"conversation.item.create","item":{"type":"message","role":"user","content":[]}}`, `{"type":"error","error":{"message":"rejected"}}`, "public-second"},
		{"multiple_pending", `{"type":"session.update","session":{"model":"public-third"}}`, `{"type":"error","error":{"message":"rejected"}}`, "public-third"},
		{"duplicate_event_id", `{"type":"session.update","event_id":"set_two","session":{"model":"public-third"}}`, `{"type":"error","error":{"event_id":"set_two","message":"rejected"}}`, "public-third"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newWSClientSessionHarness(t)
			h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
			if tc.extra != "" {
				h.submit(tc.extra)
			}
			h.emit(tc.rejection)
			h.create(`{"type":"response.create"}`, tc.model)
			h.complete("resp_ambiguous", tc.model, tc.model)
		})
	}
}

func TestPassthroughClientModelPrivacy_DelayedUncorrelatedErrorKeepsDefault(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","session":{"model":"public-second"}}`)
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_before_unrelated_error", "public-second", "public-second")
	// A response intervened, so this uncorrelated error could belong to it.
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","message":"response rejected"}}`)
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_after_unrelated_error", "public-second", "public-second")
}

func TestPassthroughClientModelPrivacy_PipelinedRejectionBeforeOutputKeepsSnapshot(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.create(`{"type":"response.create"}`, "public-second")
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_two","message":"update rejected"}}`)
	h.created("resp_submitted", "public-second")
	h.complete("resp_submitted", "public-second", "public-second")
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_after_rejection", "public-first", "public-second")
}

func TestPassthroughClientModelPrivacy_BinarySessionConfirmation(t *testing.T) {
	h := newWSClientSessionHarness(t)
	writeWSClientModelPrivacyFrame(t, h.client, coderws.MessageBinary, `{"type":"session.update","session":{"model":"public-second"}}`)
	_ = requirePassthroughUpstreamWrite(t, h.upstream, 3*time.Second)
	h.upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: []byte(`{"type":"session.updated","session":{"model":"private-session"}}`)}
	ack := readWSClientModelPrivacyFrame(t, h.client, coderws.MessageBinary)
	require.Equal(t, "public-second", gjson.GetBytes(ack, "session.model").String())
	h.create(`{"type":"response.create"}`, "public-second")
	h.complete("resp_binary_session", "public-second", "public-second")
}

func TestPassthroughClientModelPrivacy_LateUncorrelatedErrorCannotRejectOlderPending(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","event_id":"set_three","message":"update rejected"}}`)
	// U3 may report another error without its event ID. U2 is the sole pending
	// update again, but the intervening update makes attribution ambiguous.
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","message":"update rejected"}}`)
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_after_duplicate_error", "public-second", "public-third")
}

func TestPassthroughClientModelPrivacy_UncorrelatedLatestUpdateAfterPriorAck(t *testing.T) {
	h := newWSClientSessionHarness(t)
	h.submit(`{"type":"session.update","event_id":"set_two","session":{"model":"public-second"}}`)
	h.submit(`{"type":"session.update","event_id":"set_three","session":{"model":"public-third"}}`)
	h.acknowledge("public-second")
	h.emit(`{"type":"error","error":{"type":"invalid_request_error","message":"update rejected"}}`)
	h.create(`{"type":"response.create"}`, "public-third")
	h.complete("resp_latest_rejected", "public-second", "public-third")
}
