package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ChannelMonitorProbeHeaderName     = "X-Sub2API-Monitor-Probe"
	channelMonitorProbeVersion        = "v1"
	channelMonitorProbeMaxAge         = 2 * time.Minute
	channelMonitorProbeAttemptTimeout = 10 * time.Second
)

type channelMonitorProbeContextKey struct{}

// BuildChannelMonitorProbeHeader creates a short-lived marker bound to the API key.
// The marker does not grant API-key access; it only identifies an authenticated
// internal probe for monitor-specific routing and timeout/failover behavior.
func BuildChannelMonitorProbeHeader(apiKey string, now time.Time) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := channelMonitorProbeSignature(apiKey, timestamp)
	return channelMonitorProbeVersion + "." + timestamp + "." + hex.EncodeToString(signature)
}

func VerifyChannelMonitorProbeHeader(value, apiKey string, now time.Time) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 || parts[0] != channelMonitorProbeVersion {
		return false
	}
	timestamp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Sub(time.Unix(timestamp, 0)) > channelMonitorProbeMaxAge ||
		time.Unix(timestamp, 0).Sub(now) > channelMonitorProbeMaxAge {
		return false
	}
	expected := channelMonitorProbeSignature(strings.TrimSpace(apiKey), parts[1])
	actual, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	return hmac.Equal(actual, expected)
}

func channelMonitorProbeSignature(apiKey, timestamp string) []byte {
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = mac.Write([]byte(channelMonitorProbeVersion + "." + timestamp))
	return mac.Sum(nil)
}

func WithChannelMonitorProbe(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, channelMonitorProbeContextKey{}, true)
}

func IsChannelMonitorProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(channelMonitorProbeContextKey{}).(bool)
	return marked
}

// ChannelMonitorProbeAttemptContext bounds one selected account. The caller's
// context remains the total group-probe budget so failover can select another account.
func ChannelMonitorProbeAttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if !IsChannelMonitorProbe(ctx) {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, channelMonitorProbeAttemptTimeout)
}

func NewChannelMonitorProbeTimeoutFailoverError(c *gin.Context, resp *http.Response, err error) *UpstreamFailoverError {
	if !IsChannelMonitorProbeContext(c) || !isChannelMonitorProbeTimeout(err) {
		return nil
	}
	var headers http.Header
	if resp != nil {
		headers = resp.Header.Clone()
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	return &UpstreamFailoverError{
		StatusCode: http.StatusGatewayTimeout, ResponseHeaders: headers,
		ResponseBody:             []byte(`{"error":{"type":"monitor_probe_timeout","message":"Upstream produced no response before the monitor deadline"}}`),
		SafeToFailoverAfterWrite: true,
	}
}

func IsChannelMonitorProbeContext(c *gin.Context) bool {
	return c != nil && c.Request != nil && IsChannelMonitorProbe(c.Request.Context())
}

func isChannelMonitorProbeTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}
