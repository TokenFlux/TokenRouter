// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

const (
	OllamaCloudBaseURLRegexSQL       = `^[hH][tT][tT][pP][sS]://([wW][wW][wW]\.)?[oO][lL][lL][aA][mM][aA]\.[cC][oO][mM](:443)?(/v1)?$`
	OllamaCloudBaseURLMatchSQLPrefix = "btrim("
	OllamaCloudBaseURLMatchSQLSuffix = ") ~ '" + OllamaCloudBaseURLRegexSQL + "'"
	OllamaCloudUsageEligibleSQL      = `
	platform IN ('openai', 'anthropic')
	AND type = 'apikey'
	AND ` + OllamaCloudBaseURLMatchSQLPrefix + `credentials ->> 'base_url'` + OllamaCloudBaseURLMatchSQLSuffix + `
	AND jsonb_typeof(credentials -> 'api_key') = 'string'
`
)

func OllamaCloudBaseURLMatchesSQL(expression string) string {
	return OllamaCloudBaseURLMatchSQLPrefix + expression + OllamaCloudBaseURLMatchSQLSuffix
}
