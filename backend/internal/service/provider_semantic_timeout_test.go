package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderSemanticTimeoutAccountScope(t *testing.T) {
	cases := []struct {
		name    string
		account *Account
		inScope bool
	}{
		{"nil", nil, false},
		{"openai", &Account{Platform: PlatformOpenAI}, true},
		{"anthropic", &Account{Platform: PlatformAnthropic}, true},
		{"grok", &Account{Platform: PlatformGrok}, false},
		{"kimi_chat_completions", &Account{Platform: PlatformKimi}, false},
		{"gemini", &Account{Platform: PlatformGemini}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.inScope, providerSemanticTimeoutAccount(tc.account))
		})
	}
}

// CN 供应商配成 Anthropic 原生协议时下游响应是 Anthropic 形态，必须纳入范围
// （这类账号的 platform 是 kimi/zhipu/deepseek，只判 PlatformAnthropic 会漏）。
func TestProviderSemanticTimeoutAccountCoversAnthropicProtocolCNAccounts(t *testing.T) {
	account := &Account{
		Platform:    PlatformZhipu,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": APIProtocolAnthropic},
	}
	require.True(t, account.IsAnthropicProtocol(), "前置条件：账号应为 Anthropic 协议")
	require.True(t, providerSemanticTimeoutAccount(account))
}

func TestProviderSemanticTimeoutHitBoundaries(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI}
	cases := []struct {
		name      string
		cacheRead int
		output    int
		hit       bool
	}{
		{"exact", 1000, 1000, true},
		{"cache_read_999", 999, 1000, false},
		{"cache_read_1001", 1001, 1000, false},
		{"output_999", 1000, 999, false},
		{"output_1001", 1000, 1001, false},
		{"zero", 0, 0, false},
		{"no_cache_read", 0, 1000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.hit, providerSemanticTimeoutHit(account, tc.cacheRead, tc.output))
		})
	}
}

func TestProviderSemanticTimeoutHitRespectsScope(t *testing.T) {
	// 范围外账号即使 usage 完全命中也必须放行。
	require.False(t, providerSemanticTimeoutHit(&Account{Platform: PlatformGrok}, 1000, 1000))
	require.False(t, providerSemanticTimeoutHitClaude(&Account{Platform: PlatformGrok},
		&ClaudeUsage{CacheReadInputTokens: 1000, OutputTokens: 1000}))
	require.False(t, providerSemanticTimeoutHitOpenAI(&Account{Platform: PlatformGrok},
		OpenAIUsage{CacheReadInputTokens: 1000, OutputTokens: 1000}))
	// nil usage 指针不得 panic。
	require.False(t, providerSemanticTimeoutHitClaude(&Account{Platform: PlatformOpenAI}, nil))
}

func TestProviderSemanticTimeoutCaptureKeepsHeadAndTail(t *testing.T) {
	capture := &providerSemanticTimeoutCapture{}
	capture.Observe(`{"type":"message_start"}`)
	capture.Observe(`{"type":"content_block_start"}`)
	for i := 0; i < 40; i++ {
		capture.Observe(`{"type":"content_block_delta","i":` + string(rune('0'+i%10)) + `}`)
	}
	capture.Observe(`{"type":"message_delta","usage":{"output_tokens":1000}}`)

	snapshot := capture.Snapshot()
	require.Contains(t, snapshot, "message_start", "首帧必须保留")
	require.Contains(t, snapshot, "content_block_start", "第二帧必须保留")
	require.Contains(t, snapshot, "message_delta", "终局帧必须保留")
	require.Contains(t, snapshot, "frames omitted", "丢弃的中间帧数量必须显式标注")
	require.LessOrEqual(t, len(snapshot), providerSemanticTimeoutCaptureMax)
}

func TestProviderSemanticTimeoutCaptureBounds(t *testing.T) {
	capture := &providerSemanticTimeoutCapture{}
	// 单帧超长必须截断，且不得让快照超过总上限。
	capture.Observe(strings.Repeat("a", 100_000))
	for i := 0; i < 50; i++ {
		capture.Observe(strings.Repeat("b", 100_000))
	}
	snapshot := capture.Snapshot()
	require.LessOrEqual(t, len(snapshot), providerSemanticTimeoutCaptureMax)

	// 空帧与 [DONE] 哨兵不进入快照。
	empty := &providerSemanticTimeoutCapture{}
	empty.Observe("")
	empty.Observe("   ")
	empty.Observe("[DONE]")
	require.Empty(t, empty.Snapshot())

	// nil 接收者安全。
	var nilCapture *providerSemanticTimeoutCapture
	nilCapture.Observe("x")
	require.Empty(t, nilCapture.Snapshot())
}

func TestProviderSemanticTimeoutCaptureShortStreamKeepsEveryFrame(t *testing.T) {
	capture := &providerSemanticTimeoutCapture{}
	capture.Observe("f1")
	capture.Observe("f2")
	capture.Observe("f3")
	require.Equal(t, "f1\nf2\nf3", capture.Snapshot())
}

func TestProviderSemanticTimeoutReportTruncatesUpstreamBody(t *testing.T) {
	report := providerSemanticTimeoutOpenAIReport("route", "model", strings.Repeat("x", 99_999),
		OpenAIUsage{InputTokens: 3, OutputTokens: 1000, CacheReadInputTokens: 1000, CacheCreationInputTokens: 7})
	require.Len(t, report.UpstreamBody, providerSemanticTimeoutCaptureMax)
	require.Equal(t, 3, report.InputTokens)
	require.Equal(t, 1000, report.OutputTokens)
	require.Equal(t, 1000, report.CacheReadInputTokens)
	require.Equal(t, 7, report.CacheCreationInputTokens)

	claudeReport := providerSemanticTimeoutClaudeReport("route", "model", "body",
		&ClaudeUsage{InputTokens: 1, OutputTokens: 1000, CacheReadInputTokens: 1000, CacheCreationInputTokens: 2})
	require.Equal(t, "body", claudeReport.UpstreamBody)
	require.Equal(t, 1, claudeReport.InputTokens)
	require.Equal(t, 2, claudeReport.CacheCreationInputTokens)

	// nil usage 只填路由信息，不 panic。
	nilReport := providerSemanticTimeoutClaudeReport("route", "model", "body", nil)
	require.Equal(t, 0, nilReport.OutputTokens)
}
