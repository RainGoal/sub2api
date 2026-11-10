package service

import (
	"fmt"
	"testing"
	"time"
)

func TestChannelMonitorDegradedThreshold(t *testing.T) {
	tests := []struct {
		name    string
		latency time.Duration
		status  string
	}{
		{"old threshold", 6 * time.Second, MonitorStatusOperational},
		{"below twelve seconds", 12*time.Second - time.Nanosecond, MonitorStatusOperational},
		{"exactly twelve seconds", 12 * time.Second, MonitorStatusOperational},
		{"just over twelve seconds", 12*time.Second + time.Nanosecond, MonitorStatusDegraded},
		{"over twelve seconds", 13 * time.Second, MonitorStatusDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			latencyMs := int(tt.latency / time.Millisecond)
			result := finalizeOperationalOrDegraded(&CheckResult{}, tt.latency, latencyMs)
			if result.Status != tt.status {
				t.Fatalf("latency %s: got status %q, want %q", tt.latency, result.Status, tt.status)
			}
			wantMessage := ""
			if tt.status == MonitorStatusDegraded {
				wantMessage = fmt.Sprintf("slow response: %dms", latencyMs)
			}
			if result.Message != wantMessage {
				t.Fatalf("latency %s: got message %q, want %q", tt.latency, result.Message, wantMessage)
			}
		})
	}
}
