package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIClientRequestedModelKey = "openai_client_requested_model"

// A presentation failure is local: it must never trigger another upstream call.
var errOpenAIClientPayload = errors.New("invalid upstream response payload")

func IsOpenAIClientPayloadError(err error) bool {
	return errors.Is(err, errOpenAIClientPayload)
}

// ValidateOpenAIClientModel runs before forwarding, without restricting public
// aliases to any provider's model catalog.
func ValidateOpenAIClientModel(model string) error {
	if strings.TrimSpace(model) == "" || !utf8.ValidString(model) || strings.ContainsFunc(model, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return fmt.Errorf("%w: invalid public model", errOpenAIClientPayload)
	}
	return nil
}

func openAIClientPrivacyApplies(account *Account) bool {
	return account == nil || account.Platform == "" || account.Platform == PlatformOpenAI
}

func openAIClientErrorMessageForAccount(account *Account, status int, body []byte, message string) string {
	if openAIClientPrivacyApplies(account) {
		return OpenAIClientErrorMessage(status, body, message)
	}
	return message
}

// SetOpenAIClientRequestedModel snapshots the public name before channel/account
// mapping. Failover attempts keep the same snapshot; billing uses its own fields.
func SetOpenAIClientRequestedModel(c *gin.Context, fallback string) string {
	model := openAIClientRequestedModel(c, fallback)
	if c != nil {
		if _, exists := c.Get(openAIClientRequestedModelKey); !exists {
			c.Set(openAIClientRequestedModelKey, model)
		}
	}
	return model
}

// HasOpenAIClientRequestedModel scopes shared error writers to conversation
// requests that established an OpenAI presentation boundary before forwarding.
func HasOpenAIClientRequestedModel(c *gin.Context) bool {
	if c == nil {
		return false
	}
	_, exists := c.Get(openAIClientRequestedModelKey)
	return exists
}

func openAIClientRequestedModel(c *gin.Context, fallback string) string {
	if c != nil {
		if value, exists := c.Get(openAIClientRequestedModelKey); exists {
			model, _ := value.(string)
			return model
		}
		if c.Request != nil {
			if model, ok := RequestedPublicModelFromContext(c.Request.Context()); ok {
				return model
			}
		}
	}
	return strings.TrimSpace(fallback)
}

func rewriteOpenAIClientPayload(payload []byte, publicModel string) ([]byte, error) {
	if data := bytes.TrimSpace(payload); len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return payload, nil
	}
	clientPayload, err := rewriteOpenAIClientModel(payload, publicModel)
	if err != nil {
		return nil, err
	}
	clientPayload, err = sanitizeOpenAIClientErrorPayload(clientPayload, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: error envelope", errOpenAIClientPayload)
	}
	return clientPayload, nil
}

// Only protocol identity fields are edited. Assistant text, tool arguments and
// opaque state are deliberately outside these paths, even if they contain model.
func rewriteOpenAIClientModel(payload []byte, publicModel string) ([]byte, error) {
	data := bytes.TrimSpace(payload)
	if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
		return payload, nil
	}
	if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return nil, errOpenAIClientPayload
	}
	paths, fallback := openAIClientModelPaths(gjson.ParseBytes(payload))
	if len(paths) == 0 {
		return payload, nil
	}
	publicModel = strings.TrimSpace(publicModel)
	if err := ValidateOpenAIClientModel(publicModel); err != nil {
		return nil, err
	}
	if !fallback {
		updated := payload
		for _, path := range paths {
			if current := gjson.GetBytes(updated, path); current.Type == gjson.String && current.Str == publicModel {
				continue
			}
			var err error
			updated, err = sjson.SetBytes(updated, path, publicModel)
			if err != nil {
				fallback = true
				break
			}
		}
		if !fallback && gjson.ValidBytes(updated) {
			return updated, nil
		}
	}
	return rewriteOpenAIClientModelFull(payload, publicModel)
}

func openAIClientModelPaths(root gjson.Result) ([]string, bool) {
	paths := make([]string, 0, 4)
	seen := make(map[string]bool, 4)
	fallback := false
	root.ForEach(func(key, value gjson.Result) bool {
		name := key.Str
		if name != "model" && name != "response" && name != "message" && name != "session" {
			return true
		}
		fallback = fallback || seen[name] || key.Raw != `"`+name+`"`
		seen[name] = true
		if name == "model" {
			paths = append(paths, name)
		} else if value.IsObject() {
			found := false
			value.ForEach(func(childKey, _ gjson.Result) bool {
				if childKey.Str == "model" {
					fallback = fallback || found || childKey.Raw != `"model"`
					found = true
					paths = append(paths, name+".model")
				}
				return true
			})
		}
		return true
	})
	return paths, fallback
}

// RawMessage preserves unknown values (including large integers and opaque
// signatures). Duplicate/escaped identity keys use standard JSON last-key wins
// semantics, removing every alternate spelling rather than leaking a second C.
func rewriteOpenAIClientModelFull(payload []byte, publicModel string) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil || document == nil {
		return nil, errOpenAIClientPayload
	}
	encoded, err := json.Marshal(publicModel)
	if err != nil {
		return nil, errOpenAIClientPayload
	}
	if _, exists := document["model"]; exists {
		document["model"] = encoded
	}
	for _, parent := range []string{"response", "message", "session"} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(document[parent], &nested); err != nil || nested == nil {
			continue
		}
		if _, exists := nested["model"]; exists {
			nested["model"] = encoded
			value, err := json.Marshal(nested)
			if err != nil {
				return nil, errOpenAIClientPayload
			}
			document[parent] = value
		}
	}
	updated, err := json.Marshal(document)
	if err != nil {
		return nil, errOpenAIClientPayload
	}
	return updated, nil
}
