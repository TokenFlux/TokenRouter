//go:build integration

package billing_test

import (
	"fmt"

	"github.com/google/uuid"
)

func uniqueTeamTestEmail(prefix string) string {
	return fmt.Sprintf("team-%s-%s@example.com", prefix, uuid.NewString())
}
