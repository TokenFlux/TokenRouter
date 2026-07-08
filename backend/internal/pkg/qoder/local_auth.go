package qoder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultAuthDir 返回默认的 Qoder 认证目录。
func DefaultAuthDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".qoder", ".auth")
}

// ReadLocalAuth 从本地文件系统读取并解密 Qoder 认证数据。
func ReadLocalAuth(authDir string) (*AuthInfo, error) {
	if authDir == "" {
		authDir = DefaultAuthDir()
	}

	machineID, err := os.ReadFile(filepath.Join(authDir, "machine_id"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read machine_id: %w", err)
	}
	machineID = []byte(string(machineID))[:len(machineID)]
	// 去掉结尾空白和换行。
	for len(machineID) > 0 && (machineID[len(machineID)-1] == '\n' || machineID[len(machineID)-1] == '\r') {
		machineID = machineID[:len(machineID)-1]
	}

	userB64, err := os.ReadFile(filepath.Join(authDir, "user"))
	if err != nil {
		return nil, fmt.Errorf("qoder: read user: %w", err)
	}
	// 去掉结尾空白。
	for len(userB64) > 0 && (userB64[len(userB64)-1] == '\n' || userB64[len(userB64)-1] == '\r') {
		userB64 = userB64[:len(userB64)-1]
	}

	ciphertext, err := base64.StdEncoding.DecodeString(string(userB64))
	if err != nil {
		return nil, fmt.Errorf("qoder: decode user base64: %w", err)
	}

	// key 取 machine_id 的前 16 个 ASCII 字节。
	key := make([]byte, 16)
	copy(key, machineID)

	plaintext, err := AESDecrypt(ciphertext, key)
	if err != nil {
		return nil, fmt.Errorf("qoder: decrypt user: %w", err)
	}

	var info AuthInfo
	if err := json.Unmarshal(plaintext, &info); err != nil {
		return nil, fmt.Errorf("qoder: parse user JSON: %w", err)
	}
	info.MachineID = string(machineID)

	return &info, nil
}

// LoadLocalIdentity 从本地 Qoder 认证数据加载身份。
func LoadLocalIdentity(authDir string) (*AuthIdentity, *MachineIdentity, error) {
	info, err := ReadLocalAuth(authDir)
	if err != nil {
		return nil, nil, err
	}

	identity := info.ToAuthIdentity()
	machine := &MachineIdentity{
		MachineID:    info.MachineID,
		MachineToken: RandomToken(50),
		MachineType:  RandomHex(18),
	}

	return identity, machine, nil
}
