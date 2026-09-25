// Package testkit 提供会话合同使用的存储替身，不接入生产调用链。
package testkit

import (
	"context"
	"errors"
	"time"
)

type StickyCache struct {
	SessionBindings map[string]int64
	DeletedSessions map[string]int
}

func (c *StickyCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := c.SessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}
func (c *StickyCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if c.SessionBindings == nil {
		c.SessionBindings = make(map[string]int64)
	}
	c.SessionBindings[sessionHash] = accountID
	return nil
}
func (c *StickyCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}
func (c *StickyCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	if c.SessionBindings == nil {
		return nil
	}
	if c.DeletedSessions == nil {
		c.DeletedSessions = make(map[string]int)
	}
	c.DeletedSessions[sessionHash]++
	delete(c.SessionBindings, sessionHash)
	return nil
}
func (c *StickyCache) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	return true, nil
}
func (c *StickyCache) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	return 0, errors.New("not found")
}
func (c *StickyCache) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	return nil
}
