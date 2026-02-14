// Package consistency 提供元数据一致性协调器
//
// Fencing Token 机制：
//   - 防止脑裂（Split-Brain）导致的数据损坏
//   - 通过单调递增的 Term 实现写入防护
//   - Term 持久化保证重启后防护有效
package consistency

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== Errors ====================

var (
	// ErrStaleToken Token 过期（Term 小于当前值）
	ErrStaleToken = errors.New("fencing token is stale")
	// ErrTokenNotFromLeader Token 不是来自当前 Leader
	ErrTokenNotFromLeader = errors.New("token is not from current leader")
	// ErrTermPersistenceFailed Term 持久化失败
	ErrTermPersistenceFailed = errors.New("failed to persist term")
)

// ==================== FencingToken ====================

// FencingToken 防脑裂令牌
//
// 核心原理：
//  1. 每次 Leader 选举产生新 Leader 时，Term 必须递增
//  2. 存储层拒绝 Token.Term <= current.Term 的写入
//  3. Term 持久化，节点重启后不会丢失
//
// 使用场景：
//   - 脑裂恢复：旧 Leader 的写入被拒绝
//   - Leader 切换：新 Leader 使用更高的 Term
//   - 节点重启：从持久化恢复 Term，防护有效
type FencingToken struct {
	// Term 任期号（全局单调递增）
	// 每次 Leader 选举 +1
	Term uint64

	// NodeID 签发节点 ID
	NodeID string

	// IssuedAt 签发时间
	IssuedAt time.Time
}

// NewFencingToken 创建新的 Fencing Token
func NewFencingToken(term uint64, nodeID string) *FencingToken {
	return &FencingToken{
		Term:     term,
		NodeID:   nodeID,
		IssuedAt: time.Now(),
	}
}

// IsNewerThan 检查当前 Token 是否比另一个 Token 更新
func (t *FencingToken) IsNewerThan(other *FencingToken) bool {
	if other == nil {
		return true
	}
	return t.Term > other.Term
}

// ==================== TermStorage ====================

// TermStorage Term 持久化存储
//
// 职责：
//   - 持久化当前 Term 到 NamespaceCluster
//   - 节点重启后恢复 Term
//   - 原子递增 Term（新 Leader 上任时）
//
// 存储键：meta:cluster:current_term
type TermStorage struct {
	mu sync.RWMutex
	kv kvstore.Store

	// cachedTerm 内存缓存的 Term（减少磁盘读取）
	cachedTerm uint64
	dirty      bool
}

// termKey Term 存储键
const termKey = "current_term"

// NewTermStorage 创建 Term 存储实例
func NewTermStorage(kv kvstore.Store) *TermStorage {
	return &TermStorage{
		kv: kv,
	}
}

// GetCurrentTerm 获取当前 Term
//
// 优先从内存缓存读取，缓存未命中则从持久化存储读取
func (t *TermStorage) GetCurrentTerm(ctx context.Context) (uint64, error) {
	t.mu.RLock()
	if t.cachedTerm > 0 && !t.dirty {
		term := t.cachedTerm
		t.mu.RUnlock()
		return term, nil
	}
	t.mu.RUnlock()

	// 从持久化存储读取
	data, err := t.kv.GetRaw(ctx, kvstore.NamespaceCluster, termKey)
	if err != nil {
		// 首次访问，返回 0
		return 0, nil
	}

	if len(data) < 8 {
		return 0, nil
	}

	term := binary.BigEndian.Uint64(data)

	// 更新缓存
	t.mu.Lock()
	t.cachedTerm = term
	t.dirty = false
	t.mu.Unlock()

	return term, nil
}

// AdvanceTerm 推进 Term（新 Leader 上任时调用）
//
// 原子操作：
//  1. 读取当前 Term
//  2. Term + 1
//  3. 持久化新 Term
//
// 返回新的 Term 值
func (t *TermStorage) AdvanceTerm(ctx context.Context) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 获取当前 Term
	currentTerm := t.cachedTerm
	if currentTerm == 0 || t.dirty {
		data, err := t.kv.GetRaw(ctx, kvstore.NamespaceCluster, termKey)
		if err == nil && len(data) >= 8 {
			currentTerm = binary.BigEndian.Uint64(data)
		}
	}

	// 递增 Term
	newTerm := currentTerm + 1

	// 持久化
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, newTerm)

	if err := t.kv.PutRaw(ctx, kvstore.NamespaceCluster, termKey, data); err != nil {
		return 0, ErrTermPersistenceFailed
	}

	// 更新缓存
	t.cachedTerm = newTerm
	t.dirty = false

	return newTerm, nil
}

