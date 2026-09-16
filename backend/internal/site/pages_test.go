package site

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type pageMenus string

func (m pageMenus) GetCustomMenuItemsRaw(context.Context) string { return string(m) }

type pageFixtureFiles struct{}

func (pageFixtureFiles) ReadMarkdown(context.Context, string) ([]byte, error) {
	return []byte("body"), nil
}
func (pageFixtureFiles) ListPages(context.Context) ([]string, error) {
	return []string{"user", "admin"}, nil
}
func (pageFixtureFiles) ImagePath(context.Context, string, string) (string, error) {
	return "image", nil
}
func TestPageVisibilityContracts(t *testing.T) {
	service := NewPages(pageFixtureFiles{}, pageMenus(`[{"url":"md:user","visibility":"user"},{"page_slug":"admin","visibility":"admin"}]`))
	ctx := context.Background()
	_, err := service.ReadMarkdown(ctx, "user", false)
	require.NoError(t, err)
	_, err = service.ReadMarkdown(ctx, "admin", false)
	require.ErrorIs(t, err, ErrPageNotFound)
	_, err = service.ReadMarkdown(ctx, "admin", true)
	require.NoError(t, err)
	_, err = service.ReadMarkdown(ctx, "missing", true)
	require.ErrorIs(t, err, ErrPageNotFound)
	_, err = service.ReadMarkdown(ctx, "../user", true)
	require.ErrorIs(t, err, ErrPageSlug)
	_, err = service.ImagePath(ctx, "user", "x.png")
	require.NoError(t, err)
	_, err = service.ImagePath(ctx, "admin", "x.png")
	require.ErrorIs(t, err, ErrPageNotFound)
}
