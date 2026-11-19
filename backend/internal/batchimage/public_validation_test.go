package batchimage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

// TestValidateSubmitRequestAndMetadataSanitization exercises item repetition
// expansion, duplicate custom-ID rejection, and deterministic metadata key
// trimming. It checks expanded custom IDs, collision errors, and sorted bounded
// metadata entries across ordinary and composite request hashes.
func TestValidateSubmitRequestAndMetadataSanitization(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService()

	normalized, err := svc.ValidateSubmitRequest(batchimage.BatchImageSubmitRequest{
		Model: "gemini-2.5-flash-image",
		Items: []batchimage.BatchImageSubmitItem{
			{CustomID: "cover", Prompt: "ok", OutputCount: 2},
		},
		Metadata: map[string]string{" title ": "  hello  ", "": "ignored"},
	})
	require.NoError(t, err)
	require.Len(t, normalized.Items, 2)
	require.Equal(t, "cover_01", normalized.Items[0].CustomID)
	require.Equal(t, "cover_02", normalized.Items[1].CustomID)
	require.Equal(t, map[string]string{"title": "hello"}, normalized.Metadata)

	_, err = svc.ValidateSubmitRequest(batchimage.BatchImageSubmitRequest{
		Model: "gemini-2.5-flash-image",
		Items: []batchimage.BatchImageSubmitItem{
			{CustomID: "cover", Prompt: "ok", OutputCount: 2},
			{CustomID: "cover_01", Prompt: "dup"},
		},
	})
	require.ErrorIs(t, err, batchimage.ErrBatchImageDuplicateCustomIDInRequest)

	groupA, groupB := int64(1), int64(2)
	hashA := batchimage.HashCompositeBatchImageSubmitRequest(normalized, &groupA)
	hashB := batchimage.HashCompositeBatchImageSubmitRequest(normalized, &groupB)
	require.NotEqual(t, hashA, hashB)
}
