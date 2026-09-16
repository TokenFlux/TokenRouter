// 站点导航 DTO 保留原字段、排序与解析失败回退。
package dto

import (
	"encoding/json"
	"strings"
)

// ParseHomeFeaturedModels 将 JSON 字符串解析为首页展示模型 ID 列表。
// 空串或非法输入返回空切片。
func ParseHomeFeaturedModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []string{}
	}
	var models []string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return []string{}
	}
	return models
}

// ParseFooterLinks parses a JSON string into a slice of FooterLinkGroup.
// Returns empty slice on empty/invalid input.
func ParseFooterLinks(raw string) []FooterLinkGroup {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []FooterLinkGroup{}
	}
	var groups []FooterLinkGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return []FooterLinkGroup{}
	}
	return groups
}

// FooterLinkGroup 首页底栏链接分组（一列）。
type FooterLinkGroup struct {
	Title string       `json:"title"`
	Links []FooterLink `json:"links"`
}

// FooterLink 首页底栏单条链接。
type FooterLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ParseCustomEndpoints parses a JSON string into a slice of CustomEndpoint.
// Returns empty slice on empty/invalid input.
func ParseCustomEndpoints(raw string) []CustomEndpoint {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []CustomEndpoint{}
	}
	var items []CustomEndpoint
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []CustomEndpoint{}
	}
	return items
}

// ParseCustomMenuItems parses a JSON string into a slice of CustomMenuItem.
// Returns empty slice on empty/invalid input.
func ParseCustomMenuItems(raw string) []CustomMenuItem {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []CustomMenuItem{}
	}
	var items []CustomMenuItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []CustomMenuItem{}
	}
	return items
}

// CustomEndpoint represents an admin-configured API endpoint for quick copy.
type CustomEndpoint struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
}

// CustomMenuItem represents a user-configured custom menu entry.
type CustomMenuItem struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	IconSVG    string `json:"icon_svg"`
	URL        string `json:"url"`
	PageSlug   string `json:"page_slug,omitempty"`
	Visibility string `json:"visibility"` // "user" or "admin"
	SortOrder  int    `json:"sort_order"`
}

// ParseUserVisibleMenuItems parses custom menu items and filters out admin-only entries.
func ParseUserVisibleMenuItems(raw string) []CustomMenuItem {
	items := ParseCustomMenuItems(raw)
	filtered := make([]CustomMenuItem, 0, len(items))
	for _, item := range items {
		if item.Visibility != "admin" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
