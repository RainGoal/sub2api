package service

import (
	"encoding/json"
	"time"
)

// Remember field presence so an explicit zero cannot silently fall back to minutes.
// Old JSON must not inherit the new 1800-second default over its stored minutes.
func (c *UpstreamBalanceConfig) UnmarshalJSON(raw []byte) error {
	type alias UpstreamBalanceConfig
	decoded := alias(*c)
	decoded.IntervalSeconds = 0
	decoded.intervalSecondsSet = false
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var fields struct {
		IntervalSeconds json.RawMessage `json:"interval_seconds"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	seconds := fields.IntervalSeconds
	present := len(seconds) > 0
	if present && string(seconds) == "null" {
		return ErrUpstreamBalanceInvalid
	}
	decoded.intervalSecondsSet = present
	*c = UpstreamBalanceConfig(decoded)
	return nil
}

// NormalizeUpstreamBalanceInterval only changes the supplied in-memory DTO.
// Seconds win when present; minutes remain valid for old clients and rollbacks.
func NormalizeUpstreamBalanceInterval(c *UpstreamBalanceConfig) error {
	if c == nil {
		return ErrUpstreamBalanceInvalid
	}
	if c.intervalSecondsSet || c.IntervalSeconds != 0 {
		if c.IntervalSeconds < 10 || c.IntervalSeconds > 86400 {
			return ErrUpstreamBalanceInvalid
		}
	} else {
		if c.IntervalMinutes < 5 || c.IntervalMinutes > 1440 {
			return ErrUpstreamBalanceInvalid
		}
		c.IntervalSeconds = c.IntervalMinutes * 60
	}
	c.IntervalMinutes = max(5, (c.IntervalSeconds+59)/60)
	return nil
}

func upstreamBalanceInterval(c *UpstreamBalanceConfig) time.Duration {
	return time.Duration(c.IntervalSeconds) * time.Second
}

func applyUpstreamBalanceTiming(item *UpstreamBalanceItem, interval time.Duration) {
	if item.LastAttemptAt != nil {
		delay := interval
		if item.Status == "unsupported" {
			delay = min(24*time.Hour, 4*delay)
		}
		next := item.LastAttemptAt.Add(delay)
		item.NextRefreshAt = &next
	}
	if item.LastSuccessAt != nil {
		fresh := item.LastSuccessAt.Add(2 * interval)
		item.FreshUntil = &fresh
	}
}

func (s *UpstreamBalanceService) notifyRunner() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
