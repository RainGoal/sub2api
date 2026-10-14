package requestaudit

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// PostgreSQL TEXT rejects NUL and invalid UTF-8. Invalid searchable summaries
// are omitted, never normalized into another identity; raw BYTEA stays intact.
func validSummaryText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func safeSummaryText(value string) string {
	if !validSummaryText(value) {
		return ""
	}
	return value
}

func storageEvent(e auditEvent) auditEvent {
	if e.meta != nil {
		meta := *e.meta
		meta.Kind, meta.Method = safeSummaryText(meta.Kind), safeSummaryText(meta.Method)
		meta.RoutePattern, meta.Protocol = safeSummaryText(meta.RoutePattern), safeSummaryText(meta.Protocol)
		meta.ClientRequestID, meta.ConnectionID = safeSummaryText(meta.ClientRequestID), safeSummaryText(meta.ConnectionID)
		meta.MessageType = safeSummaryText(meta.MessageType)
		e.meta = &meta
	}
	if e.identity != nil {
		id := *e.identity
		id.SessionID, id.SessionSource = safeSummaryText(id.SessionID), safeSummaryText(id.SessionSource)
		id.SessionResolution, id.Model = safeSummaryText(id.SessionResolution), safeSummaryText(id.Model)
		e.identity = &id
	}
	if e.completion != nil {
		done := *e.completion
		done.Outcome, done.UpstreamSummary = safeSummaryText(done.Outcome), safeSummaryText(done.UpstreamSummary)
		e.completion = &done
	}
	return e
}

type rejectedEventError struct {
	id  string
	err error
}

func (e *rejectedEventError) Error() string { return "request audit event rejected" }
func (e *rejectedEventError) Unwrap() error { return e.err }

func isolateEventError(id string, err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		// Only row-value failures are isolated. Generic data exceptions,
		// transaction failures and schema/resource failures still fail the batch.
		switch state.SQLState() {
		case "22001", "22003", "22021", "22P02", "22P05", "23502", "23503", "23505", "23514":
			return &rejectedEventError{id: id, err: err}
		}
	}
	return err
}
