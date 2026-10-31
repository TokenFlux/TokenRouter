package sdk

import (
	"sync"
	"testing"
)

// TestConfigPublishesImmutableSnapshots exercises concurrent reads while new
// snapshots replace the live handle. Callback snapshots remain unchanged, and
// integer access preserves values beyond float64's exact integer range.
func TestConfigPublishesImmutableSnapshots(t *testing.T) {
	current, err := NewConfig([]byte(`{"name":"old","integer":9007199254740993.0,"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if current.Int("integer") != 9007199254740993 {
		t.Fatal("integer precision lost")
	}
	next, err := NewConfig([]byte(`{"name":"new","integer":2,"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			for range 1000 {
				_ = current.String("name")
				_ = current.Bool("enabled")
				_ = current.Int("integer")
			}
		})
	}
	current.replace(next)
	workers.Wait()
	if current.String("name") != "new" || next.String("name") != "new" {
		t.Fatal("configuration publication failed")
	}
	last, err := NewConfig([]byte(`{"name":"last"}`))
	if err != nil {
		t.Fatal(err)
	}
	current.replace(last)
	if next.String("name") != "new" {
		t.Fatal("callback snapshot changed")
	}
	for _, data := range []string{"null", "[]", "{} {}", "bad"} {
		if _, err := NewConfig([]byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
}
