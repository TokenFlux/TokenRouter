// Package anthropicfp 提供纯函数，用于清理转发到 Anthropic 上游前可能暴露的客户端指纹。
//
// 当前暴露 NormalizeDateline：它会把请求体中的 "Today's date is YYYY-MM-DD."
// 句子还原为规范 ASCII 形态，抹除部分客户端在检测到非官方 base URL 时注入的
// 3 bit 隐写信号（4 种撇号码点和日期分隔符变体）。
package anthropicfp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// datelineRegexes 匹配带指纹的 dateline 句子，覆盖野外观察到的 4 种撇号码点和两种日期分隔符。
// Go 的 RE2 正则不支持反向引用，因此这里用两个正则分别匹配 "-" 与 "/"，保证
// YYYY?MM?DD 中两个分隔符一致，避免误匹配 "Today's date is 2026-07/01." 这类混合分隔符，
// 也避免碰触 "Today is foo."、"His date is 2026-06-30." 这类用户自然文本。
var (
	datelineRegexHyphen = regexp.MustCompile(`Today(['’ʼʹ])s date is (\d{4})-(\d{2})-(\d{2})\.`)
	datelineRegexSlash  = regexp.MustCompile(`Today(['’ʼʹ])s date is (\d{4})/(\d{2})/(\d{2})\.`)
)

// systemReminderRegex 匹配 <system-reminder> 块。多轮对话后 dateline 常出现在该块中，
// 因此 messages[].content[] 只扫描这些标签内部，避免影响普通用户文本。
var systemReminderRegex = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// DatelineHit 记录单次归一化命中的指纹形态，便于观测。
type DatelineHit struct {
	// ApostropheVariant 为 "ascii"（U+0027）、"u2019"、"u02bc"、"u02b9" 之一。
	ApostropheVariant string
	// DateSeparator 是归一化前观察到的 "-" 或 "/"。
	DateSeparator string
}

// canonicalize 返回命中 dateline 句子的规范形态，固定使用 ASCII 撇号和短横线日期分隔符。
func canonicalize(year, month, day string) string {
	return fmt.Sprintf("Today's date is %s-%s-%s.", year, month, day)
}

func apostropheVariant(r rune) string {
	switch r {
	case '’':
		return "u2019"
	case 'ʼ':
		return "u02bc"
	case 'ʹ':
		return "u02b9"
	default:
		return "ascii"
	}
}

type datelineMatch struct {
	start, end       int
	apoRune          rune
	sep              string
	year, month, day string
}

func collectMatches(text string, re *regexp.Regexp, sep string) []datelineMatch {
	locs := re.FindAllStringSubmatchIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	out := make([]datelineMatch, 0, len(locs))
	for _, m := range locs {
		var apoRune rune
		for _, r := range text[m[2]:m[3]] {
			apoRune = r
			break
		}
		out = append(out, datelineMatch{
			start:   m[0],
			end:     m[1],
			apoRune: apoRune,
			sep:     sep,
			year:    text[m[4]:m[5]],
			month:   text[m[6]:m[7]],
			day:     text[m[8]:m[9]],
		})
	}
	return out
}

// NormalizeText 将文本中带指纹的 dateline 句子替换为规范形态。
// 未命中时返回原始字符串本身和 nil 命中列表。
func NormalizeText(text string) (string, []DatelineHit) {
	if !strings.Contains(text, "date is ") {
		return text, nil
	}
	matches := collectMatches(text, datelineRegexHyphen, "-")
	matches = append(matches, collectMatches(text, datelineRegexSlash, "/")...)
	if len(matches) == 0 {
		return text, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })

	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	hits := make([]DatelineHit, 0, len(matches))
	changed := false
	for _, m := range matches {
		full := text[m.start:m.end]
		canonical := canonicalize(m.year, m.month, m.day)
		if canonical == full {
			// 已经是规范形态时不记录命中。
			continue
		}
		_, _ = b.WriteString(text[prev:m.start])
		_, _ = b.WriteString(canonical)
		prev = m.end
		changed = true
		hits = append(hits, DatelineHit{
			ApostropheVariant: apostropheVariant(m.apoRune),
			DateSeparator:     m.sep,
		})
	}
	if !changed {
		return text, nil
	}
	_, _ = b.WriteString(text[prev:])
	return b.String(), hits
}

