package service

import "context"

type runtimeSettingRepoStub struct {
	values           map[string]string
	deleted          map[string]bool
	setCalls         int
	getValueCalls    int
	getMultipleCalls int
	getValueFn       func(key string) (string, error)
	setFn            func(key, value string) error
	deleteFn         func(key string) error
}

func newRuntimeSettingRepoStub() *runtimeSettingRepoStub {
	return &runtimeSettingRepoStub{
		values:  map[string]string{},
		deleted: map[string]bool{},
	}
}
func (s *runtimeSettingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	value, err := s.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: value}, nil
}
func (s *runtimeSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.getValueCalls++
	if s.getValueFn != nil {
		return s.getValueFn(key)
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}
func (s *runtimeSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.setFn != nil {
		if err := s.setFn(key, value); err != nil {
			return err
		}
	}
	s.values[key] = value
	s.setCalls++
	return nil
}
func (s *runtimeSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.getMultipleCalls++
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
func (s *runtimeSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}
func (s *runtimeSettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}
func (s *runtimeSettingRepoStub) Delete(_ context.Context, key string) error {
	if s.deleteFn != nil {
		if err := s.deleteFn(key); err != nil {
			return err
		}
	}
	if _, ok := s.values[key]; !ok {
		return ErrSettingNotFound
	}
	delete(s.values, key)
	s.deleted[key] = true
	return nil
}
