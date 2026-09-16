// Pages 只拥有本地页面文件读取，身份与菜单规则由 site 决定。
package filesystem

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/site"
)

type Pages struct{ pagesDir string }

func New(dataDir string) *Pages {
	dir := filepath.Join(dataDir, "pages")
	_ = os.MkdirAll(dir, 0755)
	return &Pages{pagesDir: dir}
}

// @project-doc docs/interfaces/http_api.md#site_pages
func (s *Pages) ReadMarkdown(ctx context.Context, slug string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(s.pagesDir)
	if err != nil {
		return nil, site.ErrPageNotFound
	}
	target, err := filepath.EvalSymlinks(filepath.Join(s.pagesDir, slug+".md"))
	if err != nil || !isPathWithinBase(target, root) {
		return nil, site.ErrPageNotFound
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return nil, site.ErrPageNotFound
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, site.ErrPageNotFound
	}
	defer func() { _ = base.Close() }()
	file, err := base.Open(rel)
	if err != nil {
		return nil, site.ErrPageNotFound
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return nil, site.ErrPageNotFound
	}
	if info.Size() > site.MaxPageFileSize {
		return nil, site.ErrPageTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(file, site.MaxPageFileSize+1))
	if err != nil {
		return nil, site.ErrPageReadFailed
	}
	if len(content) > site.MaxPageFileSize {
		return nil, site.ErrPageTooLarge
	}
	return content, nil
}
func (s *Pages) ListPages(context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.pagesDir)
	if err != nil {
		return []string{}, nil
	}
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			slugs = append(slugs, strings.TrimSuffix(entry.Name(), ".md"))
		}
	}
	return slugs, nil
}
func (s *Pages) ImagePath(_ context.Context, slug, filename string) (string, error) {
	path, ok := resolvePageImagePath(s.pagesDir, filepath.Join(s.pagesDir, slug), filename)
	if !ok {
		return "", site.ErrPageNotFound
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", site.ErrPageNotFound
	}
	return path, nil
}
func resolvePageImagePath(pagesDir, imagesDir, filename string) (string, bool) {
	relPath, ok := cleanPageImageRelativePath(filename)
	if !ok {
		return "", false
	}

	cleanedPagesDir := filepath.Clean(pagesDir)
	cleanedImagesDir := filepath.Clean(imagesDir)
	cleanedTarget := filepath.Clean(filepath.Join(cleanedImagesDir, relPath))
	if !isPathWithinBase(cleanedTarget, cleanedImagesDir) {
		return "", false
	}

	realPagesDir, err := filepath.EvalSymlinks(cleanedPagesDir)
	if err != nil {
		return "", false
	}
	realImagesDir, err := filepath.EvalSymlinks(cleanedImagesDir)
	if err != nil || !isPathWithinBase(realImagesDir, realPagesDir) {
		return "", false
	}
	realTarget, err := filepath.EvalSymlinks(cleanedTarget)
	if err != nil || !isPathWithinBase(realTarget, realImagesDir) {
		return "", false
	}
	return realTarget, true
}
func cleanPageImageRelativePath(filename string) (string, bool) {
	if filename == "" || strings.HasPrefix(filename, "/") {
		return "", false
	}
	decoded, err := url.PathUnescape(filename)
	if err != nil {
		return "", false
	}
	if decoded == "" || strings.HasPrefix(decoded, "/") || strings.Contains(decoded, "\\") || strings.ContainsRune(decoded, 0) {
		return "", false
	}

	parts := make([]string, 0)
	for _, part := range strings.Split(decoded, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", false
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "", false
	}

	relPath := filepath.Join(parts...)
	if filepath.IsAbs(relPath) || filepath.VolumeName(relPath) != "" {
		return "", false
	}
	return relPath, true
}
func isPathWithinBase(path, base string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
