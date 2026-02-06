package transport

import (
	crand "crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p/core/crypto"
)

// KeyManager 密钥管理器
type KeyManager struct {
	keyPath string
	mu      sync.Mutex
}

// NewKeyManager 创建密钥管理器
func NewKeyManager(keyPath string) *KeyManager {
	return &KeyManager{keyPath: keyPath}
}

// LoadOrGenerate 加载或生成密钥
func (km *KeyManager) LoadOrGenerate() (crypto.PrivKey, error) {
	km.mu.Lock()
	defer km.mu.Unlock()

	// 确保密钥目录存在
	if err := os.MkdirAll(filepath.Dir(km.keyPath), 0700); err != nil {
		return nil, fmt.Errorf("创建密钥目录失败: %w", err)
	}

	// 尝试加载
	if privKey, err := km.load(); err == nil {
		// 验证文件权限
		km.checkAndFixPermissions()
		return privKey, nil
	}

	// 生成新密钥
	privKey, _, err := crypto.GenerateEd25519Key(crand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成密钥失败: %w", err)
	}

	// 保存
	if err := km.save(privKey); err != nil {
		return nil, fmt.Errorf("保存密钥失败: %w", err)
	}

	return privKey, nil
}

// save 保存私钥到文件
func (km *KeyManager) save(privKey crypto.PrivKey) error {
	// 使用 libp2p 标准函数保存
	keyBytes, err := crypto.MarshalPrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("序列化私钥失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(km.keyPath, keyBytes, 0600); err != nil {
		return fmt.Errorf("写入密钥文件失败: %w", err)
	}

	return nil
}

// load 从文件加载私钥
func (km *KeyManager) load() (crypto.PrivKey, error) {
	// 检查文件是否存在
	if _, err := os.Stat(km.keyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("密钥文件不存在: %s", km.keyPath)
	}

	// 读取密钥文件
	keyBytes, err := os.ReadFile(km.keyPath)
	if err != nil {
		return nil, fmt.Errorf("读取密钥文件失败: %w", err)
	}

	// 解析私钥
	privKey, err := crypto.UnmarshalPrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}

	return privKey, nil
}

// checkAndFixPermissions 检查并修复文件权限
func (km *KeyManager) checkAndFixPermissions() error {
	info, err := os.Stat(km.keyPath)
	if err != nil {
		return err
	}

	// 检查权限
	perm := info.Mode().Perm()
	if perm != 0600 {
		// 记录警告
		fmt.Printf("警告：密钥文件权限不安全 (%o)，正在修复为 0600\n", perm)
		// 修复权限
		return os.Chmod(km.keyPath, 0600)
	}

	return nil
}

// ExpandPath 展开路径中的波浪号和环境变量
func (km *KeyManager) ExpandPath(path string) string {
	// 展开波浪号
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(homeDir, path[2:])
		}
	}

	// 展开环境变量
	path = os.ExpandEnv(path)

	return path
}
