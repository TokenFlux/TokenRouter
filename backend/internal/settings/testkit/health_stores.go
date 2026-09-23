//go:build unit

package testkit

import (
	"context"
	"sync"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// Memory 在互斥保护下提供原设置替身的读写与计数。
type Memory struct {
	Mu            sync.Mutex
	Data          map[string]string
	GetValueErr   error
	GetValueCalls int
}

func NewMemory() *Memory {
	return &Memory{Data: make(map[string]string)}
}

func (m *Memory) Get(_ context.Context, key string) (*settingscore.Setting, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	v, ok := m.Data[key]
	if !ok {
		return nil, settingscore.ErrSettingNotFound
	}
	return &settingscore.Setting{Key: key, Value: v}, nil
}

func (m *Memory) GetValue(_ context.Context, key string) (string, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.GetValueCalls++
	if m.GetValueErr != nil {
		return "", m.GetValueErr
	}
	v, ok := m.Data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

func (m *Memory) Set(_ context.Context, key, value string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Data[key] = value
	return nil
}

func (m *Memory) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	result := make(map[string]string)
	for _, k := range keys {
		if v, ok := m.Data[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

func (m *Memory) SetMultiple(_ context.Context, settings map[string]string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	for k, v := range settings {
		m.Data[k] = v
	}
	return nil
}

func (m *Memory) GetAll(_ context.Context) (map[string]string, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	result := make(map[string]string, len(m.Data))
	for k, v := range m.Data {
		result[k] = v
	}
	return result, nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	delete(m.Data, key)
	return nil
}
