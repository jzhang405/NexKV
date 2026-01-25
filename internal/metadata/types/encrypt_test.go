// Package types 加密功能测试
package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========================================
// EncryptionType 类型测试
// ========================================

func TestEncryptionType_String(t *testing.T) {
	testCases := []struct {
		name          string
		encType       EncryptionType
		expectedName  string
	}{
		{"None", EncryptionTypeNone, "none"},
		{"AES256CBC", EncryptionTypeAES256CBC, "AES-256-CBC"},
		{"AES256GCM", EncryptionTypeAES256GCM, "AES-256-GCM"},
		{"ChaCha20Poly1305", EncryptionTypeChaCha20Poly1305, "ChaCha20-Poly1305"},
		{"Unknown", EncryptionType(999), "unknown"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedName, tc.encType.String())
		})
	}
}

func TestEncryptionType_Validate(t *testing.T) {
	testCases := []struct {
		name        string
		encType     EncryptionType
		shouldError bool
	}{
		{"None_Valid", EncryptionTypeNone, false},
		{"AES256CBC_Valid", EncryptionTypeAES256CBC, false},
		{"AES256GCM_Valid", EncryptionTypeAES256GCM, false},
		{"ChaCha20Poly1305_Valid", EncryptionTypeChaCha20Poly1305, false},
		{"Unknown_Invalid", EncryptionType(999), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.encType.Validate()
			if tc.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ========================================
// NoEncryption 测试
// ========================================

func TestNoEncryption_Name(t *testing.T) {
	enc := &NoEncryption{}
	assert.Equal(t, "none", enc.Name())
}

func TestNoEncryption_EncryptDecrypt(t *testing.T) {
	enc := &NoEncryption{}
	plaintext := []byte("hello world")

	ctx := context.Background()

	// 加密
	ciphertext, err := enc.Encrypt(ctx, plaintext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, ciphertext, "NoEncryption 应该直接返回明文")

	// 解密
	decrypted, err := enc.Decrypt(ctx, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestNoEncryption_EmptyData(t *testing.T) {
	enc := &NoEncryption{}
	ctx := context.Background()

	// 空数据
	ciphertext, err := enc.Encrypt(ctx, []byte{})
	require.NoError(t, err)
	assert.Empty(t, ciphertext)

	decrypted, err := enc.Decrypt(ctx, []byte{})
	require.NoError(t, err)
	assert.Empty(t, decrypted)
}

// ========================================
// AESEncryption (AES-256-CBC) 测试
// ========================================

func TestNewAESEncryption_ValidKey(t *testing.T) {
	key := make([]byte, 32) // 32字节密钥
	enc, err := NewAESEncryption(key)
	require.NoError(t, err)
	assert.NotNil(t, enc)
	assert.Equal(t, "AES-256-CBC", enc.Name())
}

func TestNewAESEncryption_InvalidKey(t *testing.T) {
	testCases := []struct {
		name        string
		key         []byte
		expectedErr string
	}{
		{"TooShort", make([]byte, 16), "密钥必须是32字节"},
		{"TooLong", make([]byte, 64), "密钥必须是32字节"},
		{"Empty", []byte{}, "密钥必须是32字节"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewAESEncryption(tc.key)
			assert.Error(t, err)
			assert.Nil(t, enc)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestAESEncryption_EncryptDecrypt_Success(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESEncryption(key)
	require.NoError(t, err)

	testCases := []struct {
		name      string
		plaintext []byte
	}{
		{"Small", []byte("hello world")},
		{"Medium", make([]byte, 100)},
		{"Large", make([]byte, 1024)},
		{"BlockSizeMultiple", make([]byte, 16)}, // 正好是块大小
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 加密
			ciphertext, err := enc.Encrypt(ctx, tc.plaintext)
			require.NoError(t, err)
			assert.NotEqual(t, tc.plaintext, ciphertext, "密文应该与明文不同")
			assert.GreaterOrEqual(t, len(ciphertext), 16, "密文应该至少包含16字节IV")

			// 解密
			decrypted, err := enc.Decrypt(ctx, ciphertext)
			require.NoError(t, err)
			assert.Equal(t, tc.plaintext, decrypted, "解密后应该与原始明文相同")
		})
	}
}

func TestAESEncryption_Encrypt_EmptyData(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESEncryption(key)
	require.NoError(t, err)

	ctx := context.Background()
	ciphertext, err := enc.Encrypt(ctx, []byte{})
	require.NoError(t, err)

	// 空数据加密后至少包含 IV（16字节）+ 一个填充块（16字节）
	assert.GreaterOrEqual(t, len(ciphertext), 32)
}

func TestAESEncryption_Decrypt_InvalidCiphertext(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESEncryption(key)
	require.NoError(t, err)

	testCases := []struct {
		name        string
		ciphertext  []byte
		expectedErr string
	}{
		{"TooShort", []byte("short"), "密文长度不足"},
		{"Empty", []byte{}, "密文长度不足"},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := enc.Decrypt(ctx, tc.ciphertext)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestAESEncryption_Decrypt_WrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1 // 不同的密钥

	enc1, _ := NewAESEncryption(key1)
	enc2, _ := NewAESEncryption(key2)

	ctx := context.Background()
	plaintext := []byte("hello world")

	// 用 key1 加密
	ciphertext, err := enc1.Encrypt(ctx, plaintext)
	require.NoError(t, err)

	// 用 key2 解密（应该失败或得到乱码）
	_, err = enc2.Decrypt(ctx, ciphertext)
	// 错误可能是填充错误或其他解密错误
	assert.Error(t, err)
}

// ========================================
// AESGCMEncryption (AES-256-GCM) 测试
// ========================================

func TestNewAESGCMEncryption_ValidKey(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESGCMEncryption(key)
	require.NoError(t, err)
	assert.NotNil(t, enc)
	assert.Equal(t, "AES-256-GCM", enc.Name())
}

func TestNewAESGCMEncryption_InvalidKey(t *testing.T) {
	testCases := []struct {
		name string
		key  []byte
	}{
		{"TooShort", make([]byte, 16)},
		{"TooLong", make([]byte, 64)},
		{"Empty", []byte{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewAESGCMEncryption(tc.key)
			assert.Error(t, err)
			assert.Nil(t, enc)
		})
	}
}

func TestAESGCMEncryption_EncryptDecrypt_Success(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESGCMEncryption(key)
	require.NoError(t, err)

	testCases := []struct {
		name      string
		plaintext []byte
	}{
		{"Small", []byte("hello world")},
		{"Medium", make([]byte, 100)},
		{"Large", make([]byte, 1024)},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 加密
			ciphertext, err := enc.Encrypt(ctx, tc.plaintext)
			require.NoError(t, err)
			assert.NotEqual(t, tc.plaintext, ciphertext, "密文应该与明文不同")
			// GCM 密文包含 nonce (12字节) + tag (16字节) + 密文
			assert.GreaterOrEqual(t, len(ciphertext), 28)

			// 解密
			decrypted, err := enc.Decrypt(ctx, ciphertext)
			require.NoError(t, err)
			assert.Equal(t, tc.plaintext, decrypted, "解密后应该与原始明文相同")
		})
	}
}

// TestAESGCMEncryption_EmptyData 单独测试空数据
func TestAESGCMEncryption_EmptyData(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESGCMEncryption(key)
	require.NoError(t, err)

	ctx := context.Background()
	ciphertext, err := enc.Encrypt(ctx, []byte{})
	require.NoError(t, err)

	// GCM 对空数据的加密结果至少包含 nonce + tag
	assert.GreaterOrEqual(t, len(ciphertext), 28)

	// 解密
	decrypted, err := enc.Decrypt(ctx, ciphertext)
	require.NoError(t, err)
	// GCM 对空数据返回 nil（这是正常的）
	assert.Nil(t, decrypted)
}

func TestAESGCMEncryption_Decrypt_TamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESGCMEncryption(key)
	require.NoError(t, err)

	ctx := context.Background()
	plaintext := []byte("hello world")

	ciphertext, err := enc.Encrypt(ctx, plaintext)
	require.NoError(t, err)

	// 篡改密文
	ciphertext[0] ^= 0xFF

	// 解密应该失败（认证标签验证失败）
	_, err = enc.Decrypt(ctx, ciphertext)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "认证标签验证失败")
}

func TestAESGCMEncryption_Decrypt_TooShort(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewAESGCMEncryption(key)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = enc.Decrypt(ctx, []byte("short"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "密文长度不足")
}

// ========================================
// ChaCha20Poly1305Encryption 测试
// ========================================

func TestNewChaCha20Poly1305Encryption_ValidKey(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewChaCha20Poly1305Encryption(key)
	require.NoError(t, err)
	assert.NotNil(t, enc)
	assert.Equal(t, "ChaCha20-Poly1305", enc.Name())
}

func TestNewChaCha20Poly1305Encryption_InvalidKey(t *testing.T) {
	testCases := []struct {
		name string
		key  []byte
	}{
		{"TooShort", make([]byte, 16)},
		{"TooLong", make([]byte, 64)},
		{"Empty", []byte{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewChaCha20Poly1305Encryption(tc.key)
			assert.Error(t, err)
			assert.Nil(t, enc)
		})
	}
}

func TestChaCha20Poly1305Encryption_EncryptDecrypt_Success(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewChaCha20Poly1305Encryption(key)
	require.NoError(t, err)

	testCases := []struct {
		name      string
		plaintext []byte
	}{
		{"Small", []byte("hello world")},
		{"Medium", make([]byte, 100)},
		{"Large", make([]byte, 1024)},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 加密
			ciphertext, err := enc.Encrypt(ctx, tc.plaintext)
			require.NoError(t, err)
			assert.NotEqual(t, tc.plaintext, ciphertext)

			// 解密
			decrypted, err := enc.Decrypt(ctx, ciphertext)
			require.NoError(t, err)
			assert.Equal(t, tc.plaintext, decrypted)
		})
	}
}

// TestChaCha20Poly1305Encryption_EmptyData 单独测试空数据
func TestChaCha20Poly1305Encryption_EmptyData(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewChaCha20Poly1305Encryption(key)
	require.NoError(t, err)

	ctx := context.Background()
	ciphertext, err := enc.Encrypt(ctx, []byte{})
	require.NoError(t, err)

	// 解密
	decrypted, err := enc.Decrypt(ctx, ciphertext)
	require.NoError(t, err)
	// ChaCha20 对空数据返回 nil（这是正常的）
	assert.Nil(t, decrypted)
}

// ========================================
// 工厂函数 NewEncryption 测试
// ========================================

func TestNewEncryption_AllTypes(t *testing.T) {
	key := make([]byte, 32)

	testCases := []struct {
		name         string
		encType      EncryptionType
		expectedName string
	}{
		{"None", EncryptionTypeNone, "none"},
		{"AES256CBC", EncryptionTypeAES256CBC, "AES-256-CBC"},
		{"AES256GCM", EncryptionTypeAES256GCM, "AES-256-GCM"},
		{"ChaCha20Poly1305", EncryptionTypeChaCha20Poly1305, "ChaCha20-Poly1305"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncryption(tc.encType, key)
			require.NoError(t, err)
			assert.NotNil(t, enc)
			assert.Equal(t, tc.expectedName, enc.Name())
		})
	}
}

func TestNewEncryption_InvalidType(t *testing.T) {
	key := make([]byte, 32)
	enc, err := NewEncryption(EncryptionType(999), key)
	assert.Error(t, err)
	assert.Nil(t, enc)
}

func TestNewEncryption_InvalidKeySize(t *testing.T) {
	invalidKey := make([]byte, 16)

	testCases := []struct {
		name    string
		encType EncryptionType
	}{
		{"AES256CBC", EncryptionTypeAES256CBC},
		{"AES256GCM", EncryptionTypeAES256GCM},
		{"ChaCha20Poly1305", EncryptionTypeChaCha20Poly1305},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncryption(tc.encType, invalidKey)
			assert.Error(t, err)
			assert.Nil(t, enc)
		})
	}
}

