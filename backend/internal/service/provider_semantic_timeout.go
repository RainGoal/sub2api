package service

// [provider-semantic-timeout] 上游"假成功"超时响应的窄规则拦截。
//
// 部分 OpenAI 兼容中转与 Anthropic 上游会以 HTTP 200 + 完整协议帧返回一个实际是
// 超时的占位响应，其 usage 恒为 cache_read_input_tokens=1000 且 output_tokens=1000。
// 网关此前把它当成功请求照常计费。本文件用纯 usage 数字判定（不看响应内容），命中
// 后不计费、对下游回 502、并把上游原始内容记进日志与 ops 错误日志供人工确认。
//
// 本文件是该 workaround 的唯一实现处；后期不需要时删除本文件，再按
// `grep -rn "provider-semantic-timeout" backend/` 清掉各落点的 if 块即可。

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// 判定阈值：两项必须同时精确相等。正常响应同时命中这两个整数的概率极低。
	providerSemanticTimeoutCacheReadTokens = 1000
	providerSemanticTimeoutOutputTokens    = 1000

	// providerSemanticTimeoutCode 是稳定标签。自定义 error.type 会被 ops 的
	// normalizeOpsErrorType 归一成 api_error，所以标签同时写进对客 message，
	// 后台错误日志可直接按文本检索。
	providerSemanticTimeoutCode    = "provider_semantic_timeout"
	providerSemanticTimeoutMessage = "Upstream provider timeout (provider_semantic_timeout)"

	// 上游内容捕获上限。故意不接 gateway.log_upstream_error_body 配置：该项默认
	// 关闭会让这个诊断恒为空。若要改成受配置约束，只改这里取值即可。
	providerSemanticTimeoutCaptureHeadFrames = 2
	providerSemanticTimeoutCaptureTailFrames = 12
	providerSemanticTimeoutCaptureFrameMax   = 512
	providerSemanticTimeoutCaptureMax        = 4096
)

// errProviderSemanticTimeout 标记"HTTP 成功但语义失败"的上游响应。调用方必须与
// result=nil 一起返回：partialStreamUsageResult 依赖该 sentinel 跳过部分 usage 计费。
var errProviderSemanticTimeout = errors.New("provider semantic timeout")

// providerSemanticTimeoutAccount 限定生效范围：OpenAI 平台，以及下游响应为
// Anthropic 形态的账号。原生 Anthropic 协议账号使用 CN 平台值，只判
// PlatformAnthropic 会漏掉隔离的 passthrough 路径。
func providerSemanticTimeoutAccount(account *Account) bool {
	if account == nil {
		return false
	}
	return account.Platform == PlatformOpenAI ||
		account.Platform == PlatformAnthropic ||
		account.IsAnthropicProtocol() ||
		account.IsAdaptiveAPIProtocol()
}

// providerSemanticTimeoutHit 是唯一判定入口。
func providerSemanticTimeoutHit(account *Account, cacheReadInputTokens, outputTokens int) bool {
	if !providerSemanticTimeoutAccount(account) {
		return false
	}
	return cacheReadInputTokens == providerSemanticTimeoutCacheReadTokens &&
		outputTokens == providerSemanticTimeoutOutputTokens
}

func providerSemanticTimeoutHitClaude(account *Account, usage *ClaudeUsage) bool {
	if usage == nil {
		return false
	}
	return providerSemanticTimeoutHit(account, usage.CacheReadInputTokens, usage.OutputTokens)
}

func providerSemanticTimeoutHitOpenAI(account *Account, usage OpenAIUsage) bool {
	return providerSemanticTimeoutHit(account, usage.CacheReadInputTokens, usage.OutputTokens)
}

// providerSemanticTimeoutCapture 为流式响应保留有界的上游内容样本：前若干帧覆盖
// message_start / 首个 chunk，尾部环形覆盖终局事件与可能的错误文案。每帧一次小
// 拷贝，未命中的请求不会付出拼接开销（Snapshot 只在命中时调用）。
type providerSemanticTimeoutCapture struct {
	head      [providerSemanticTimeoutCaptureHeadFrames]string
	headCount int
	tail      [providerSemanticTimeoutCaptureTailFrames]string
	tailNext  int
	tailCount int
	dropped   int
}

