package btree

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CCOWManager Copy-on-Write 管理器
type CCOWManager struct {
	gc *BTreeGC

	// 快照管理
	snapshots  map[uint64]*BTreeSnapshot
	snapshotID atomic.Uint64
	snapshotMu sync.RWMutex

	// 脏页跟踪
	dirtyPages   map[*PageInfo]struct{}
	dirtyPagesMu sync.RWMutex
}

// BTreeSnapshot BTree 快照
type BTreeSnapshot struct {
	ID        uint64
	RootRef   *RootPageRef
	Version   uint64
	CreatedAt int64
}

// NewCCOWManager 创建新的 CCOW 管理器
func NewCCOWManager(gc *BTreeGC) *CCOWManager {
	return &CCOWManager{
		gc:         gc,
		snapshots:  make(map[uint64]*BTreeSnapshot),
		dirtyPages: make(map[*PageInfo]struct{}),
	}
}

// TakeSnapshot 创建快照
func (ccow *CCOWManager) TakeSnapshot(rootRef *RootPageRef) (*BTreeSnapshot, error) {
	ccow.snapshotMu.Lock()
	defer ccow.snapshotMu.Unlock()

	// 获取当前根节点的 PageInfo
	rootInfo := rootRef.GetPageInfo()
	if rootInfo == nil {
		return nil, fmt.Errorf("root page info is nil")
	}

	// 生成新的快照 ID
	snapshotID := ccow.snapshotID.Add(1)

	// 创建快照
	snapshot := &BTreeSnapshot{
		ID:        snapshotID,
		RootRef:   rootRef,
		Version:   rootInfo.GetPageVersion(),
		CreatedAt: time.Now().UnixNano(),
	}

	// 保存快照
	ccow.snapshots[snapshotID] = snapshot

	return snapshot, nil
}

// GetSnapshot 获取快照
func (ccow *CCOWManager) GetSnapshot(snapshotID uint64) (*BTreeSnapshot, bool) {
	ccow.snapshotMu.RLock()
	defer ccow.snapshotMu.RUnlock()

	snapshot, exists := ccow.snapshots[snapshotID]
	return snapshot, exists
}

// ReleaseSnapshot 释放快照
func (ccow *CCOWManager) ReleaseSnapshot(snapshotID uint64) {
	ccow.snapshotMu.Lock()
	defer ccow.snapshotMu.Unlock()

	delete(ccow.snapshots, snapshotID)
}

// MarkDirty 标记页面为脏页
func (ccow *CCOWManager) MarkDirty(pageInfo *PageInfo) {
	ccow.dirtyPagesMu.Lock()
	defer ccow.dirtyPagesMu.Unlock()

	if !pageInfo.IsDirty() {
		pageInfo.MarkDirty()
		ccow.dirtyPages[pageInfo] = struct{}{}
	}
}

// ClearDirty 清除脏页标记
func (ccow *CCOWManager) ClearDirty(pageInfo *PageInfo) {
	ccow.dirtyPagesMu.Lock()
	defer ccow.dirtyPagesMu.Unlock()

	pageInfo.ClearDirty()
	delete(ccow.dirtyPages, pageInfo)
}

// GetDirtyPages 获取所有脏页
func (ccow *CCOWManager) GetDirtyPages() []*PageInfo {
	ccow.dirtyPagesMu.RLock()
	defer ccow.dirtyPagesMu.RUnlock()

	dirtyPages := make([]*PageInfo, 0, len(ccow.dirtyPages))
	for pageInfo := range ccow.dirtyPages {
		dirtyPages = append(dirtyPages, pageInfo)
	}

	return dirtyPages
}

// CopyPathBottomUp 自底向上复制路径
func (ccow *CCOWManager) CopyPathBottomUp(
	ctx context.Context,
	rootRef *RootPageRef,
	path []*PageInfo,
	modifyFunc func(*PageInfo) error,
) (*PageInfo, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	// 从叶子节点开始，向上复制
	for i := len(path) - 1; i >= 0; i-- {
		pageInfo := path[i]

		// 克隆页面
		clonedInfo := ccow.clonePageInfo(pageInfo)

		// 应用修改
		if err := modifyFunc(clonedInfo); err != nil {
			return nil, fmt.Errorf("modify failed: %w", err)
		}

		// 标记为脏页
		ccow.MarkDirty(clonedInfo)

		// 如果不是根节点，更新父节点的引用
		if i > 0 {
			parentInfo := path[i-1]
			if err := ccow.updateChildRef(parentInfo, pageInfo, clonedInfo); err != nil {
				return nil, fmt.Errorf("update child ref failed: %w", err)
			}
		} else {
			// 根节点，使用 CAS 更新 RootPageRef
			if rootRef != nil {
				oldRootInfo := rootRef.pInfo.Load()
				if !rootRef.ReplacePage(oldRootInfo, clonedInfo) {
					return nil, fmt.Errorf("CAS update root failed: concurrent modification detected")
				}
			}
			// 如果 rootRef 为 nil，跳过 CAS 更新（测试场景）
		}
	}

	return path[0], nil
}

