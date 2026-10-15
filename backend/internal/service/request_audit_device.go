package service

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/requestaudit"
	"github.com/tidwall/gjson"
)

// RequestAuditHeaderDevice reads only client-supplied installation identifiers.
// Provider account IDs and rewritten outbound identities must never reach here.
func RequestAuditHeaderDevice(headers http.Header) (deviceType, deviceID, source, resolution string) {
	resolution = "missing"
	for _, name := range []string{"X-Codex-Installation-Id", "X-Codex-Turn-Metadata"} {
		for _, raw := range headers.Values(name) {
			if raw == "" {
				continue
			}
			candidate, candidateResolution := raw, "resolved"
			candidateSource := "header.x-codex-installation-id"
			if name == "X-Codex-Turn-Metadata" {
				candidate, candidateResolution = requestAuditDeviceJSONField(raw, "installation_id")
				candidateSource = "header.x-codex-turn-metadata.installation_id"
			}
			if !requestaudit.ValidDeviceIdentity("codex", candidate) {
				if deviceID == "" && (candidateResolution == "invalid" || raw != "" && name == "X-Codex-Installation-Id") {
					resolution = "invalid"
				}
				continue
			}
			if deviceID == "" {
				deviceType, deviceID, source, resolution = "codex", strings.Clone(candidate), candidateSource, candidateResolution
			} else if candidate != deviceID || candidateResolution == "conflict" {
				resolution = "conflict"
			}
		}
	}
	return
}

func requestAuditDeviceJSONField(raw, field string) (deviceID, resolution string) {
	if !utf8.ValidString(raw) || !gjson.Valid(raw) || !gjson.Parse(raw).IsObject() {
		return "", "invalid"
	}
	resolution = "missing"
	seen := false
	gjson.Parse(raw).ForEach(func(key, value gjson.Result) bool {
		if key.String() != field {
			return true
		}
		candidate := ""
		if value.Type == gjson.String && requestaudit.ValidDeviceIdentity("codex", value.String()) {
			candidate = value.String()
		}
		if !seen {
			seen, deviceID, resolution = true, candidate, "invalid"
			if candidate != "" {
				resolution = "resolved"
			}
		} else if deviceID != "" && candidate != deviceID {
			resolution = "conflict"
		}
		return true
	})
	return strings.Clone(deviceID), resolution
}

// RequestAuditBodyDevice inspects the first decoded inbound body on a parsing
// copy, with the same normalization bound as the business request reader.
func RequestAuditBodyDevice(body []byte, claude bool, maxNormalizedBytes int64) (deviceType, deviceID, source, resolution string) {
	if !utf8.Valid(body) || maxNormalizedBytes > 0 && int64(len(body)) > maxNormalizedBytes {
		return "", "", "", "invalid"
	}
	normalized, err := httputil.NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
	if err != nil || !gjson.ValidBytes(normalized) {
		return "", "", "", "invalid"
	}
	return requestAuditBodyDevice(normalized, claude)
}

func requestAuditBodyDevice(body []byte, claude bool) (deviceType, deviceID, source, resolution string) {
	if claude {
		userID := gjson.GetBytes(body, "metadata.user_id")
		if userID.Type != gjson.String {
			return "", "", "", "missing"
		}
		raw := strings.TrimSpace(userID.String())
		if strings.HasPrefix(raw, "{") {
			deviceID, resolution = requestAuditDeviceJSONField(raw, "device_id")
		} else if parsed := ParseMetadataUserID(raw); parsed != nil {
			deviceID, resolution = strings.Clone(parsed.DeviceID), "resolved"
		} else {
			resolution = "invalid"
		}
		if requestaudit.ValidDeviceIdentity("claude", deviceID) {
			return "claude", deviceID, "body.metadata.user_id.device_id", resolution
		}
		return "", "", "", resolution
	}
	metadata := openAIRequestPayloadView(body).Get("client_metadata")
	if !metadata.IsObject() {
		return "", "", "", "missing"
	}
	deviceID, resolution = requestAuditDeviceJSONField(metadata.Raw, "x-codex-installation-id")
	source = "body.client_metadata.x-codex-installation-id"
	embedded := metadata.Get("x-codex-turn-metadata")
	if embedded.Type == gjson.String {
		candidate, candidateResolution := requestAuditDeviceJSONField(embedded.String(), "installation_id")
		if deviceID == "" && candidate != "" {
			deviceID, resolution = candidate, candidateResolution
			source = "body.client_metadata.x-codex-turn-metadata.installation_id"
		} else if deviceID != "" && candidate != "" && (candidate != deviceID || candidateResolution == "conflict") {
			resolution = "conflict"
		}
	}
	if deviceID == "" {
		return "", "", "", resolution
	}
	return "codex", deviceID, source, resolution
}
