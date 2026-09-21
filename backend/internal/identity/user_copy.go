package identity

// CopyUser 复制用户结构体，保留原投影边界的标量隔离。
// 关联切片、映射和指针沿用原引用；跨请求缓存仍由各自的深复制入口处理。
func CopyUser(user *User) *User {
	if user == nil {
		return nil
	}
	copy := *user
	return &copy
}
