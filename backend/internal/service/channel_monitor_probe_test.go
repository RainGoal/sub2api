package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestChannelMonitorProbeHeaderIsBoundToAPIKeyAndExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	header := BuildChannelMonitorProbeHeader("sk-monitor", now)

	if !VerifyChannelMonitorProbeHeader(header, "sk-monitor", now.Add(30*time.Second)) {
		t.Fatal("expected probe header to verify")
	}
	if VerifyChannelMonitorProbeHeader(header, "sk-other", now) {
		t.Fatal("probe header must be bound to the API key")
	}
	if VerifyChannelMonitorProbeHeader(header, "sk-monitor", now.Add(channelMonitorProbeMaxAge+time.Second)) {
		t.Fatal("expired probe header must be rejected")
	}
}

func TestChannelMonitorProbeAttemptContextHasPerAccountDeadline(t *testing.T) {
	base := WithChannelMonitorProbe(context.Background())
	attempt, cancel := ChannelMonitorProbeAttemptContext(base)
	defer cancel()

	deadline, ok := attempt.Deadline()
	if !ok {
		t.Fatal("expected monitor attempt deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > channelMonitorProbeAttemptTimeout {
		t.Fatalf("unexpected attempt deadline: %s", remaining)
	}
}

func TestNewChannelMonitorProbeTimeoutFailoverErrorOnlyForMarkedRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(WithChannelMonitorProbe(req.Context()))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	err := NewChannelMonitorProbeTimeoutFailoverError(c, nil, context.DeadlineExceeded)
	if err == nil || err.StatusCode != http.StatusGatewayTimeout || !err.ShouldRetryNextAccount() {
		t.Fatalf("expected retryable monitor timeout failover, got %#v", err)
	}

	ordinary := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request = ordinary
	if err := NewChannelMonitorProbeTimeoutFailoverError(c, nil, context.DeadlineExceeded); err != nil {
		t.Fatalf("ordinary request must not use monitor failover: %#v", err)
	}
}
