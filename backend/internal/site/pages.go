// Pages 拥有 slug 和菜单可见性；文件技术细节由注入的存储处理。
package site

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const MaxPageFileSize = 1 << 20

var ErrPageNotFound = errors.New("page not found")
var ErrPageTooLarge = errors.New("page too large")
var ErrPageReadFailed = errors.New("failed to read page")
var ErrPageSlug = errors.New("invalid page slug")
var validSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

type PageFiles interface {
	ReadMarkdown(context.Context, string) ([]byte, error)
	ListPages(context.Context) ([]string, error)
	ImagePath(context.Context, string, string) (string, error)
}
type PageMenus interface{ GetCustomMenuItemsRaw(context.Context) string }
type Pages struct {
	files PageFiles
	menus PageMenus
}

func NewPages(files PageFiles, menus PageMenus) *Pages { return &Pages{files: files, menus: menus} }
func (s *Pages) visibility(ctx context.Context, slug string) (string, bool) {
	if s.menus == nil {
		return "", false
	}
	raw := s.menus.GetCustomMenuItemsRaw(ctx)
	if raw == "" || raw == "[]" {
		return "", false
	}
	var items []struct {
		URL        string `json:"url"`
		PageSlug   string `json:"page_slug"`
		Visibility string `json:"visibility"`
	}
	if json.Unmarshal([]byte(raw), &items) != nil {
		return "", false
	}
	for _, item := range items {
		page := item.PageSlug
		if page == "" && strings.HasPrefix(item.URL, "md:") {
			page = strings.TrimPrefix(item.URL, "md:")
		}
		if page == slug {
			return item.Visibility, true
		}
	}
	return "", false
}
func (s *Pages) ReadMarkdown(ctx context.Context, slug string, admin bool) ([]byte, error) {
	if !validSlugPattern.MatchString(slug) || len(slug) > 64 {
		return nil, ErrPageSlug
	}
	visibility, found := s.visibility(ctx, slug)
	if !found || visibility == "admin" && !admin {
		return nil, ErrPageNotFound
	}
	return s.files.ReadMarkdown(ctx, slug)
}
func (s *Pages) ListPages(ctx context.Context) ([]string, error) { return s.files.ListPages(ctx) }
func (s *Pages) ImagePath(ctx context.Context, slug, filename string) (string, error) {
	if !validSlugPattern.MatchString(slug) || len(slug) > 64 {
		return "", ErrPageNotFound
	}
	visibility, found := s.visibility(ctx, slug)
	if !found || visibility == "admin" {
		return "", ErrPageNotFound
	}
	return s.files.ImagePath(ctx, slug, filename)
}
