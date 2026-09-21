package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// identityPatchStore 只控制原单键读取，不引入缓存或批量查询。
type identityPatchStore struct {
	RuntimeSettingsStore
	values map[string]string
	err    error
	reads  []string
}

func (s *identityPatchStore) GetValue(_ context.Context, key string) (string, error) {
	s.reads = append(s.reads, key)
	return s.values[key], s.err
}

func TestIdentityPatchSettingsOriginalReadSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		err         error
		enabled     bool
	}{
		{name: "enabled", value: "true", enabled: true},
		{name: "disabled", value: "false"},
		{name: "empty"},
		{name: "exact_case", value: "TRUE"},
		{name: "read_failure", err: errors.New("read failed"), enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &identityPatchStore{values: map[string]string{SettingKeyEnableIdentityPatch: tc.value, SettingKeyIdentityPatchPrompt: "原提示词"}, err: tc.err}
			runtime := NewRuntimeSettings(store, nil, nil)
			require.Empty(t, store.reads)
			require.Equal(t, tc.enabled, runtime.IsIdentityPatchEnabled(context.Background()))
			expected := "原提示词"
			if tc.err != nil {
				expected = ""
			}
			require.Equal(t, expected, runtime.GetIdentityPatchPrompt(context.Background()))
			require.Equal(t, []string{SettingKeyEnableIdentityPatch, SettingKeyIdentityPatchPrompt}, store.reads)
		})
	}
}