// clonePageInfo 克隆 PageInfo
func (ccow *CCOWManager) clonePageInfo(info *PageInfo) *PageInfo {
	// 创建新的 PageInfo（深拷贝 Page 数据）
	newInfo := &PageInfo{
		page:        info.GetPage(), // 暂时共享 Page，后续实现深拷贝
		pageLock:    NewPageLock(),  // 创建新锁
		metaVersion: info.metaVersion,
		pageSize:    info.pageSize,
	}

	// ✅ 修复：使用 Store() 方法设置原子字段
	newInfo.SetPos(info.GetPos())
	newInfo.lastTime.Store(time.Now().UnixNano())
	newInfo.hits.Store(0) // 重置访问计数

	// ✅ 复制标志位（并发安全），但重置 isDirty
	newInfo.flags.Store(info.flags.Load() &^ 0x01) // 保留 isSplitted，清除 isDirty

	// 复制序列化缓冲区
	if info.buff != nil {
		newInfo.buff = make([]byte, len(info.buff))
		copy(newInfo.buff, info.buff)
	}

	return newInfo
}

// updateChildRef 更新子节点引用
//
// 从父节点的 children 数组中找到旧的孩子引用，将其替换为新的孩子引用
func (ccow *CCOWManager) updateChildRef(parentInfo, oldChild, newChild *PageInfo) error {
	// 获取父页面
	parentPage := parentInfo.GetPage()
	if parentPage == nil {
		// 父页面为 nil，跳过更新（可能只是占位符 PageInfo）
		return nil
	}

	// 只处理 InternalPage（叶子节点没有子节点）
	internalPage, ok := parentPage.(*InternalPage)
	if !ok {
		// 不是 InternalPage，跳过更新
		return nil
	}

	// 获取旧孩子的 PageID
	oldChildID := oldChild.GetPageID()

	// 在父节点的 children 数组中查找并替换
	for i, childRef := range internalPage.children {
		if childRef == nil {
			continue
		}

		childInfo := childRef.pInfo.Load()
		if childInfo == nil {
			continue
		}

		// 通过 PageID 匹配找到旧的孩子
		if childInfo.GetPageID() == oldChildID {
			// 创建新的 PageRef 指向新的 PageInfo
			newChildRef := NewPageRefWithInfo(newChild)

			// 替换子节点引用
			internalPage.children[i] = newChildRef

			// 更新父节点版本
			internalPage.version++
			break
		}
	}

	return nil
}

// FlushDirtyPages 刷出脏页到磁盘
func (ccow *CCOWManager) FlushDirtyPages(ctx context.Context) error {
	// 获取所有脏页
	dirtyPages := ccow.GetDirtyPages()
	if len(dirtyPages) == 0 {
		return nil
	}

	// 构建脏页集合
	dirtyPageSet := make(map[*PageInfo]bool, len(dirtyPages))
	for _, pageInfo := range dirtyPages {
		dirtyPageSet[pageInfo] = true
	}

	// 收集脏页（自底向上写入）
	if err := ccow.gc.collectDirtyPages(dirtyPageSet); err != nil {
		return fmt.Errorf("collect dirty pages failed: %w", err)
	}

	// 清除脏页标记
	for _, pageInfo := range dirtyPages {
		ccow.ClearDirty(pageInfo)
	}

	return nil
}

// VerifySnapshotIntegrity 验证快照完整性
func (ccow *CCOWManager) VerifySnapshotIntegrity(snapshotID uint64) (bool, error) {
	snapshot, exists := ccow.GetSnapshot(snapshotID)
	if !exists {
		return false, fmt.Errorf("snapshot not found")
	}

	// 验证根节点
	if snapshot.RootRef == nil {
		return false, fmt.Errorf("snapshot root ref is nil")
	}

	rootInfo := snapshot.RootRef.GetPageInfo()
	if rootInfo == nil {
		return false, fmt.Errorf("snapshot root info is nil")
	}

	// 验证版本
	if rootInfo.GetPageVersion() != snapshot.Version {
		return false, nil
	}

	return true, nil
}