// normalizeSystemReminderScopedText 只扫描文本中的 <system-reminder> 块并归一化其中的 dateline。
// 块外文本逐字节保留，因此普通用户文本、tool_result、代码块或 shell 命令不会被误改。
func normalizeSystemReminderScopedText(text string) (string, []DatelineHit) {
	if !strings.Contains(text, "<system-reminder>") {
		return text, nil
	}
	locs := systemReminderRegex.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return text, nil
	}
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	var hits []DatelineHit
	changed := false
	for _, loc := range locs {
		_, _ = b.WriteString(text[prev:loc[0]])
		block := text[loc[0]:loc[1]]
		normalized, blockHits := NormalizeText(block)
		if normalized != block {
			changed = true
		}
		_, _ = b.WriteString(normalized)
		hits = append(hits, blockHits...)
		prev = loc[1]
	}
	if !changed {
		return text, nil
	}
	_, _ = b.WriteString(text[prev:])
	return b.String(), hits
}

// NormalizeDateline 扫描 Anthropic /v1/messages 请求体，将带指纹的 dateline 句子还原为规范 ASCII 形态。
//
// 作用范围与真实客户端放置 dateline 的位置保持一致：
//   - 顶层 `system` 字符串，或 `system` 文本块中的 `.text` 字段。
//   - `messages[i].content` 中位于 `<system-reminder>...</system-reminder>` 标签内的文本。
//
// 普通用户文本、tool_use.input、tool_result.content 和其它 block 类型不会扫描，
// 避免意外改写代码块、shell 命令或正常聊天内容。该函数为纯转换：不会修改输入切片；
// 无需改写时返回原始切片、nil 命中列表和 changed=false。
func NormalizeDateline(body []byte) ([]byte, []DatelineHit, bool) {
	if len(body) == 0 {
		return body, nil, false
	}
	out := body
	var hits []DatelineHit
	changed := false

	sys := gjson.GetBytes(out, "system")
	if sys.Exists() {
		switch {
		case sys.Type == gjson.String:
			normalized, sysHits := NormalizeText(sys.String())
			if normalized != sys.String() {
				if next, err := sjson.SetBytes(out, "system", normalized); err == nil {
					out = next
					changed = true
					hits = append(hits, sysHits...)
				}
			}
		case sys.IsArray():
			idx := 0
			sys.ForEach(func(_, item gjson.Result) bool {
				if item.Get("type").String() == "text" {
					t := item.Get("text")
					if t.Exists() && t.Type == gjson.String {
						normalized, textHits := NormalizeText(t.String())
						if normalized != t.String() {
							path := fmt.Sprintf("system.%d.text", idx)
							if next, err := sjson.SetBytes(out, path, normalized); err == nil {
								out = next
								changed = true
								hits = append(hits, textHits...)
							}
						}
					}
				}
				idx++
				return true
			})
		}
	}

	messages := gjson.GetBytes(out, "messages")
	if messages.IsArray() {
		msgIdx := -1
		messages.ForEach(func(_, msg gjson.Result) bool {
			msgIdx++
			content := msg.Get("content")
			if !content.Exists() {
				return true
			}
			switch {
			case content.Type == gjson.String:
				normalized, contentHits := normalizeSystemReminderScopedText(content.String())
				if normalized != content.String() {
					path := fmt.Sprintf("messages.%d.content", msgIdx)
					if next, err := sjson.SetBytes(out, path, normalized); err == nil {
						out = next
						changed = true
						hits = append(hits, contentHits...)
					}
				}
			case content.IsArray():
				contentIdx := -1
				content.ForEach(func(_, block gjson.Result) bool {
					contentIdx++
					if block.Get("type").String() != "text" {
						return true
					}
					t := block.Get("text")
					if !t.Exists() || t.Type != gjson.String {
						return true
					}
					normalized, textHits := normalizeSystemReminderScopedText(t.String())
					if normalized != t.String() {
						path := fmt.Sprintf("messages.%d.content.%d.text", msgIdx, contentIdx)
						if next, err := sjson.SetBytes(out, path, normalized); err == nil {
							out = next
							changed = true
							hits = append(hits, textHits...)
						}
					}
					return true
				})
			}
			return true
		})
	}

	if !changed {
		return body, nil, false
	}
	return out, hits, true
}
