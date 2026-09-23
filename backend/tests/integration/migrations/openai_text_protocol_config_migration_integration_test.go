//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	dbmigrations "github.com/TokenFlux/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration247NormalizesOpenAITextProtocolConfigIdempotently(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("247_migrate_openai_text_protocol_config.sql")
	require.NoError(t, err)

	tests := []struct {
		name        string
		platform    string
		accountType string
		credentials string
		extra       string
		wantCaps    []any
		wantMode    string
		wantProbe   string
		targeted    bool
	}{
		{
			name:     "旧 auto、true 与数组能力",
			platform: "openai", accountType: "apikey",
			credentials: `{"api_key":"secret","openai_capabilities":["chat_completions","embeddings"]}`,
			extra:       `{"openai_responses_mode":"auto","openai_responses_supported":true,"custom":{"nested":1}}`,
			wantCaps:    []any{"text_generation", "embeddings"}, wantMode: "preserve_client_protocol", wantProbe: "supported", targeted: true,
		},
		{
			name:     "旧 force responses、false 与对象能力",
			platform: "openai", accountType: "apikey",
			credentials: `{"openai_capabilities":{"chat_completions":true,"embeddings":false}}`,
			extra:       `{"openai_responses_mode":"force_responses","openai_responses_supported":false}`,
			wantCaps:    []any{"text_generation"}, wantMode: "force_responses", wantProbe: "unsupported", targeted: true,
		},
		{
			name:     "旧 force chat、缺失探测与显式空能力",
			platform: "openai", accountType: "apikey",
			credentials: `{"openai_capabilities":[]}`,
			extra:       `{"openai_responses_mode":"force_chat_completions"}`,
			wantCaps:    []any{}, wantMode: "force_chat_completions", wantProbe: "unknown", targeted: true,
		},
		{
			name:     "缺失旧配置使用默认值",
			platform: "openai", accountType: "apikey",
			credentials: `{"api_key":"secret"}`,
			extra:       `{"custom":true}`,
			wantCaps:    []any{"text_generation", "embeddings"}, wantMode: "preserve_client_protocol", wantProbe: "unknown", targeted: true,
		},
		{
			name:     "非法旧值归一化",
			platform: "openai", accountType: "apikey",
			credentials: `{"openai_capabilities":{"chat_completions":false,"embeddings":false}}`,
			extra:       `{"openai_responses_mode":"invalid","openai_responses_supported":"true"}`,
			wantCaps:    []any{}, wantMode: "preserve_client_protocol", wantProbe: "unknown", targeted: true,
		},
		{
			name:     "已有新字段优先于遗留字段",
			platform: "openai", accountType: "apikey",
			credentials: `{"openai_workload_capabilities":["embeddings"],"openai_capabilities":["chat_completions"]}`,
			extra:       `{"openai_text_route_mode":"force_chat_completions","openai_responses_probe_status":"supported","openai_responses_mode":"force_responses","openai_responses_supported":false}`,
			wantCaps:    []any{"embeddings"}, wantMode: "force_chat_completions", wantProbe: "supported", targeted: true,
		},
		{
			name:     "非目标账号保持原样",
			platform: "grok", accountType: "apikey",
			credentials: `{"openai_capabilities":["chat_completions"]}`,
			extra:       `{"openai_responses_mode":"auto"}`,
			targeted:    false,
		},
	}

	ids := make([]int64, 0, len(tests))
	for index, tc := range tests {
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra)
VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)
RETURNING id
`, fmt.Sprintf("migration-247-%d", index), tc.platform, tc.accountType, tc.credentials, tc.extra).Scan(&id))
		ids = append(ids, id)
	}

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "迁移必须可安全重复执行")

	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var credentialsBytes, extraBytes []byte
			require.NoError(t, tx.QueryRowContext(ctx,
				"SELECT credentials, extra FROM accounts WHERE id = $1", ids[index],
			).Scan(&credentialsBytes, &extraBytes))
			var credentials, extra map[string]any
			require.NoError(t, json.Unmarshal(credentialsBytes, &credentials))
			require.NoError(t, json.Unmarshal(extraBytes, &extra))

			if !tc.targeted {
				require.Contains(t, credentials, "openai_capabilities")
				require.Contains(t, extra, "openai_responses_mode")
				return
			}
			require.Equal(t, tc.wantCaps, credentials["openai_workload_capabilities"])
			require.Equal(t, tc.wantMode, extra["openai_text_route_mode"])
			require.Equal(t, tc.wantProbe, extra["openai_responses_probe_status"])
			require.NotContains(t, credentials, "openai_capabilities")
			require.NotContains(t, extra, "openai_responses_mode")
			require.NotContains(t, extra, "openai_responses_supported")
			if index == 0 {
				require.Equal(t, "secret", credentials["api_key"])
				require.Equal(t, map[string]any{"nested": float64(1)}, extra["custom"])
			}
		})
	}
}
