package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/stretchr/testify/require"
)

// B04 保留普通页面和根内链接，只拒绝越出页面根的内容读取。
func TestMarkdownRootAndSize(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	pages := filepath.Join(root, "pages")
	require.NoError(t, os.WriteFile(filepath.Join(pages, "guide.md"), []byte("guide"), 0600))
	outside := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("private-fixture"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(pages, "escape.md")))
	_, err := store.ReadMarkdown(context.Background(), "escape")
	require.ErrorIs(t, err, site.ErrPageNotFound)
	require.NoError(t, os.Symlink(filepath.Join(pages, "guide.md"), filepath.Join(pages, "inside.md")))
	body, err := store.ReadMarkdown(context.Background(), "inside")
	require.NoError(t, err)
	require.Equal(t, "guide", string(body))
	require.NoError(t, os.WriteFile(filepath.Join(pages, "limit.md"), make([]byte, site.MaxPageFileSize), 0600))
	body, err = store.ReadMarkdown(context.Background(), "limit")
	require.NoError(t, err)
	require.Len(t, body, site.MaxPageFileSize)
	require.NoError(t, os.WriteFile(filepath.Join(pages, "large.md"), make([]byte, site.MaxPageFileSize+1), 0600))
	_, err = store.ReadMarkdown(context.Background(), "large")
	require.ErrorIs(t, err, site.ErrPageTooLarge)
}
