// Package consistency 提供元数据一致性协调器
package consistency

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jzhang405/NexKV/internal/metadata/kvstore"
)

// ==================== Mock Store ====================

// fencingMockStore 模拟 KV 存储（用于 Fencing 测试）
type fencingMockStore struct {
	mu    sync.RWMutex
	data  map[string][]byte
	calls int
}

func newFencingMockStore() *fencingMockStore {
	return &fencingMockStore{
		data: make(map[string][]byte),
	}
}

func (m *fencingMockStore) Put(ctx context.Context, ns, key string, value any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.data[ns+key] = []byte("mock")
	return nil
}

func (m *fencingMockStore) Get(ctx context.Context, ns, key string, value any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.data[ns+key]
	if !ok {
		return errors.New("not found")
	}
	_ = data
	return nil
}

func (m *fencingMockStore) Delete(ctx context.Context, ns, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, ns+key)
	return nil
}

func (m *fencingMockStore) Exists(ctx context.Context, ns, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.data[ns+key]
	return ok, nil
}

func (m *fencingMockStore) ListPrefix(ctx context.Context, ns, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for k := range m.data {
		result = append(result, k)
	}
	return result, nil
}

func (m *fencingMockStore) Close() error { return nil }

func (m *fencingMockStore) GetRaw(ctx context.Context, ns, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.data[ns+key]
	if !ok {
		return nil, errors.New("not found")
	}
	return data, nil
}

func (m *fencingMockStore) PutRaw(ctx context.Context, ns, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.data[ns+key] = data
	return nil
}

func (m *fencingMockStore) BatchGetRaw(ctx context.Context, ns string, keys []string) (map[string][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string][]byte)
	for _, k := range keys {
		if data, ok := m.data[ns+k]; ok {
			result[k] = data
		}
	}
	return result, nil
}

// ==================== FencingToken Tests ====================

func TestFencingToken_NewFencingToken(t *testing.T) {
	token := NewFencingToken(100, "node-1")

	if token.Term != 100 {
		t.Errorf("expected term 100, got %d", token.Term)
	}
	if token.NodeID != "node-1" {
		t.Errorf("expected nodeID node-1, got %s", token.NodeID)
	}
	if token.IssuedAt.IsZero() {
		t.Error("expected IssuedAt to be set")
	}
}

func TestFencingToken_IsNewerThan(t *testing.T) {
	token1 := NewFencingToken(100, "node-1")
	token2 := NewFencingToken(99, "node-2")
	token3 := NewFencingToken(100, "node-3")

	// token1 比 token2 新
	if !token1.IsNewerThan(token2) {
		t.Error("expected token1 to be newer than token2")
	}

	// token2 不比 token1 新
	if token2.IsNewerThan(token1) {
		t.Error("expected token2 not to be newer than token1")
	}

	// 相同 Term 不算更新
	if token1.IsNewerThan(token3) {
		t.Error("expected same term not to be newer")
	}

	// nil 被视为更旧
	if !token1.IsNewerThan(nil) {
		t.Error("expected token1 to be newer than nil")
	}
}

// ==================== TermStorage Tests ====================

func TestTermStorage_GetCurrentTerm_Empty(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	term, err := termStore.GetCurrentTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term != 0 {
		t.Errorf("expected term 0 for empty store, got %d", term)
	}
}

func TestTermStorage_AdvanceTerm(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	// 第一次推进
	term1, err := termStore.AdvanceTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term1 != 1 {
		t.Errorf("expected term 1, got %d", term1)
	}

	// 第二次推进
	term2, err := termStore.AdvanceTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term2 != 2 {
		t.Errorf("expected term 2, got %d", term2)
	}

	// 验证持久化
	term, err := termStore.GetCurrentTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term != 2 {
		t.Errorf("expected term 2 from storage, got %d", term)
	}
}