func (p *providerSemanticTimeoutCapture) Observe(payload string) {
	if p == nil {
		return
	}
	frame := strings.TrimSpace(payload)
	if frame == "" || frame == "[DONE]" {
		return
	}
	if len(frame) > providerSemanticTimeoutCaptureFrameMax {
		// Clone 避免子串继续持有整行的底层数组（单行可能很大）。
		frame = strings.Clone(truncateString(frame, providerSemanticTimeoutCaptureFrameMax))
	}
	if p.headCount < len(p.head) {
		p.head[p.headCount] = frame
		p.headCount++
		return
	}
	if p.tailCount == len(p.tail) {
		p.dropped++
	} else {
		p.tailCount++
	}
	p.tail[p.tailNext] = frame
	p.tailNext = (p.tailNext + 1) % len(p.tail)
}

// Snapshot 按观测顺序拼出样本，中间被丢弃的帧数显式标注。
func (p *providerSemanticTimeoutCapture) Snapshot() string {
	if p == nil {
		return ""
	}
	frames := make([]string, 0, p.headCount+p.tailCount+1)
	frames = append(frames, p.head[:p.headCount]...)
	if p.dropped > 0 {
		frames = append(frames, "...("+strconv.Itoa(p.dropped)+" frames omitted)...")
	}
	start := 0
	if p.tailCount == len(p.tail) {
		start = p.tailNext
	}
	for i := 0; i < p.tailCount; i++ {
		frames = append(frames, p.tail[(start+i)%len(p.tail)])
	}
	return truncateString(strings.Join(frames, "\n"), providerSemanticTimeoutCaptureMax)
}

