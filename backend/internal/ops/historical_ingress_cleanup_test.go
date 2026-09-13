package ops

import "testing"

func TestHistoricalIngressRejectReason(t *testing.T) {
	tests := []struct {
		name   string
		item   HistoricalIngressCandidate
		reason string
		match  bool
	}{
		{name: "standard invalid key", item: HistoricalIngressCandidate{Body: `{"code":"INVALID_API_KEY","message":"Invalid API key"}`}, reason: "invalid_key", match: true},
		{name: "google missing key", item: HistoricalIngressCandidate{Body: `{"error":{"code":401,"message":"API key is required","status":"UNAUTHENTICATED"}}`}, reason: "missing_key", match: true},
		{name: "google group deleted", item: HistoricalIngressCandidate{Body: `{"error":{"code":403,"message":"API Key 所属分组已删除","status":"PERMISSION_DENIED"}}`}, reason: "group_deleted", match: true},
		{name: "ip acl", item: HistoricalIngressCandidate{Body: `{"code":"ACCESS_DENIED","message":"Access denied. Your IP is 192.0.2.1"}`}, reason: "ip_acl_denied", match: true},
		{name: "user not found remains", item: HistoricalIngressCandidate{Body: `{"code":"USER_NOT_FOUND","message":"User associated with API key not found"}`}, match: false},
		{name: "quota remains", item: HistoricalIngressCandidate{Body: `{"code":"API_KEY_QUOTA_EXHAUSTED","message":"quota"}`}, match: false},
		{name: "database failure remains", item: HistoricalIngressCandidate{StatusCode: 500, Message: "Failed to validate API key", Body: `{"code":"INTERNAL_ERROR","message":"Failed to validate API key"}`}, match: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, ok := historicalIngressRejectReason(tt.item)
			if ok != tt.match || reason != tt.reason {
				t.Fatalf("got (%q, %v), want (%q, %v)", reason, ok, tt.reason, tt.match)
			}
		})
	}
}
