package transport

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeyManager_GenerateNewKey 测试密钥生成（RED）
func TestKeyManager_GenerateNewKey(t *testing.T) {
	// Given: 临时密钥文件
	keyFile := filepath.Join(os.TempDir(), "test-key.pem")
	defer os.Remove(keyFile)

	// When: 首次加载（文件不存在）
	km := NewKeyManager(keyFile)
	privKey, err := km.LoadOrGenerate()

	// Then: 应生成新密钥
	require.NoError(t, err)
	assert.NotNil(t, privKey)

	// And: 文件应被创建
	_, err = os.Stat(keyFile)
	require.NoError(t, err)
}

// TestKeyManager_LoadExistingKey 测试密钥加载
func TestKeyManager_LoadExistingKey(t *testing.T) {
	// Given: 已存在的密钥文件
	keyFile := filepath.Join(os.TempDir(), "test-existing-key.pem")
	km1 := NewKeyManager(keyFile)
	privKey1, err := km1.LoadOrGenerate()
	require.NoError(t, err)

	peerID1, _ := peer.IDFromPrivateKey(privKey1)

	// When: 再次加载
	km2 := NewKeyManager(keyFile)
	privKey2, err := km2.LoadOrGenerate()

	// Then: 应返回相同密钥
	require.NoError(t, err)
	peerID2, _ := peer.IDFromPrivateKey(privKey2)
	assert.Equal(t, peerID1, peerID2)

	os.Remove(keyFile)
}

// TestKeyManager_FileCorruptionHandling 测试文件损坏处理
func TestKeyManager_FileCorruptionHandling(t *testing.T) {
	// Given: 损坏的密钥文件
	keyFile := filepath.Join(os.TempDir(), "test-corrupt-key.pem")
	err := os.WriteFile(keyFile, []byte("corrupted data"), 0600)
	require.NoError(t, err)
	defer os.Remove(keyFile)

	// When: 尝试加载
	km := NewKeyManager(keyFile)
	privKey, err := km.LoadOrGenerate()

	// Then: 应生成新密钥（容错）
	require.NoError(t, err)
	assert.NotNil(t, privKey)
}

// TestKeyManager_FilePermissions 测试文件权限
func TestKeyManager_FilePermissions(t *testing.T) {
	// Given: 创建密钥
	keyFile := filepath.Join(os.TempDir(), "test-perm-key.pem")
	km := NewKeyManager(keyFile)
	_, err := km.LoadOrGenerate()
	require.NoError(t, err)
	defer os.Remove(keyFile)

	// When: 检查文件权限
	info, err := os.Stat(keyFile)
	require.NoError(t, err)

	// Then: 权限应为 0600
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

// TestKeyManager_ConcurrentAccess 测试并发访问
func TestKeyManager_ConcurrentAccess(t *testing.T) {
	// Given: 共享密钥文件
	keyFile := filepath.Join(os.TempDir(), "test-concurrent-key.pem")
	defer os.Remove(keyFile)

	// When: 并发加载
	km := NewKeyManager(keyFile)
	var wg sync.WaitGroup
	results := make([]peer.ID, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			privKey, err := km.LoadOrGenerate()
			require.NoError(t, err)
			pid, _ := peer.IDFromPrivateKey(privKey)
			results[idx] = pid
		}(i)
	}
	wg.Wait()

	// Then: 所有结果应一致
	firstPID := results[0]
	for _, pid := range results[1:] {
		assert.Equal(t, firstPID, pid)
	}
}