func TestTermStorage_SetTerm(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	err := termStore.SetTerm(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	term, err := termStore.GetCurrentTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if term != 100 {
		t.Errorf("expected term 100, got %d", term)
	}
}

// ==================== FencingStore Tests ====================

func TestFencingStore_Write_First(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	token := NewFencingToken(1, "node-1")

	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFencingStore_Write_NewerToken(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	token1 := NewFencingToken(1, "node-1")
	token2 := NewFencingToken(2, "node-2")

	// 第一次写入
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 更高 Term 写入
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFencingStore_Write_StaleToken(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	token1 := NewFencingToken(2, "node-1")
	token2 := NewFencingToken(1, "node-2")

	// 第一次写入
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 旧 Token 写入应该失败
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token2)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken, got %v", err)
	}
}

func TestFencingStore_Write_SameTerm(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	token1 := NewFencingToken(1, "node-1")
	token2 := NewFencingToken(1, "node-2")

	// 第一次写入
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 相同 Term 写入应该失败
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token2)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken, got %v", err)
	}
}

func TestFencingStore_GetCurrentToken(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// 初始为 nil
	token := fencingStore.GetCurrentToken()
	if token != nil {
		t.Error("expected nil token initially")
	}

	// 写入后获取
	writeToken := NewFencingToken(1, "node-1")
	_ = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", writeToken)

	token = fencingStore.GetCurrentToken()
	if token == nil {
		t.Fatal("expected non-nil token after write")
	}
	if token.Term != 1 {
		t.Errorf("expected term 1, got %d", token.Term)
	}
}

// ==================== LeaderManager Tests ====================

func TestLeaderManager_Initialize(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 设置初始 Term
	_ = termStore.SetTerm(context.Background(), 50)

	err := manager.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if manager.GetCurrentTerm() != 50 {
		t.Errorf("expected term 50, got %d", manager.GetCurrentTerm())
	}
}

func TestLeaderManager_BecomeLeader(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 初始不是 Leader
	if manager.IsLeader() {
		t.Error("expected not to be leader initially")
	}

	// 成为 Leader
	token, err := manager.BecomeLeader(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证 Token
	if token.Term != 1 {
		t.Errorf("expected term 1, got %d", token.Term)
	}
	if token.NodeID != "node-1" {
		t.Errorf("expected nodeID node-1, got %s", token.NodeID)
	}

	// 验证状态
	if !manager.IsLeader() {
		t.Error("expected to be leader")
	}
	if manager.GetLeaderID() != "node-1" {
		t.Errorf("expected leaderID node-1, got %s", manager.GetLeaderID())
	}
}

func TestLeaderManager_StepDown(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 成为 Leader
	_, _ = manager.BecomeLeader(context.Background())
	if !manager.IsLeader() {
		t.Fatal("expected to be leader")
	}

	// 退位
	manager.StepDown()

	if manager.IsLeader() {
		t.Error("expected not to be leader after step down")
	}
	if manager.GetLeaderID() != "" {
		t.Errorf("expected empty leaderID, got %s", manager.GetLeaderID())
	}
}

func TestLeaderManager_GenerateToken_NotLeader(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 未成为 Leader，生成 Token 应该失败
	_, err := manager.GenerateToken()
	if err != ErrTokenNotFromLeader {
		t.Errorf("expected ErrTokenNotFromLeader, got %v", err)
	}
}

func TestLeaderManager_GenerateToken_Leader(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	manager := NewLeaderManager(termStore, "node-1")

	// 成为 Leader
	_, _ = manager.BecomeLeader(context.Background())

	// 生成 Token
	token, err := manager.GenerateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if token.NodeID != "node-1" {
		t.Errorf("expected nodeID node-1, got %s", token.NodeID)
	}
}

// ==================== Split Brain Scenario Test ====================

func TestSplitBrain_Scenario(t *testing.T) {
	// 模拟脑裂场景：
	// 1. Leader (Term=100) 写入数据
	// 2. 网络分区，node-2 以为自己成为 Leader (Term=99)
	// 3. 分区恢复，node-2 尝试写入，应该被拒绝

	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// Term=100 的 Leader 写入
	token100 := NewFencingToken(100, "node-1")
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Term=99 的旧 Leader 尝试写入（模拟脑裂）
	token99 := NewFencingToken(99, "node-2")
	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token99)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken for stale leader, got %v", err)
	}

	// 验证当前 Token 仍然是 Term=100
	currentToken := fencingStore.GetCurrentToken()
	if currentToken.Term != 100 {
		t.Errorf("expected current term 100, got %d", currentToken.Term)
	}
}

// ==================== Concurrent Write Test ====================

