package openai

import (
	"regexp"
	"strings"
)

// CodexCLIUserAgentPrefixes 定义历史 Codex CLI User-Agent 前缀。
// 示例："codex_vscode/1.0.0"、"codex_cli_rs/0.1.2"。
var CodexCLIUserAgentPrefixes = []string{
	"codex_vscode/",
	"codex_cli_rs/",
}

// CodexOfficialClientUserAgentPrefixes 定义 Codex 官方客户端家族 User-Agent 确定前缀。
// `Codex ` 家族前缀需要保留尾随空格，单独由 codexOfficialClientFamilyPrefix 处理。
var CodexOfficialClientUserAgentPrefixes = []string{
	"codex_cli_rs/",
	"codex-tui/",
	"codex_vscode/",
	"codex_vscode_copilot/",
	"codex_app/",
	"codex_chatgpt_desktop/",
	"codex_atlas/",
	"codex_exec/",
	"codex_sdk_ts/",
}

// codexOfficialClientFamilyPrefix 覆盖 Codex Desktop 等 `Codex ` 家族标识。
// 该值不能进入通用前缀列表，否则归一化会移除尾随空格并退化成裸 codex。
const codexOfficialClientFamilyPrefix = "codex "

// codexOfficialClientOriginators 定义 Codex 官方客户端家族 originator 精确集合。
// 精确匹配可避免 evil-codex_cli、my_codex_thing 等伪造值绕过 codex_only。
var codexOfficialClientOriginators = map[string]bool{
	"codex_cli_rs":          true,
	"codex-tui":             true,
	"codex_vscode":          true,
	"codex_vscode_copilot":  true,
	"codex_app":             true,
	"codex_chatgpt_desktop": true,
	"codex_atlas":           true,
	"codex_exec":            true,
	"codex_sdk_ts":          true,
}

// IsBrowserUserAgent 判断 User-Agent 是否来自浏览器（Chrome/Firefox/Safari/Edge/Opera 等）。
// 所有现代浏览器的 UA 均以 "Mozilla/" 作为前缀，CLI 工具（codex/claude/curl/postman/python-requests 等）不会。
// 该判定用于避免 Cloudflare 对浏览器型 UA 在 OpenAI 上游接口上触发 JS 质询。
func IsBrowserUserAgent(userAgent string) bool {
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(ua), "mozilla/")
}

// IsCodexCLIRequest 判断 User-Agent 是否指向历史 Codex CLI 请求。
func IsCodexCLIRequest(userAgent string) bool {
	ua := normalizeCodexClientHeader(userAgent)
	if ua == "" {
		return false
	}
	return matchCodexClientHeaderPrefixes(ua, CodexCLIUserAgentPrefixes)
}

// IsCodexOfficialClientRequest 判断 User-Agent 是否指向 Codex 官方客户端请求。
// 宽松版保留历史 contains 兜底，供透传等兼容路径使用。
func IsCodexOfficialClientRequest(userAgent string) bool {
	return isCodexOfficialClientRequest(userAgent, false)
}

// IsCodexOfficialClientRequestStrict 判断 User-Agent 是否严格指向 Codex 官方客户端请求。
// strict 版只接受官方 UA 前缀或可信尾部兜底，专供 codex_only 访问限制使用。
func IsCodexOfficialClientRequestStrict(userAgent string) bool {
	return isCodexOfficialClientRequest(userAgent, true)
}

func isCodexOfficialClientRequest(userAgent string, strict bool) bool {
	ua := normalizeCodexClientHeader(userAgent)
	if ua == "" {
		return false
	}
	if strict {
		if matchCodexClientHeaderStrictPrefixes(ua, CodexOfficialClientUserAgentPrefixes) {
			return true
		}
	} else if matchCodexClientHeaderPrefixes(ua, CodexOfficialClientUserAgentPrefixes) {
		return true
	}
	if strings.HasPrefix(ua, codexOfficialClientFamilyPrefix) {
		return true
	}
	if name := codexUATrailerName(ua); name != "" {
		return IsCodexOfficialClientOriginator(name)
	}
	return false
}

// codexUATrailerName 从 codex-rs 形态 UA 的最后一个括号组提取 clientInfo.name。
// CODEX_INTERNAL_ORIGINATOR_OVERRIDE 修改 UA 前缀（originator 段），但不修改尾部的
// `(name; version)` 括号组——该组由 codex-rs engine 写入，保留真实 clientInfo.name。
// 故从尾部提取 name 可以恢复被 override 的真实客户端标识（例如 cccc → codex-tui）。
//
// input 应为去首尾空格的 UA；本函数本身大小写无关，大小写由调用方按需处理
// （isCodexOfficialClientRequest 传入已小写化的 UA 做匹配；PairCodexClientIdentity
// 传入原始大小写以保留 originator 的真实大小写）。
// 若无法解析则返回空字符串。
func codexUATrailerName(ua string) string {
	last := strings.LastIndex(ua, "(")
	if last < 0 {
		return ""
	}
	rest := ua[last+1:]
	closeIdx := strings.Index(rest, ")")
	if closeIdx < 0 {
		return ""
	}
	inner := strings.TrimSpace(rest[:closeIdx])
	if semi := strings.Index(inner, ";"); semi >= 0 {
		inner = strings.TrimSpace(inner[:semi])
	}
	return inner
}

