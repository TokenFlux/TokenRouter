package httpapi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOpenAICompatibleStream(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStream bool
		wantOK     bool
	}{
		{name: "missing", body: `{"model":"gpt-5"}`, wantStream: false, wantOK: true},
		{name: "true", body: `{"model":"gpt-5","stream":true}`, wantStream: true, wantOK: true},
		{name: "false", body: `{"model":"gpt-5","stream":false}`, wantStream: false, wantOK: true},
		{name: "string", body: `{"model":"gpt-5","stream":"true"}`, wantStream: false, wantOK: false},
		{name: "number", body: `{"model":"gpt-5","stream":1}`, wantStream: false, wantOK: false},
		{name: "null", body: `{"model":"gpt-5","stream":null}`, wantStream: false, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStream, gotOK := ParseOpenAICompatibleStream([]byte(tt.body))

			require.Equal(t, tt.wantStream, gotStream)
			require.Equal(t, tt.wantOK, gotOK)
		})
	}
}
