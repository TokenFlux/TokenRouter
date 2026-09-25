package openai_test

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

func TestCollectOpenAIEncryptedContentDigestsRaw(t *testing.T) {
	t.Parallel()

	digests := openai.CollectOpenAIEncryptedContentDigestsRaw([]byte(`{"input":[
		{"type":"reasoning","encrypted_content":"cipher-a"},
		{"type":"compaction","encrypted_content":"cipher-b"},
		{"type":"input_text","text":"hello"},
		{"type":"reasoning"},
		{"type":"message","encrypted_content":"cipher-alien"}
	]}`))
	// 剥离端处理不了的类型（message）不收摘要，否则该摘要永远命中却剥不掉。
	require.Equal(t, []string{
		openai.OpenAIEncryptedContentDigest("cipher-a"),
		openai.OpenAIEncryptedContentDigest("cipher-b"),
	}, digests)

	require.Nil(t, openai.CollectOpenAIEncryptedContentDigestsRaw([]byte(`{"model":"gpt-5"}`)))
}

func TestStripOpenAIInvalidEncryptedContentItems(t *testing.T) {
	t.Parallel()

	invalid := map[string]struct{}{
		openai.OpenAIEncryptedContentDigest("stale-cipher"): {},
	}

	t.Run("only_strips_matching_items", func(t *testing.T) {
		t.Parallel()
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "stale-cipher", "summary": []any{}},
				map[string]any{"type": "reasoning", "id": "rs_2", "encrypted_content": "fresh-cipher"},
				map[string]any{"type": "input_text", "text": "hello"},
			},
		}
		stripped := openai.StripOpenAIInvalidEncryptedContentItems(reqBody, invalid)
		require.Equal(t, 1, stripped)
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 3)
		first, ok := input[0].(map[string]any)
		require.True(t, ok)
		_, hasEncrypted := first["encrypted_content"]
		require.False(t, hasEncrypted, "命中项应剥离 encrypted_content")
		require.Equal(t, "rs_1", first["id"], "reasoning 骨架应保留")
		second, ok := input[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "fresh-cipher", second["encrypted_content"], "新密文不得误删")
	})

	t.Run("compaction_removed_entirely", func(t *testing.T) {
		t.Parallel()
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"type": "compaction", "encrypted_content": "stale-cipher"},
				map[string]any{"type": "input_text", "text": "hello"},
			},
		}
		stripped := openai.StripOpenAIInvalidEncryptedContentItems(reqBody, invalid)
		require.Equal(t, 1, stripped)
		input, ok := reqBody["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 1)
		survivor, ok := input[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "input_text", survivor["type"])
	})

	t.Run("no_hit_no_change", func(t *testing.T) {
		t.Parallel()
		reqBody := map[string]any{
			"input": []any{
				map[string]any{"type": "reasoning", "encrypted_content": "fresh-cipher"},
			},
		}
		require.Zero(t, openai.StripOpenAIInvalidEncryptedContentItems(reqBody, invalid))
	})
}

func TestStripOpenAIInvalidEncryptedContentFromReplayItems(t *testing.T) {
	t.Parallel()

	invalid := map[string]struct{}{
		openai.OpenAIEncryptedContentDigest("stale-cipher"): {},
	}

	t.Run("rewrites_hit_items_and_shares_the_rest", func(t *testing.T) {
		t.Parallel()
		original := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher"}`)
		clean := json.RawMessage(`{"type":"input_text","text":"hello"}`)
		compaction := json.RawMessage(`{"type":"compaction","encrypted_content":"stale-cipher"}`)
		items := []json.RawMessage{original, clean, compaction}

		next, stripped := openai.StripOpenAIInvalidEncryptedContentFromReplayItems(items, invalid)
		require.Equal(t, 2, stripped)
		require.Len(t, next, 2, "compaction 命中项应整项删除")
		var rewritten map[string]any
		require.NoError(t, json.Unmarshal(next[0], &rewritten))
		require.Equal(t, "rs_1", rewritten["id"])
		_, hasEncrypted := rewritten["encrypted_content"]
		require.False(t, hasEncrypted)
		// 未命中项正文共享；原命中项正文不被修改（整体替换语义）。
		require.Same(t, &clean[0], &next[1][0])
		require.Contains(t, string(original), "stale-cipher")
	})

	t.Run("miss_returns_original_header", func(t *testing.T) {
		t.Parallel()
		items := []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"fresh-cipher"}`)}
		next, stripped := openai.StripOpenAIInvalidEncryptedContentFromReplayItems(items, invalid)
		require.Zero(t, stripped)
		require.Same(t, &items[0][0], &next[0][0])
	})
}

func TestStripOpenAIInvalidEncryptedContentRaw(t *testing.T) {
	t.Parallel()

	invalid := map[string]struct{}{
		openai.OpenAIEncryptedContentDigest("stale-cipher"): {},
	}
	payload := []byte(`{"model":"gpt-5","input":[
		{"type":"reasoning","id":"rs_1","encrypted_content":"stale-cipher"},
		{"type":"reasoning","id":"rs_2","encrypted_content":"fresh-cipher"},
		{"type":"input_text","text":"hello"}
	],"stream":true}`)

	stripped, count, err := openai.StripOpenAIInvalidEncryptedContentRaw(payload, invalid)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stripped, &decoded))
	require.Equal(t, "gpt-5", decoded["model"])
	input, ok := decoded["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 3)
	first, ok := input[0].(map[string]any)
	require.True(t, ok)
	_, hasEncrypted := first["encrypted_content"]
	require.False(t, hasEncrypted)
	second, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fresh-cipher", second["encrypted_content"])

	t.Run("miss_fast_path_returns_original", func(t *testing.T) {
		t.Parallel()
		cleanPayload := []byte(`{"input":[{"type":"reasoning","encrypted_content":"fresh-cipher"}]}`)
		unchanged, count, err := openai.StripOpenAIInvalidEncryptedContentRaw(cleanPayload, invalid)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Same(t, &cleanPayload[0], &unchanged[0], "未命中时应原样返回，不重编码")
	})
}
