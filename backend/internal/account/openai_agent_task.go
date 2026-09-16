// Agent Identity 的任务登记、锁内复查和凭据写入属于账号用例；供应商交换通过端口执行。
package account

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type OpenAITaskOptions struct {
	Read          func(context.Context, int64) (*Record, error)
	Register      func(context.Context, *Record) (string, error)
	Persist       func(context.Context, *Record, map[string]any) error
	Invalidate    func(int64)
	FallbackMutex *sync.Mutex
}

// OpenAITaskCoordinator 延续原进程内按账号共享锁，不增加跨进程协调协议。
type OpenAITaskCoordinator struct{ locks sync.Map }

var sharedOpenAITaskCoordinator OpenAITaskCoordinator

// SharedOpenAITaskCoordinator 让装配和兼容消费者引用同一协调实例。
func SharedOpenAITaskCoordinator() *OpenAITaskCoordinator {
	return &sharedOpenAITaskCoordinator
}

// openAITaskCopyCredentials 保留原任务更新的浅复制边界。
func openAITaskCopyCredentials(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func (s *OpenAITaskCoordinator) Ensure(ctx context.Context, options OpenAITaskOptions, value *Record, expectedTaskID string) error {
	taskMu := options.FallbackMutex
	if value == nil || !value.IsOpenAIAgentIdentity() {
		return nil
	}
	credAccount := value
	if value.IsCredentialShadow() {
		resolved, err := ResolveCredentialRecord(ctx, options.Read, value)
		if err != nil {
			return err
		}
		credAccount = resolved
	}
	if credAccount == nil || !credAccount.IsOpenAIAgentIdentity() {
		return errors.New("agent identity credentials are unavailable")
	}
	currentTaskID := strings.TrimSpace(credAccount.GetCredential("task_id"))
	if currentTaskID != "" && (expectedTaskID == "" || currentTaskID != expectedTaskID) {
		return nil
	}
	if taskMu == nil {
		return errors.New("agent identity task lock is unavailable")
	}
	sharedTaskMu := taskMu
	if credAccount.ID > 0 {
		candidate := &sync.Mutex{}
		actual, _ := s.locks.LoadOrStore(credAccount.ID, candidate)
		loadedTaskMu, ok := actual.(*sync.Mutex)
		if !ok {
			return errors.New("agent identity task lock has invalid type")
		}
		sharedTaskMu = loadedTaskMu
	}
	sharedTaskMu.Lock()
	defer sharedTaskMu.Unlock()
	// 共享锁内重新读取账号，避免不同请求持有旧快照时依次重复注册 task。
	if options.Read != nil && credAccount.ID > 0 {
		if refreshed, refreshErr := options.Read(ctx, credAccount.ID); refreshErr == nil && refreshed != nil {
			if refreshed.IsCredentialShadow() {
				if resolved, resolveErr := ResolveCredentialRecord(ctx, options.Read, refreshed); resolveErr == nil && resolved != nil {
					refreshed = resolved
				}
			}
			if refreshed.IsOpenAIAgentIdentity() {
				credAccount = refreshed
				if !value.IsCredentialShadow() {
					value.Credentials = openAITaskCopyCredentials(credAccount.Credentials)
				}
			}
		}
	}
	currentTaskID = strings.TrimSpace(credAccount.GetCredential("task_id"))
	if currentTaskID != "" && (expectedTaskID == "" || currentTaskID != expectedTaskID) {
		return nil
	}
	newTaskID, err := options.Register(ctx, credAccount)
	if err != nil {
		return err
	}
	credentials := make(map[string]any, len(credAccount.Credentials)+1)
	for key, value := range credAccount.Credentials {
		credentials[key] = value
	}
	credentials["task_id"] = newTaskID
	if err := options.Persist(ctx, credAccount, credentials); err != nil {
		return err
	}
	if !value.IsCredentialShadow() && value != credAccount {
		value.Credentials = openAITaskCopyCredentials(credAccount.Credentials)
	}
	if options.Invalidate != nil {
		options.Invalidate(credAccount.ID)
	}
	return nil
}