// providerSemanticTimeoutReport 汇总一次命中的全部特征，用于日志与 ops 记录。
type providerSemanticTimeoutReport struct {
	Route                    string
	Model                    string
	UpstreamBody             string
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

func providerSemanticTimeoutClaudeReport(route, model, upstreamBody string, usage *ClaudeUsage) providerSemanticTimeoutReport {
	report := providerSemanticTimeoutReport{
		Route:        route,
		Model:        model,
		UpstreamBody: truncateString(upstreamBody, providerSemanticTimeoutCaptureMax),
	}
	if usage != nil {
		report.InputTokens = usage.InputTokens
		report.OutputTokens = usage.OutputTokens
		report.CacheReadInputTokens = usage.CacheReadInputTokens
		report.CacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	return report
}

func providerSemanticTimeoutOpenAIReport(route, model, upstreamBody string, usage OpenAIUsage) providerSemanticTimeoutReport {
	return providerSemanticTimeoutReport{
		Route:                    route,
		Model:                    model,
		UpstreamBody:             truncateString(upstreamBody, providerSemanticTimeoutCaptureMax),
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
}

// rejectProviderSemanticTimeout 是命中后的统一动作：
//  1. WARN 日志带全部特征字段与上游原始内容；
//  2. SetOpsUpstreamError 把内容写进 ops 行的 upstream_error_detail（后台详情可见）；
//  3. MarkOpsStreamFailure 保证 wire 已固化为 200 时该失败仍计入后台与 SLA；
//  4. 未写出过响应则按协议回 502，已开始输出则补一帧带内错误；
//  5. 返回 sentinel —— 调用方必须同时返回 result=nil，否则仍会计费。
func rejectProviderSemanticTimeout(
	c *gin.Context,
	account *Account,
	report providerSemanticTimeoutReport,
	writeFresh func(),
	writeInBand func(),
) error {
	outputStarted := c != nil && c.Writer != nil && c.Writer.Written()

	fields := []zap.Field{
		zap.String("route", report.Route),
		zap.String("model", report.Model),
		zap.Int("input_tokens", report.InputTokens),
		zap.Int("output_tokens", report.OutputTokens),
		zap.Int("cache_read_input_tokens", report.CacheReadInputTokens),
		zap.Int("cache_creation_input_tokens", report.CacheCreationInputTokens),
		zap.Bool("client_output_started", outputStarted),
		zap.String("upstream_body", report.UpstreamBody),
	}
	if account != nil {
		fields = append(fields,
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("platform", account.Platform),
		)
	}
	if c != nil && c.Writer != nil {
		if requestID := c.Writer.Header().Get("X-Request-Id"); requestID != "" {
			fields = append(fields, zap.String("request_id", requestID))
		}
	}
	logger.L().Warn("gateway.provider_semantic_timeout", fields...)

	if c == nil || c.Writer == nil {
		return errProviderSemanticTimeout
	}

	SetOpsUpstreamError(c, http.StatusOK, providerSemanticTimeoutCode, report.UpstreamBody)
	MarkOpsStreamFailure(c, "upstream_error", providerSemanticTimeoutCode, providerSemanticTimeoutMessage, http.StatusBadGateway)

	if !outputStarted {
		// 流式路径在读上游前就把 Content-Type 预置成 text/event-stream，这里回的是
		// JSON 错误体，必须显式改回来。
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		if writeFresh != nil {
			writeFresh()
		}
	} else if writeInBand != nil {
		writeInBand()
	}
	MarkResponseCommitted(c)
	return errProviderSemanticTimeout
}

// logProviderSemanticTimeoutBillingSkip 记录计费层兜底拦截。该层拿不到
// gin.Context 与响应体，只保证"任何路径都不会为这种响应扣费"。
func logProviderSemanticTimeoutBillingSkip(account *Account, model, requestID string, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int) {
	fields := []zap.Field{
		zap.String("route", "billing.guard"),
		zap.String("model", model),
		zap.String("request_id", requestID),
		zap.Int("input_tokens", inputTokens),
		zap.Int("output_tokens", outputTokens),
		zap.Int("cache_read_input_tokens", cacheReadTokens),
		zap.Int("cache_creation_input_tokens", cacheCreationTokens),
	}
	if account != nil {
		fields = append(fields,
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("platform", account.Platform),
		)
	}
	logger.L().Warn("gateway.provider_semantic_timeout_billing_skipped", fields...)
}

// rejectProviderSemanticTimeoutWithWriter 用于自身已能区分"回 JSON 还是补带内
// SSE"的写出器（如 writeOpenAINonStreamingProtocolError 会在 compact 心跳已提交
// 200 时改写 response.failed 终止事件）：两种情形都交给它，不在此处二次分支。
func rejectProviderSemanticTimeoutWithWriter(
	c *gin.Context,
	account *Account,
	report providerSemanticTimeoutReport,
	write func(),
) error {
	return rejectProviderSemanticTimeout(c, account, report, write, write)
}

// rejectAnthropicSemanticTimeout 服务 Anthropic Messages 形态的下游。
func rejectAnthropicSemanticTimeout(c *gin.Context, account *Account, report providerSemanticTimeoutReport) error {
	return rejectProviderSemanticTimeout(c, account, report,
		func() {
			writeAnthropicError(c, http.StatusBadGateway, "api_error", providerSemanticTimeoutMessage)
		},
		func() {
			_, _ = c.Writer.WriteString(buildAnthropicStreamErrorSSE("api_error", providerSemanticTimeoutMessage))
			c.Writer.Flush()
		},
	)
}

// rejectChatCompletionsSemanticTimeout 服务 Chat Completions 形态的下游。
func rejectChatCompletionsSemanticTimeout(c *gin.Context, account *Account, report providerSemanticTimeoutReport) error {
	return rejectProviderSemanticTimeout(c, account, report,
		func() {
			writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", providerSemanticTimeoutMessage)
		},
		func() {
			_, _ = c.Writer.WriteString(buildChatStreamErrorSSE(providerSemanticTimeoutCode, providerSemanticTimeoutMessage))
			_, _ = c.Writer.WriteString("data: [DONE]\n\n")
			c.Writer.Flush()
		},
	)
}

// rejectResponsesSemanticTimeout 服务 Responses 形态的下游。带内错误用
// response.failed 终止事件：Codex 只把它当合法终止，普通 error 帧会退化成盲重连。
func rejectResponsesSemanticTimeout(c *gin.Context, account *Account, report providerSemanticTimeoutReport) error {
	return rejectProviderSemanticTimeout(c, account, report,
		func() {
			writeResponsesError(c, http.StatusBadGateway, providerSemanticTimeoutCode, providerSemanticTimeoutMessage)
		},
		func() {
			writeOpenAICompactSSEFailureMessage(c, http.StatusBadGateway, "upstream_error", providerSemanticTimeoutMessage)
		},
	)
}