// IsCodexOfficialClientOriginator 判断 originator 是否指向 Codex 官方客户端请求。
// 精确集合之外仅保留 `Codex ` 家族前缀，避免任意 codex_* 伪造值绕过。
func IsCodexOfficialClientOriginator(originator string) bool {
	v := normalizeCodexClientHeader(originator)
	if v == "" {
		return false
	}
	if codexOfficialClientOriginators[v] {
		return true
	}
	return strings.HasPrefix(v, codexOfficialClientFamilyPrefix)
}

// IsCodexOfficialClientByHeaders 判断请求头是否指向 Codex 官方客户端家族。
func IsCodexOfficialClientByHeaders(userAgent, originator string) bool {
	return IsCodexOfficialClientRequest(userAgent) || IsCodexOfficialClientOriginator(originator)
}

func normalizeCodexClientHeader(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func matchCodexClientHeaderPrefixes(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		normalizedPrefix := normalizeCodexClientHeader(prefix)
		if normalizedPrefix == "" {
			continue
		}
		// 优先前缀匹配；若 UA/Originator 被网关拼接为复合字符串时，退化为包含匹配。
		if strings.HasPrefix(value, normalizedPrefix) || strings.Contains(value, normalizedPrefix) {
			return true
		}
	}
	return false
}

// matchCodexClientHeaderStrictPrefixes 仅进行前缀匹配，不使用 contains 历史兜底。
func matchCodexClientHeaderStrictPrefixes(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		normalizedPrefix := normalizeCodexClientHeader(prefix)
		if normalizedPrefix == "" {
			continue
		}
		if strings.HasPrefix(value, normalizedPrefix) {
			return true
		}
	}
	return false
}

// PairCodexClientIdentity 由最终出站 User-Agent 推导与其配套的 originator，必要时归一化
// UA 首段，保证两者一致。上游 /backend-api/codex 会校验 originator 与 UA 首段（首个 '/'
// 之前的 client 名）是否配套，错配（如 originator=codex_cli_rs + UA=codex-tui/...）一律
// 404（issue #3901，2026-07 实测）。
//
// 推导优先级：
//  1. UA 首段是官方 originator（精确集合或 `Codex ` 家族前缀）→ 直接配对，UA 原样保留；
//  2. UA 尾部括号组 `(name; version)` 的 name 是官方 originator——CODEX_INTERNAL_ORIGINATOR_OVERRIDE
//     只改 UA 前缀不改尾部（如 cccc/0.142.0 ... (codex-tui; 0.142.0)）→ 用尾部 name 重写
//     UA 首段后配对，保留真实版本/OS/终端指纹；
//  3. 均不命中 → ok=false，调用方应整体回退为默认官方身份。
func PairCodexClientIdentity(userAgent string) (originator string, pairedUA string, ok bool) {
	ua := strings.TrimSpace(userAgent)
	slash := strings.IndexByte(ua, '/')
	if slash <= 0 {
		return "", "", false
	}
	if leading := strings.TrimSpace(ua[:slash]); isSaneCodexOriginator(leading) && IsCodexOfficialClientOriginator(leading) {
		leading = canonicalizeCodexOriginator(leading)
		return leading, leading + ua[slash:], true
	}
	// 传原始大小写 UA 提取 trailer，保留 `Codex ` 家族身份的真实大小写；含 '/' 的
	// trailer 会破坏重写后 UA 首段与 originator 的一致性，直接拒绝。
	if trailer := codexUATrailerName(ua); trailer != "" && !strings.ContainsRune(trailer, '/') &&
		isSaneCodexOriginator(trailer) && IsCodexOfficialClientOriginator(trailer) {
		trailer = canonicalizeCodexOriginator(trailer)
		return trailer, trailer + ua[slash:], true
	}
	return "", "", false
}

// codexOriginatorMaxLen 官方 clientInfo.name 均为短 ASCII 标识，远低于此上限。
const codexOriginatorMaxLen = 64

// isSaneCodexOriginator 拒绝超长或含不可打印/非 ASCII 字节的候选 originator，
// 避免 `Codex ` 家族宽前缀把客户端可控的任意字节当作官方身份逐字转发给上游。
func isSaneCodexOriginator(name string) bool {
	if name == "" || len(name) > codexOriginatorMaxLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// canonicalizeCodexOriginator 把精确集合的官方 originator 大小写变体归一为规范小写形态
// （如 CODEX_CLI_RS → codex_cli_rs）；`Codex ` 家族不在精确集合中，保留原大小写
// （其规范形态本就是混合大小写，上游按大小写敏感 starts_with("Codex ") 判定）。
func canonicalizeCodexOriginator(name string) string {
	if lower := normalizeCodexClientHeader(name); codexOfficialClientOriginators[lower] {
		return lower
	}
	return name
}

// codexEngineVersionPattern 提取版本段开头的三段数字 X.Y.Z（忽略 -alpha 等后缀）。
var codexEngineVersionPattern = regexp.MustCompile(`^(\d+\.\d+\.\d+)`)

// ParseCodexEngineVersion 从 codex-rs 形态 UA 取引擎版本：
// `{originator}/{X.Y.Z} (...)`，第一个 '/' 后、首个空格或 '(' 前的三段版本。
// 该版本是 codex-rs CARGO_PKG_VERSION（引擎版本，CLI/app-server 一致）。
func ParseCodexEngineVersion(ua string) (string, bool) {
	ua = strings.TrimSpace(ua)
	slash := strings.IndexByte(ua, '/')
	if slash < 0 {
		return "", false
	}
	rest := ua[slash+1:]
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == ' ' || rest[i] == '(' {
			end = i
			break
		}
	}
	m := codexEngineVersionPattern.FindString(strings.TrimSpace(rest[:end]))
	if m == "" {
		return "", false
	}
	return m, true
}
