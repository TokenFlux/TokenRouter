//go:build integration && !unit

package payment_test

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(runPostgresTests(m))
}
