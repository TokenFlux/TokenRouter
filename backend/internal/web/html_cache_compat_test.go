//go:build embed

package web

// Set 为既有缓存测试设置当前快照；生产调用必须使用 Publish。
func (c *HTMLCache) Set(html, settingsJSON []byte) {
	_, version := c.Snapshot()
	c.Publish(version, html, settingsJSON)
}
