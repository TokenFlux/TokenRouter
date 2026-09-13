package account

import (
	"context"
)

type ManagementCreationPrivacy interface {
	ForceAntigravityPrivacy(context.Context, *Record) string
	ForceOpenAIPrivacy(context.Context, *Record) string
}
type ManagementCreationOptions struct {
	Privacy     ManagementCreationPrivacy
	Background  func(string, func()) bool
	AfterCreate func(*Record)
	Error       func(string, ...any)
}
type ManagementCreationItem struct {
	Name    string
	ID      int64
	Success bool
	Error   string
}
type ManagementCreationResult struct {
	Success, Failed int
	Results         []ManagementCreationItem
}

// Create 保留原逐项创建、部分成功和两组异步隐私；幂等重放由 HTTP Adapter 控制。
func (s *ManagementBatch) Create(ctx context.Context, inputs []CreateAccountInput) (*ManagementCreationResult, error) {
	result := &ManagementCreationResult{Results: make([]ManagementCreationItem, 0, len(inputs))}
	var antigravity, openai []*Record
	for _, input := range inputs {
		if input.RateMultiplier != nil && *input.RateMultiplier < 0 {
			result.Failed++
			result.Results = append(result.Results, ManagementCreationItem{Name: input.Name, Error: "rate_multiplier must be >= 0"})
			continue
		}
		SanitizeManagedBaseRPM(input.Extra)
		if err := ValidateUpstreamRequestIDHeaderExtra(input.Extra); err != nil {
			result.Failed++
			result.Results = append(result.Results, ManagementCreationItem{Name: input.Name, Error: err.Error()})
			continue
		}
		value, err := s.store.CreateAccount(ctx, &input)
		if err != nil {
			result.Failed++
			result.Results = append(result.Results, ManagementCreationItem{Name: input.Name, Error: err.Error()})
			continue
		}
		if value.Type == AccountTypeOAuth {
			switch value.Platform {
			case PlatformAntigravity:
				antigravity = append(antigravity, CloneRecord(value))
			case PlatformOpenAI:
				openai = append(openai, CloneRecord(value))
			}
		}
		if s.creation.AfterCreate != nil {
			s.creation.AfterCreate(value)
		}
		result.Success++
		result.Results = append(result.Results, ManagementCreationItem{Name: input.Name, ID: value.ID, Success: true})
	}
	s.scheduleCreationPrivacy(antigravity, PlatformAntigravity)
	s.scheduleCreationPrivacy(openai, PlatformOpenAI)
	return result, nil
}

// 排队仍复用 app 的完成屏障；隐私实际请求另由同一个 PrivacyService 接管取消与等待。
func (s *ManagementBatch) scheduleCreationPrivacy(values []*Record, platform string) {
	if len(values) == 0 || s.creation.Background == nil {
		return
	}
	s.creation.Background("handler/admin/account_handler.go:BatchCreate", func() {
		defer func() {
			if r := recover(); r != nil {
				event := "batch_create_openai_privacy_panic"
				if platform == PlatformAntigravity {
					event = "batch_create_antigravity_privacy_panic"
				}
				if s.creation.Error != nil {
					s.creation.Error(event, "recover", r)
				}
			}
		}()
		for _, value := range values {
			if platform == PlatformAntigravity {
				s.creation.Privacy.ForceAntigravityPrivacy(context.Background(), value)
			} else {
				s.creation.Privacy.ForceOpenAIPrivacy(context.Background(), value)
			}
		}
	})
}
