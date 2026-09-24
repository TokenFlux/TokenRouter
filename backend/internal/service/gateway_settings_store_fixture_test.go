package service

import (
	"context"
	"errors"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

type gatewayTTLSettingRepo struct {
	data map[string]string
}

func (r *gatewayTTLSettingRepo) Get(context.Context, string) (*settingscore.Setting, error) {
	return nil, settingscore.ErrSettingNotFound
}

func (r *gatewayTTLSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r == nil {
		return "", settingscore.ErrSettingNotFound
	}
	v, ok := r.data[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return v, nil
}

func (r *gatewayTTLSettingRepo) Set(_ context.Context, key, value string) error {
	if r == nil {
		return errors.New("setting repo is nil")
	}
	if r.data == nil {
		r.data = map[string]string{}
	}
	r.data[key] = value
	return nil
}

func (r *gatewayTTLSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string)
	if r == nil {
		return result, nil
	}
	for _, key := range keys {
		if v, ok := r.data[key]; ok {
			result[key] = v
		}
	}
	return result, nil
}

func (r *gatewayTTLSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	if r == nil {
		return errors.New("setting repo is nil")
	}
	if r.data == nil {
		r.data = map[string]string{}
	}
	for key, value := range settings {
		r.data[key] = value
	}
	return nil
}

func (r *gatewayTTLSettingRepo) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string)
	if r == nil {
		return result, nil
	}
	for key, value := range r.data {
		result[key] = value
	}
	return result, nil
}

func (r *gatewayTTLSettingRepo) Delete(_ context.Context, key string) error {
	if r != nil {
		delete(r.data, key)
	}
	return nil
}
