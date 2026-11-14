package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type collaborationStagedWSConn struct{ *stagedPassthroughConn }

func (c *collaborationStagedWSConn) WriteJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

func TestCollaborationShutdownNativeWSKeepsLaterPlaintextReadable(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		for _, keyID := range []int64{11, 12} {
			for _, plainFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/key=%d/plain_first=%v", mode, keyID, plainFirst), func(t *testing.T) {
					cfg := passthroughLifecycleConfig()
					cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
					cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
					cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
					cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
					upstream := newStagedPassthroughConn()
					defer func() { _ = upstream.Close() }()
					svc := newPassthroughLifecycleService(cfg, upstream)
					dialer := &stagedPassthroughDialer{conn: &collaborationStagedWSConn{upstream}}
					svc.openaiWSPassthroughDialer = dialer
					svc.openaiWSPool = newOpenAIWSConnPool(cfg)
					svc.openaiWSPool.setClientDialerForTest(dialer)
					svc.settingService = NewSettingService(&panelRateLimitSettingRepo{}, cfg)
					settings := enabledCollaborationTestSettings()
					settings.Enabled = false
					require.NoError(t, svc.settingService.SetOpenAICollaborationSettings(context.Background(), settings))
					account := passthroughLifecycleAccount()
					account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
						groupID := int64(7)
						c.Set("api_key", &APIKey{ID: keyID, UserID: 9, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformOpenAI}})
						return nil
					})
					defer server.Close()
					plainFrame := `{"type":"response.create","model":"gpt-5.1","tools":` + collaborationPlaintextTools + `,"input":[{"type":"agent_message","id":"amsg_plain","content":[{"type":"input_text","text":"Complete CURRENT_TASK"}]},{"type":"agent_message","id":"amsg_opaque","content":[{"type":"input_text","text":"Prefix"},{"type":"encrypted_content","encrypted_content":"KEEP_CIPHER"}]}]}`
					firstFrame := `{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"hello"}]}`
					if plainFirst {
						firstFrame = plainFrame
					}
					client := dialPassthroughLifecycleClientWithPayload(t, server, firstFrame)
					defer func() { _ = client.CloseNow() }()
					for turn := 1; turn <= 2; turn++ {
						if turn > 1 {
							require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(plainFrame)))
						}
						forwarded := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
						if turn > 1 || plainFirst {
							wantKind := "agent_message"
							if keyID == 11 {
								wantKind = "message"
								require.Equal(t, "user", gjson.GetBytes(forwarded, "input.0.role").String())
							}
							require.Equal(t, wantKind, gjson.GetBytes(forwarded, "input.0.type").String())
							require.Contains(t, gjson.GetBytes(forwarded, "input.0.content").Raw, "CURRENT_TASK")
							require.JSONEq(t, gjson.Get(plainFrame, "input.1").Raw, gjson.GetBytes(forwarded, "input.1").Raw)
							require.JSONEq(t, collaborationPlaintextTools, gjson.GetBytes(forwarded, "tools").Raw)
						}
						upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_collab_%d","model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
						completed, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
						require.NoError(t, err)
						require.Equal(t, "response.completed", gjson.GetBytes(completed, "type").String())
					}
					_ = client.Close(coderws.StatusNormalClosure, "done")
					select {
					case err := <-serverErr:
						if err != nil {
							require.Contains(t, err.Error(), "StatusNormalClosure")
						}
					case <-ctx.Done():
						t.Fatal("native websocket did not finish")
					}
				})
			}
		}
	}
}
