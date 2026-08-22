package service

import (
	"context"
	"testing"
	"time"
)

func TestActiveConnectionLifecycleAndUserIsolation(t *testing.T) {
	svc := NewActiveConnectionService()
	clock := time.Date(2026, time.August, 22, 1, 2, 3, 0, time.UTC)
	svc.now = func() time.Time { return clock }

	snapshot, events, unsubscribe := svc.Subscribe(7)
	defer unsubscribe()
	if len(snapshot) != 0 {
		t.Fatalf("initial snapshot has %d connections", len(snapshot))
	}

	handle := svc.Start(7, ActiveConnectionStart{
		RequestID:   "req-7",
		Model:       "gpt-5",
		RequestType: "chat_completions",
		APIKeyName:  "primary",
	})
	if handle == nil {
		t.Fatal("expected active connection handle")
	}
	event := <-events
	if event.Type != "connection.started" || event.Connection == nil || event.Connection.RequestID != "req-7" {
		t.Fatalf("unexpected started event: %+v", event)
	}
	if got := len(svc.Snapshot(8)); got != 0 {
		t.Fatalf("user isolation failed, user 8 saw %d connections", got)
	}

	clock = clock.Add(120 * time.Millisecond)
	UpdateActiveConnectionMetadata(WithActiveConnection(context.Background(), handle), "gpt-5-mini", true, "responses")
	updated := <-events
	if updated.Type != "connection.updated" || updated.Connection == nil || updated.Connection.Model != "gpt-5-mini" || !updated.Connection.Stream {
		t.Fatalf("unexpected metadata event: %+v", updated)
	}

	MarkFirstSSEData(WithActiveConnection(context.Background(), handle), " ")
	MarkFirstSSEData(WithActiveConnection(context.Background(), handle), "[DONE]")
	if got := svc.Snapshot(7)[0].FirstTokenMs; got != nil {
		t.Fatalf("empty or DONE data unexpectedly set first token: %v", *got)
	}
	clock = clock.Add(80 * time.Millisecond)
	MarkFirstSSEData(WithActiveConnection(context.Background(), handle), `{"type":"response.created"}`)
	firstData := <-events
	if firstData.Connection == nil || firstData.Connection.FirstTokenMs == nil || *firstData.Connection.FirstTokenMs != 200 {
		t.Fatalf("unexpected first data timing: %+v", firstData.Connection)
	}

	handle.Finish(ActiveConnectionStatusCompleted, "")
	handle.Finish(ActiveConnectionStatusFailed, "must be ignored")
	completed := <-events
	if completed.Type != "connection.completed" || completed.RequestID != "req-7" {
		t.Fatalf("unexpected completed event: %+v", completed)
	}
	if got := len(svc.Snapshot(7)); got != 0 {
		t.Fatalf("completed connection remained in snapshot: %d", got)
	}
}

func TestActiveConnectionPrunesExpiredEntries(t *testing.T) {
	svc := NewActiveConnectionService()
	clock := time.Date(2026, time.August, 22, 1, 2, 3, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	_, events, unsubscribe := svc.Subscribe(9)
	defer unsubscribe()
	if svc.Start(9, ActiveConnectionStart{RequestID: "expired"}) == nil {
		t.Fatal("expected active connection handle")
	}
	_ = <-events
	clock = clock.Add(activeConnectionTTL + time.Second)
	svc.PruneExpired()
	if got := len(svc.Snapshot(9)); got != 0 {
		t.Fatalf("expired connection remained in snapshot: %d", got)
	}
	failed := <-events
	if failed.Type != "connection.failed" || failed.RequestID != "expired" {
		t.Fatalf("unexpected expiry event: %+v", failed)
	}
}
