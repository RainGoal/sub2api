package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractGrokTTSInputText(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{name: "input", body: []byte(`{"input":" speak this "}`), want: "speak this"},
		{name: "text alias", body: []byte(`{"text":"read this"}`), want: "read this"},
		{name: "prompt alias", body: []byte(`{"prompt":"say this"}`), want: "say this"},
		{name: "invalid input falls back", body: []byte(`{"input":42,"text":"fallback"}`), want: "fallback"},
		{name: "invalid json", body: []byte(`not-json`), want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, extractGrokTTSInputText(test.body))
		})
	}
}
