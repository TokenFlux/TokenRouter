package repository

import (
	"strings"
	"testing"
	"unicode"

	"github.com/BrandonVee/TokenRouter/migrations"
	"github.com/stretchr/testify/require"
)

// TestNormalizedUserEmailSQLMatchesMigrationIndex 防止查询与函数索引的归一化表达式发生漂移。
func TestNormalizedUserEmailSQLMatchesMigrationIndex(t *testing.T) {
	content, err := migrations.FS.ReadFile("220_users_registration_email_normalized_index_notx.sql")
	require.NoError(t, err)

	stripWhitespace := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, value)
	}
	compactMigration := stripWhitespace(string(content))
	compactQueryExpression := stripWhitespace(normalizedUserEmailSQL)
	require.Contains(t, compactMigration, compactQueryExpression)
}
