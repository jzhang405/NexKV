// Copyright 2026 NexKV Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

package btree

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// DataMigrator 从旧的 Node-based 格式迁移到新的 Page-based 格式
// Phase 1 Week 11-12: 数据迁移实现
type DataMigrator struct {
	// 旧数据库路径
	oldDBPath string

	// 新的 ChunkManager
	newMgr *ChunkManager

	// GC 管理器
	gc *BTreeGC

	// CCOW 管理器
	ccow *CCOWManager

	// 迁移状态
	status     MigrationStatus
	statusFile string

	// 统计信息
	stats MigrationStats
}

// MigrationStatus 迁移状态
type MigrationStatus struct {
	Phase       string    `json:"phase"`        // 迁移阶段
	StartedAt   time.Time `json:"started_at"`   // 开始时间
	UpdatedAt   time.Time `json:"updated_at"`   // 更新时间
	TotalNodes  int64     `json:"total_nodes"`  // 总节点数
	MigratedNodes int64   `json:"migrated_nodes"` // 已迁移节点数
	FailedNodes int64    `json:"failed_nodes"` // 失败节点数
	Completed   bool      `json:"completed"`    // 是否完成
	Error       string    `json:"error,omitempty"` // 错误信息
}

// MigrationStats 迁移统计
type MigrationStats struct {
	TotalNodes      int64         // 总节点数
	MigratedNodes   int64         // 已迁移节点数
	FailedNodes     int64         // 失败节点数
	SkippedNodes    int64         // 跳过节点数
	BytesMigrated   int64         // 已迁移字节数
	Duration        time.Duration  // 迁移耗时
	NodesPerSecond  float64       // 每秒迁移节点数
}

// MigrationPhase 迁移阶段
const (
	PhaseInit       = "init"
	PhaseScanning   = "scanning"
	PhaseMigrating  = "migrating"
	PhaseVerifying  = "verifying"
	PhaseCompleted  = "completed"
	PhaseRollback   = "rollback"
	PhaseFailed     = "failed"
)

