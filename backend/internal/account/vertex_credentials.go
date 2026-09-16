// Vertex 凭据的历史字段选择和账号位置覆盖由账号模块唯一拥有。
package account

import (
	"encoding/json"
	"errors"
	"strings"
)

const VertexDefaultLocation = "us-central1"

func (a *Record) VertexLocation(model string) string {
	if a == nil {
		return VertexDefaultLocation
	}
	if model != "" && a.Credentials != nil {
		if raw, ok := a.Credentials["vertex_model_locations"].(map[string]any); ok {
			if loc, ok := raw[model].(string); ok && strings.TrimSpace(loc) != "" {
				return strings.TrimSpace(loc)
			}
		}
	}
	if v := strings.TrimSpace(a.GetCredential("location")); v != "" {
		return v
	}
	if v := strings.TrimSpace(a.GetCredential("vertex_location")); v != "" {
		return v
	}
	return VertexDefaultLocation
}
func VertexServiceAccountJSON(account *Record) ([]byte, error) {
	if account == nil || account.Credentials == nil {
		return nil, errors.New("service account credentials not configured")
	}

	if raw := strings.TrimSpace(account.GetCredential("service_account_json")); raw != "" {
		return []byte(raw), nil
	}
	if raw := strings.TrimSpace(account.GetCredential("service_account")); raw != "" {
		return []byte(raw), nil
	}
	if nested, ok := account.Credentials["service_account_json"].(map[string]any); ok {
		b, _ := json.Marshal(nested)
		return b, nil
	}
	if nested, ok := account.Credentials["service_account"].(map[string]any); ok {
		b, _ := json.Marshal(nested)
		return b, nil
	}
	return nil, errors.New("service_account_json not found in credentials")
}

// VertexProjectID 保留显式 project 优先；解析失败时返回原空值。
func (a *Record) VertexProjectID(parseProject func([]byte) (string, error)) string {
	if a == nil {
		return ""
	}
	if v := strings.TrimSpace(a.GetCredential("project_id")); v != "" {
		return v
	}
	raw, err := VertexServiceAccountJSON(a)
	if err != nil {
		return ""
	}
	value, err := parseProject(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