// SetTerm 设置 Term（从持久化恢复或外部同步）
func (t *TermStorage) SetTerm(ctx context.Context, term uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, term)

	if err := t.kv.PutRaw(ctx, kvstore.NamespaceCluster, termKey, data); err != nil {
		return ErrTermPersistenceFailed
	}

	t.cachedTerm = term
	t.dirty = false

	return nil
}

// ==================== FencingStore ====================

// FencingStore 带防护的存储
//
// 职责：
//   - 验证写入请求的 Fencing Token
//   - 拒绝 Term <= current.Term 的写入
//   - 更新当前 Token（写入成功后）
type FencingStore struct {
	mu        sync.RWMutex
	current   *FencingToken
	termStore *TermStorage
	storage   kvstore.Store
}

// NewFencingStore 创建带防护的存储实例
func NewFencingStore(termStore *TermStorage, storage kvstore.Store) *FencingStore {
	return &FencingStore{
		termStore: termStore,
		storage:   storage,
	}
}

// Write 带防护的写入
//
// 流程：
//  1. 检查 Token 是否有效（Term > current.Term）
//  2. 拒绝旧 Token
//  3. 更新当前 Token
//  4. 执行写入
func (s *FencingStore) Write(ctx context.Context, ns, key string, value any, token *FencingToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查 Token 是否有效
	if s.current != nil && token.Term <= s.current.Term {
		return ErrStaleToken
	}

	// 更新当前 Token
	s.current = token

	// 执行写入
	return s.storage.Put(ctx, ns, key, value)
}

// WriteRaw 带防护的原始字节写入
func (s *FencingStore) WriteRaw(ctx context.Context, ns, key string, data []byte, token *FencingToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查 Token 是否有效
	if s.current != nil && token.Term <= s.current.Term {
		return ErrStaleToken
	}

	// 更新当前 Token
	s.current = token

	// 执行写入
	return s.storage.PutRaw(ctx, ns, key, data)
}

// GetCurrentToken 获取当前 Token（用于调试/监控）
func (s *FencingStore) GetCurrentToken() *FencingToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// ==================== LeaderManager ====================

// LeaderManager Leader 管理器
//
// 职责：
//   - 管理 Leader 状态和 Term
//   - 处理 Leader 选举和切换
//   - 生成有效的 Fencing Token
type LeaderManager struct {
	mu sync.RWMutex

	// 当前 Leader 信息
	leaderID    string
	isLeader    bool
	currentTerm uint64

	// 依赖
	termStore *TermStorage
	nodeID    string
}

// NewLeaderManager 创建 Leader 管理器
func NewLeaderManager(termStore *TermStorage, nodeID string) *LeaderManager {
	return &LeaderManager{
		termStore: termStore,
		nodeID:    nodeID,
	}
}

// Initialize 初始化（启动时调用，从持久化恢复 Term）
func (m *LeaderManager) Initialize(ctx context.Context) error {
	term, err := m.termStore.GetCurrentTerm(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.currentTerm = term
	m.mu.Unlock()

	return nil
}

// BecomeLeader 成为 Leader（选举成功后调用）
//
// 流程：
//  1. 推进 Term
//  2. 更新 Leader 状态
//  3. 返回新的 Fencing Token
func (m *LeaderManager) BecomeLeader(ctx context.Context) (*FencingToken, error) {
	// 推进 Term
	newTerm, err := m.termStore.AdvanceTerm(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.leaderID = m.nodeID
	m.isLeader = true
	m.currentTerm = newTerm

	return NewFencingToken(newTerm, m.nodeID), nil
}

// StepDown 退位（不再是 Leader）
func (m *LeaderManager) StepDown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.isLeader = false
	m.leaderID = ""
}

// IsLeader 检查当前节点是否是 Leader
func (m *LeaderManager) IsLeader() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isLeader
}

// GetCurrentTerm 获取当前 Term
func (m *LeaderManager) GetCurrentTerm() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentTerm
}

// GetLeaderID 获取当前 Leader ID
func (m *LeaderManager) GetLeaderID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leaderID
}

// GenerateToken 生成 Fencing Token（仅 Leader 可调用）
func (m *LeaderManager) GenerateToken() (*FencingToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.isLeader {
		return nil, ErrTokenNotFromLeader
	}

	return NewFencingToken(m.currentTerm, m.nodeID), nil
}

// ==================== Helper Functions ====================

// ValidateFencingToken 验证 Fencing Token 是否有效
//
// 用于存储层验证写入请求
func ValidateFencingToken(token, current *FencingToken) error {
	if current == nil {
		return nil // 首次写入，任何 Token 都有效
	}

	if token.Term <= current.Term {
		return ErrStaleToken
	}

	return nil
}
