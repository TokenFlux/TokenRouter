package repository

import accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

// 原 SQL 契约测试委托新存储表达式，测试夹具不保留第二份算法。
const ollamaCloudBaseURLRegexSQL = accountpostgres.OllamaCloudBaseURLRegexSQL

func ollamaCloudBaseURLMatchesSQL(expression string) string {
	return accountpostgres.OllamaCloudBaseURLMatchesSQL(expression)
}
