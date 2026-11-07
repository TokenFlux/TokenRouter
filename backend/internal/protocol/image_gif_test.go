package protocol

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

// image_gif_test.go checks shared GIF controls and bounded container accounting.
// Small two-frame bytes exercise complete blocks, truncated data, logical screen
// bounds, and frame-delay normalization without relying on any external image service.

// TestParseGIFMetadata counts complete image descriptors instead of marker bytes
// in compressed data. Every truncated prefix must fail without returning usable metadata.
func TestParseGIFMetadata(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs=")
	require.NoError(t, err)
	result, err := ParseGIFMetadata(data)
	require.NoError(t, err)
	require.Equal(t, GIFMetadata{Width: 1, Height: 1, FrameCount: 2}, result)
	for size := range data {
		_, err := ParseGIFMetadata(data[:size])
		require.Error(t, err)
	}
	data[6] = 0
	_, err = ParseGIFMetadata(data)
	require.Error(t, err)
}

// TestNormalizeGIFOptions protects defaults and strict animation-only options.
// Explicit out-of-range controls fail while supported delays use upstream centisecond precision.
func TestNormalizeGIFOptions(t *testing.T) {
	frames, duration, err := NormalizeGIFOptions("gif", nil, nil)
	require.NoError(t, err)
	require.Equal(t, 9, frames)
	require.Equal(t, 200, duration)
	count, delay := 16, 255
	frames, duration, err = NormalizeGIFOptions("GIF", &count, &delay)
	require.NoError(t, err)
	require.Equal(t, 16, frames)
	require.Equal(t, 260, duration)
	_, _, err = NormalizeGIFOptions("png", &count, nil)
	require.Error(t, err)
	for _, count := range []int{-1, 0, 1, 17} {
		_, _, err = NormalizeGIFOptions("gif", &count, nil)
		require.Error(t, err)
	}
	for _, delay := range []int{-1, 0, 19, 2001} {
		_, _, err = NormalizeGIFOptions("gif", nil, &delay)
		require.Error(t, err)
	}
}