// ========================================
// PKCS7 填充/去填充 测试
// ========================================

func TestPKCS7Pad(t *testing.T) {
	testCases := []struct {
		name         string
		data         []byte
		blockSize    int
		expectedSize int
	}{
		// PKCS7 规范：即使数据长度是块大小的倍数，也要添加一个完整的填充块
		{"ExactMultiple", []byte("1234567812345678"), 8, 24},  // 正好是倍数，需要完整填充块
		{"NeedPadding", []byte("1234567"), 8, 8},            // 需要填充1字节
		{"EmptyData", []byte{}, 16, 16},                     // 空数据需要完整填充
		{"LargeData", make([]byte, 15), 16, 16},            // 需要1字节填充
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			padded := pkcs7Pad(tc.data, tc.blockSize)
			assert.Equal(t, tc.expectedSize, len(padded))
			assert.Equal(t, 0, len(padded)%tc.blockSize, "填充后长度应该是块大小的倍数")
		})
	}
}

func TestPKCS7Unpad(t *testing.T) {
	testCases := []struct {
		name         string
		data         []byte
		shouldErr    bool
		expectedLen  int
	}{
		{"ValidPadding", []byte("hello\x03\x03\x03"), false, 5},
		// 完整填充块：16 字节的 0x10 表示整个 16 字节都是填充
		{"FullPadding", []byte("\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10\x10"), false, 0},
		{"EmptyData", []byte{}, true, 0},
		// 填充长度 10 但数据只有 6 字节，这是无效的
		{"InvalidPadSize", []byte("hello\x0a"), true, 0},
		{"ZeroPad", []byte("hello\x00"), true, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			unpadded, err := pkcs7Unpad(tc.data)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedLen, len(unpadded))
			}
		})
	}
}

