package requestaudit

import (
	"encoding/json"
	"errors"
)

const maxAuditHeaderBytes = 16 * 1024
const deferredHeaderEncoding = "deferred_encoding"

func materializeHeaderSnapshot(event *auditEvent) error {
	switch event.kind {
	case "header_snapshot":
		if event.headerMeta == nil {
			return errors.New("request audit header snapshot is missing metadata")
		}
		raw, err := marshalRequestAuditHeaders(*event.headerMeta)
		if err != nil {
			return err
		}
		if len(raw) > maxAuditHeaderBytes {
			return errors.New("request audit header snapshot exceeds its limit")
		}
		event.kind, event.raw, event.headerMeta = "chunk", raw, nil
	case "attempt_header_snapshot":
		raw, err := json.Marshal(event.headerFields)
		if err != nil {
			return err
		}
		if len(raw) > maxAuditHeaderBytes {
			return errors.New("upstream request header snapshot exceeds its limit")
		}
		event.kind, event.raw, event.headerFields = "attempt_chunk", raw, nil
	default:
		return nil
	}
	event.payloadEvent = false
	return nil
}
