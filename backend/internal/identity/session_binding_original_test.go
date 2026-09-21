package identity

import (
	"testing"
)

func TestSessionBindingHash(t *testing.T) {
	a := &SessionBinding{IP: "1.2.3.4", UserAgent: "Mozilla/5.0"}
	b := &SessionBinding{IP: "1.2.3.4", UserAgent: "Mozilla/5.0"}
	if a.Hash() != b.Hash() {
		t.Fatalf("identical bindings must hash equal")
	}
	if a.Hash() == "" {
		t.Fatalf("non-empty binding must produce non-empty hash")
	}

	// IP 变化 → 哈希变化。
	c := &SessionBinding{IP: "5.6.7.8", UserAgent: "Mozilla/5.0"}
	if a.Hash() == c.Hash() {
		t.Fatalf("changing IP must change hash")
	}
	// UA 变化 → 哈希变化。
	d := &SessionBinding{IP: "1.2.3.4", UserAgent: "curl/8.0"}
	if a.Hash() == d.Hash() {
		t.Fatalf("changing UA must change hash")
	}

	// 空指纹 → 空哈希（旧 token 兼容）。
	empty := &SessionBinding{}
	if empty.Hash() != "" {
		t.Fatalf("empty binding must hash to empty string")
	}
	var nilBinding *SessionBinding
	if nilBinding.Hash() != "" {
		t.Fatalf("nil binding must hash to empty string")
	}
}