func TestPKCS7Pad_Unpad_RoundTrip(t *testing.T) {
	testCases := []struct {
		name      string
		data      []byte
		blockSize int
	}{
		{"Small", []byte("hello"), 16},
		{"Medium", make([]byte, 100), 16},
		{"Large", make([]byte, 1024), 16},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			padded := pkcs7Pad(tc.data, tc.blockSize)
			unpadded, err := pkcs7Unpad(padded)
			require.NoError(t, err)
			assert.Equal(t, tc.data, unpadded, "往返后数据应该相同")
		})
	}
}

// ========================================
// 跨算法一致性测试
// ========================================

func TestEncryption_CrossAlgorithmConsistency(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("hello world, this is a test message for encryption algorithms")

	ctx := context.Background()

	// 创建所有加密算法
	encryptions := []struct {
		name string
		enc  Encryption
	}{
		{"NoEncryption", &NoEncryption{}},
		{"AES256CBC", must(NewAESEncryption(key))},
		{"AES256GCM", must(NewAESGCMEncryption(key))},
		{"ChaCha20Poly1305", must(NewChaCha20Poly1305Encryption(key))},
	}

	for _, enc := range encryptions {
		t.Run(enc.name, func(t *testing.T) {
			// 加密
			ciphertext, err := enc.enc.Encrypt(ctx, plaintext)
			require.NoError(t, err, "%s: 加密失败", enc.name)

			// 解密
			decrypted, err := enc.enc.Decrypt(ctx, ciphertext)
			require.NoError(t, err, "%s: 解密失败", enc.name)

			// 验证
			assert.Equal(t, plaintext, decrypted, "%s: 解密后数据不一致", enc.name)
		})
	}
}

// ========================================
// 性能基准测试
// ========================================

func BenchmarkNoEncryption_Encrypt(b *testing.B) {
	enc := &NoEncryption{}
	plaintext := make([]byte, 1024)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(ctx, plaintext)
	}
}

func BenchmarkAESEncryption_Encrypt(b *testing.B) {
	key := make([]byte, 32)
	enc, _ := NewAESEncryption(key)
	plaintext := make([]byte, 1024)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(ctx, plaintext)
	}
}

func BenchmarkAESGCMEncryption_Encrypt(b *testing.B) {
	key := make([]byte, 32)
	enc, _ := NewAESGCMEncryption(key)
	plaintext := make([]byte, 1024)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(ctx, plaintext)
	}
}

func BenchmarkChaCha20Poly1305Encryption_Encrypt(b *testing.B) {
	key := make([]byte, 32)
	enc, _ := NewChaCha20Poly1305Encryption(key)
	plaintext := make([]byte, 1024)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = enc.Encrypt(ctx, plaintext)
	}
}

// must 辅助函数：用于测试中快速创建实例
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