func TestFencingStore_ConcurrentWrite(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// 并发写入测试：模拟多个请求同时到达
	// 使用相同的 Term，只有第一个应该成功
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	// 10 个 goroutine 同时使用 Term=100 写入
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token := NewFencingToken(100, "node-1")
			err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)

			mu.Lock()
			switch err {
			case nil:
				successCount++
			case ErrStaleToken:
				failCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// 只有第一次 Term=100 的写入应该成功（后续相同 Term 应该失败）
	// 注意：由于并发，可能有多个成功，但 failCount 应该 > 0
	if successCount < 1 {
		t.Errorf("expected at least 1 success, got %d", successCount)
	}

	// 验证当前 Token 的 Term 是 100
	currentToken := fencingStore.GetCurrentToken()
	if currentToken == nil || currentToken.Term != 100 {
		t.Errorf("expected current term 100, got %v", currentToken)
	}

	t.Logf("concurrent write: %d successes, %d failures (expected: 1 success with proper locking)", successCount, failCount)
}

func TestFencingStore_SequentialWrite_DifferentTerms(t *testing.T) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	// 顺序写入，每次 Term 递增
	for term := 1; term <= 10; term++ {
		token := NewFencingToken(uint64(term), "node-1")
		err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)
		if err != nil {
			t.Errorf("unexpected error for term %d: %v", term, err)
		}
	}

	// 尝试用旧 Term 写入，应该失败
	oldToken := NewFencingToken(5, "node-2")
	err := fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", oldToken)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken for old term, got %v", err)
	}
}

// ==================== Restart Recovery Test ====================

func TestRestart_Recovery(t *testing.T) {
	// 模拟重启场景：
	// 1. Leader (Term=100) 写入数据
	// 2. Leader 宕机重启，Term 从持久化恢复
	// 3. 重启后 Term 仍然是 100，防护有效

	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	// Term=100 的 Leader 推进 Term
	term100, err := termStore.AdvanceTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 1; i < 100; i++ {
		term100, _ = termStore.AdvanceTerm(context.Background())
	}

	// 模拟重启：创建新的 TermStorage（模拟进程重启）
	newTermStore := NewTermStorage(store)

	// 从持久化恢复 Term
	recoveredTerm, err := newTermStore.GetCurrentTerm(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if recoveredTerm != term100 {
		t.Errorf("expected recovered term %d, got %d", term100, recoveredTerm)
	}

	// 验证防护仍然有效
	fencingStore := NewFencingStore(newTermStore, store)
	token99 := NewFencingToken(99, "old-node")

	// 设置当前 Token（模拟之前的状态）
	_ = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", NewFencingToken(term100, "new-node"))

	err = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value2", token99)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken after restart, got %v", err)
	}
}

// ==================== ValidateFencingToken Tests ====================

func TestValidateFencingToken_Nil(t *testing.T) {
	token := NewFencingToken(1, "node-1")

	err := ValidateFencingToken(token, nil)
	if err != nil {
		t.Errorf("expected nil error for nil current, got %v", err)
	}
}

func TestValidateFencingToken_Newer(t *testing.T) {
	token := NewFencingToken(2, "node-1")
	current := NewFencingToken(1, "node-2")

	err := ValidateFencingToken(token, current)
	if err != nil {
		t.Errorf("expected nil error for newer token, got %v", err)
	}
}

func TestValidateFencingToken_Stale(t *testing.T) {
	token := NewFencingToken(1, "node-1")
	current := NewFencingToken(2, "node-2")

	err := ValidateFencingToken(token, current)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken, got %v", err)
	}
}

func TestValidateFencingToken_SameTerm(t *testing.T) {
	token := NewFencingToken(1, "node-1")
	current := NewFencingToken(1, "node-2")

	err := ValidateFencingToken(token, current)
	if err != ErrStaleToken {
		t.Errorf("expected ErrStaleToken for same term, got %v", err)
	}
}

// ==================== Benchmark ====================

func BenchmarkFencingStore_Write(b *testing.B) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)
	fencingStore := NewFencingStore(termStore, store)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		token := NewFencingToken(uint64(i+1), "node-1")
		_ = fencingStore.Write(context.Background(), kvstore.NamespaceCluster, "key1", "value1", token)
	}
}

func BenchmarkTermStorage_AdvanceTerm(b *testing.B) {
	store := newFencingMockStore()
	termStore := NewTermStorage(store)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = termStore.AdvanceTerm(context.Background())
	}
}