// NewDataMigrator 创建新的数据迁移器
func NewDataMigrator(oldDBPath string, newMgr *ChunkManager, gc *BTreeGC, ccow *CCOWManager) *DataMigrator {
	return &DataMigrator{
		oldDBPath:  oldDBPath,
		newMgr:     newMgr,
		gc:         gc,
		ccow:       ccow,
		statusFile: filepath.Join(oldDBPath, ".migration_status.json"),
		status: MigrationStatus{
			Phase:     PhaseInit,
			StartedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

// Migrate 执行数据迁移
// progressCb: 进度回调，参数为 (已迁移节点数, 总节点数)
func (m *DataMigrator) Migrate(ctx context.Context, progressCb func(int, int)) error {
	m.status.Phase = PhaseScanning
	m.status.UpdatedAt = time.Now()
	m.saveStatus()

	// 1. 扫描旧数据库，获取所有节点
	nodes, err := m.scanOldDatabase()
	if err != nil {
		return fmt.Errorf("scan old database failed: %w", err)
	}

	m.status.TotalNodes = int64(len(nodes))
	m.status.Phase = PhaseMigrating
	m.status.UpdatedAt = time.Now()
	m.saveStatus()

	// 2. 逐个迁移节点
	for i, node := range nodes {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := m.migrateNode(node); err != nil {
			m.status.FailedNodes++
			m.stats.FailedNodes++
			m.status.Error = err.Error()
			m.saveStatus()

			// 记录错误但继续迁移
			continue
		}

		m.status.MigratedNodes++
		m.stats.MigratedNodes++

		// 进度回调
		if progressCb != nil {
			progressCb(i+1, len(nodes))
		}

		// 每 100 个节点保存一次状态
		if (i+1)%100 == 0 {
			m.status.UpdatedAt = time.Now()
			m.saveStatus()
		}
	}

	// 3. 验证迁移结果
	m.status.Phase = PhaseVerifying
	m.status.UpdatedAt = time.Now()
	m.saveStatus()

	if err := m.Verify(ctx); err != nil {
		m.status.Phase = PhaseFailed
		m.status.Error = err.Error()
		m.saveStatus()
		return fmt.Errorf("verification failed: %w", err)
	}

	// 4. 标记完成
	m.status.Phase = PhaseCompleted
	m.status.Completed = true
	m.status.UpdatedAt = time.Now()
	m.stats.Duration = time.Since(m.status.StartedAt)
	if m.stats.Duration.Seconds() > 0 {
		m.stats.NodesPerSecond = float64(m.stats.MigratedNodes) / m.stats.Duration.Seconds()
	}
	m.saveStatus()

	return nil
}

// scanOldDatabase 扫描旧数据库，获取所有节点
func (m *DataMigrator) scanOldDatabase() ([]*Node, error) {
	nodes := make([]*Node, 0) // 初始化为空列表而不是 nil

	// 遍历旧数据库目录，查找所有数据文件
	err := filepath.Walk(m.oldDBPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录和隐藏文件
		if info.IsDir() || filepath.Base(path)[0] == '.' {
			return nil
		}

		// 查找数据文件（.dat 或 .idx）
		if filepath.Ext(path) == ".dat" || filepath.Ext(path) == ".idx" {
			fileNodes, err := m.readNodesFromFile(path)
			if err != nil {
				// 记录错误但继续扫描其他文件
				m.stats.FailedNodes++
				return nil
			}
			nodes = append(nodes, fileNodes...)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	m.stats.TotalNodes = int64(len(nodes))
	return nodes, nil
}

// readNodesFromFile 从文件中读取节点
func (m *DataMigrator) readNodesFromFile(filePath string) ([]*Node, error) {
	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		// 返回空列表而不是错误
		return []*Node{}, nil
	}

	var nodes []*Node
	offset := 0

	// 逐个读取节点
	for offset < len(data) {
		// 读取节点大小（4 bytes）
		if offset+4 > len(data) {
			break
		}
		nodeSize := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4

		// 跳过无效的节点大小
		if nodeSize == 0 || nodeSize > 1024*1024 { // 最大 1MB
			break
		}

		// 读取节点数据
		if offset+int(nodeSize) > len(data) {
			break
		}
		nodeData := data[offset : offset+int(nodeSize)]
		offset += int(nodeSize)

		// 反序列化节点
		node, err := m.deserializeNode(nodeData)
		if err != nil {
			// 记录错误但继续读取
			m.stats.FailedNodes++
			continue
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// deserializeNode 反序列化节点（旧格式）
func (m *DataMigrator) deserializeNode(data []byte) (*Node, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("invalid node data: too short")
	}

	// 读取 PageID (8 bytes)
	pageID := model.PageID(binary.BigEndian.Uint64(data[0:8]))

	// 读取 IsLeaf (1 byte)
	isLeaf := data[8] != 0

	// 读取 Keys 数量 (4 bytes)
	numKeys := int(binary.BigEndian.Uint32(data[9:13]))

	offset := 13

	node := &Node{
		PageID: pageID,
		IsLeaf: isLeaf,
		Keys:   make([][]byte, 0, numKeys),
	}

	if isLeaf {
		// Leaf node: 读取 Values
		node.Values = make([][]byte, 0, numKeys)

		for i := 0; i < numKeys; i++ {
			// 读取 Key 长度
			if offset+2 > len(data) {
				return nil, fmt.Errorf("invalid key length")
			}
			keyLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2

			// 读取 Key
			if offset+keyLen > len(data) {
				return nil, fmt.Errorf("invalid key data")
			}
			key := make([]byte, keyLen)
			copy(key, data[offset:offset+keyLen])
			offset += keyLen

			// 读取 Value 长度
			if offset+2 > len(data) {
				return nil, fmt.Errorf("invalid value length")
			}
			valueLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2

			// 读取 Value
			if offset+valueLen > len(data) {
				return nil, fmt.Errorf("invalid value data")
			}
			value := make([]byte, valueLen)
			copy(value, data[offset:offset+valueLen])
			offset += valueLen

			node.Keys = append(node.Keys, key)
			node.Values = append(node.Values, value)
		}
	} else {
		// Internal node: 读取 ChildIDs
		numChildren := numKeys + 1
		node.ChildIDs = make([]model.PageID, numChildren)

		// 先读取所有 Keys
		for i := 0; i < numKeys; i++ {
			// 读取 Key 长度
			if offset+2 > len(data) {
				return nil, fmt.Errorf("invalid key length")
			}
			keyLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			offset += 2

			// 读取 Key
			if offset+keyLen > len(data) {
				return nil, fmt.Errorf("invalid key data")
			}
			key := make([]byte, keyLen)
			copy(key, data[offset:offset+keyLen])
			offset += keyLen

			node.Keys = append(node.Keys, key)
		}

		// 读取 ChildIDs
		for i := 0; i < numChildren; i++ {
			if offset+8 > len(data) {
				return nil, fmt.Errorf("invalid child id")
			}
			childID := model.PageID(binary.BigEndian.Uint64(data[offset : offset+8]))
			offset += 8
			node.ChildIDs[i] = childID
		}

		// 初始化 Children（稍后填充）
		node.Children = make([]*Node, numChildren)
	}

	return node, nil
}

// migrateNode 迁移单个节点
func (m *DataMigrator) migrateNode(oldNode *Node) error {
	if oldNode.IsLeaf {
		return m.migrateLeafNode(oldNode)
	}
	return m.migrateInternalNode(oldNode)
}

// migrateLeafNode 迁移叶子节点
func (m *DataMigrator) migrateLeafNode(oldNode *Node) error {
	// 1. 创建新的 LeafPage
	leafPage := NewLeafPage(oldNode.PageID)

	// 2. 复制键值对
	for i := range oldNode.Keys {
		if _, err := leafPage.Insert(oldNode.Keys[i], oldNode.Values[i]); err != nil {
			return fmt.Errorf("insert into leaf page failed: %w", err)
		}
	}

	// 3. 序列化页面
	data, err := leafPage.Serialize()
	if err != nil {
		return fmt.Errorf("serialize leaf page failed: %w", err)
	}

	// 4. 填充到页面大小（4096 bytes）
	pageSize := 4096
	if len(data) < pageSize {
		padded := make([]byte, pageSize)
		copy(padded, data)
		data = padded
	}

	// 5. 分配位置并写入 ChunkManager
	pos, err := m.newMgr.AllocatePage(int(model.LeafPage))
	if err != nil {
		return fmt.Errorf("allocate page failed: %w", err)
	}

	if err := m.newMgr.WritePage(pos, data); err != nil {
		return fmt.Errorf("write leaf page failed: %w", err)
	}

	// 6. 更新统计
	atomic.AddInt64(&m.stats.BytesMigrated, int64(len(data)))

	return nil
}

// migrateInternalNode 迁移内部节点
func (m *DataMigrator) migrateInternalNode(oldNode *Node) error {
	// 1. 创建新的 InternalPage
	internalPage := NewInternalPage(oldNode.PageID)

	// 2. 复制键（直接操作 keys 数组）
	for i := range oldNode.Keys {
		key := make([]byte, len(oldNode.Keys[i]))
		copy(key, oldNode.Keys[i])
		internalPage.keys = append(internalPage.keys, key)
	}

	// 3. 先扩展 children 列表到正确大小
	numChildren := len(oldNode.ChildIDs)
	for len(internalPage.children) < numChildren {
		internalPage.children = append(internalPage.children, nil)
	}

	// 4. 创建子节点引用（PageRef）
	for i := 0; i < numChildren; i++ {
		childRef := NewPageRef()
		// 子节点将在后续迁移中填充
		internalPage.children[i] = childRef
	}

	// 5. 序列化页面
	data, err := internalPage.Serialize()
	if err != nil {
		return fmt.Errorf("serialize internal page failed: %w", err)
	}

	// 6. 填充到页面大小（4096 bytes）
	pageSize := 4096
	if len(data) < pageSize {
		padded := make([]byte, pageSize)
		copy(padded, data)
		data = padded
	}

	// 7. 分配位置并写入 ChunkManager
	pos, err := m.newMgr.AllocatePage(int(model.InternalPage))
	if err != nil {
		return fmt.Errorf("allocate page failed: %w", err)
	}

	if err := m.newMgr.WritePage(pos, data); err != nil {
		return fmt.Errorf("write internal page failed: %w", err)
	}

	// 8. 更新统计
	atomic.AddInt64(&m.stats.BytesMigrated, int64(len(data)))

	return nil
}

// Verify 验证迁移完整性
func (m *DataMigrator) Verify(ctx context.Context) error {
	// TODO: 实现验证逻辑
	// 1. 读取新格式中的所有页面
	// 2. 对比旧格式和新格式的键值对数量
	// 3. 随机抽样验证键值对内容
	return nil
}

// Rollback 回滚迁移
func (m *DataMigrator) Rollback(ctx context.Context) error {
	m.status.Phase = PhaseRollback
	m.status.UpdatedAt = time.Now()
	m.saveStatus()

	// 1. 删除新创建的 Chunk 文件
	// 2. 恢复旧数据库文件
	// 3. 删除迁移状态文件

	if err := os.Remove(m.statusFile); err != nil && !os.IsNotExist(err) {
		return err
	}

	m.status.Phase = PhaseInit
	m.status.Completed = false
	m.saveStatus()

	return nil
}

// GetStats 获取迁移统计信息
func (m *DataMigrator) GetStats() MigrationStats {
	return m.stats
}

// GetStatus 获取迁移状态
func (m *DataMigrator) GetStatus() MigrationStatus {
	return m.status
}

// saveStatus 保存迁移状态到文件
func (m *DataMigrator) saveStatus() error {
	// TODO: 实现状态持久化（JSON 格式）
	// 使用 encoding/json 序列化 status 并写入文件
	return nil
}

// loadStatus 从文件加载迁移状态
func (m *DataMigrator) loadStatus() error {
	// TODO: 实现状态加载
	// 读取 JSON 文件并反序列化到 status
	return nil
}

// Resume 恢复未完成的迁移
func (m *DataMigrator) Resume(ctx context.Context, progressCb func(int, int)) error {
	if err := m.loadStatus(); err != nil {
		return fmt.Errorf("load status failed: %w", err)
	}

	if m.status.Completed {
		return fmt.Errorf("migration already completed")
	}

	// 从断点继续迁移
	return m.Migrate(ctx, progressCb)
}
