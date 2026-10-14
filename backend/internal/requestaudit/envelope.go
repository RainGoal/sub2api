package requestaudit

import (
	"encoding/base64"
	"net/http"
	"unicode/utf8"
)

// HTTP field values and request targets may contain obs-text bytes. JSON's
// automatic UTF-8 replacement would lose them, so encode only those values.
func envelopeText(value string) any {
	if utf8.ValidString(value) {
		return value
	}
	return map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(value))}
}

func envelopeHeaders(headers http.Header) map[string][]any {
	if headers == nil {
		return nil
	}
	result := make(map[string][]any, len(headers))
	for key, values := range headers {
		result[key] = make([]any, len(values))
		for index, value := range values {
			result[key][index] = envelopeText(value)
		}
	}
	return result
}
