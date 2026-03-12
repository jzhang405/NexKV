package btree

import (
	"bytes"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// InternalPage 内部节点
// 存储键和子节点引用，用于索引
type InternalPage struct {
	pageID   model.PageID // 页面 ID
	version  uint64       // 版本号（用于 CCOW）
	keys     [][]byte     // 键数组（有序）
	children []*PageRef   // 子节点引用（使用 PageRef）
}

// NewInternalPage 创建新的内部页面
func NewInternalPage(pageID model.PageID) *InternalPage {
	return &InternalPage{
		pageID:   pageID,
		version:  0,
		keys:     make([][]byte, 0, 16),   // 预分配容量
		children: make([]*PageRef, 0, 17), // 子节点比键多 1
	}
}

// GetPageID 获取页面 ID
func (p *InternalPage) GetPageID() model.PageID {
	return p.pageID
}

// SetPageID 设置页面 ID
func (p *InternalPage) SetPageID(pageID model.PageID) {
	p.pageID = pageID
}

// GetVersion 获取版本号
func (p *InternalPage) GetVersion() uint64 {
	return p.version
}

// SetVersion 设置版本号
func (p *InternalPage) SetVersion(version uint64) {
	p.version = version
}

// IncrementVersion 递增版本号
func (p *InternalPage) IncrementVersion() {
	p.version++
}

// NumKeys 获取键数量
func (p *InternalPage) NumKeys() int {
	return len(p.keys)
}

// NumChildren 获取子节点数量
func (p *InternalPage) NumChildren() int {
	return len(p.children)
}

// IsLeaf 判断是否为叶子节点（实现 Page 接口）
func (p *InternalPage) IsLeaf() bool {
	return false
}

// Children 获取所有子节点引用（用于遍历）
func (p *InternalPage) Children() []*PageRef {
	return p.children
}

// GetChild 获取指定索引的子节点
func (p *InternalPage) GetChild(idx int) *PageRef {
	if idx < 0 || idx >= len(p.children) {
		return nil
	}
	return p.children[idx]
}

// SetChild 设置子节点
func (p *InternalPage) SetChild(idx int, child *PageRef) error {
	if idx < 0 || idx >= len(p.children) {
		return fmt.Errorf("child index %d out of range [0, %d)", idx, len(p.children)-1)
	}
	p.children[idx] = child
	p.version++
	return nil
}

// search 二分查找键的位置
// 返回：子节点索引
func (p *InternalPage) search(key []byte) int {
	left, right := 0, len(p.keys)-1

	for left <= right {
		mid := left + (right-left)/2
		cmp := bytes.Compare(p.keys[mid], key)

		if cmp == 0 {
			return mid + 1 // 匹配，返回右子节点
		} else if cmp < 0 {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}

	return left
}

// FindChild 查找键对应的子节点
// 返回：子节点引用，是否精确匹配
func (p *InternalPage) FindChild(key []byte) (*PageRef, bool) {
	idx := p.search(key)

	if idx < len(p.keys) && bytes.Equal(p.keys[idx], key) {
		// 精确匹配，返回右子节点
		if idx+1 < len(p.children) {
			return p.children[idx+1], true
		}
		return nil, false
	}

	// 范围查询，返回对应子节点
	// 如果 idx >= len(children)，返回最后一个子节点
	if idx < len(p.children) {
		return p.children[idx], false
	}
	if len(p.children) > 0 {
		return p.children[len(p.children)-1], false
	}

	return nil, false
}

// FindChildRef 查找键对应的子节点引用（简化版，用于 searchPath）
// 返回：子节点引用（不关心是否精确匹配）
//
// BTree 搜索逻辑：
// - 如果 key == keys[i]，搜索应该在右子节点（children[i+1]）
// - 如果 keys[i-1] < key < keys[i]，搜索应该在子节点（children[i]）
func (p *InternalPage) FindChildRef(key []byte) *PageRef {
	idx := p.search(key)

	// 边界检查
	if idx < 0 || idx >= len(p.children) {
		// 返回最右边的子节点
		if len(p.children) > 0 {
			return p.children[len(p.children)-1]
		}
		return nil
	}

	return p.children[idx]
}

// Insert 插入键和子节点
// 返回：是否插入成功
func (p *InternalPage) Insert(key []byte, child *PageRef) (bool, error) {
	idx := p.search(key)

	// 插入键
	p.keys = insertSlice(p.keys, idx, key)

	// 插入子节点引用
	p.children = insertSlice(p.children, idx, child)
	p.version++

	return true, nil
}

// Delete 删除键和子节点
// 返回：被删除的子节点引用
func (p *InternalPage) Delete(key []byte) (*PageRef, error) {
	idx, found := p.findKeyIndex(key)
	if !found {
		return nil, fmt.Errorf("key not found")
	}

	// 删除键
	p.keys = append(p.keys[:idx], p.keys[idx+1:]...)

	// 删除子节点引用
	removedChild := p.children[idx+1]
	p.children = append(p.children[:idx+1], p.children[idx+2:]...)
	p.version++

	return removedChild, nil
}

// findKeyIndex 查找键的索引
func (p *InternalPage) findKeyIndex(key []byte) (int, bool) {
	left, right := 0, len(p.keys)-1

	for left <= right {
		mid := left + (right-left)/2
		cmp := bytes.Compare(p.keys[mid], key)

		if cmp == 0 {
			return mid, true
		} else if cmp < 0 {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}

	return left, false
}

// UpdateKey 更新键（用于键修改场景）
// 返回：是否更新成功
func (p *InternalPage) UpdateKey(oldKey, newKey []byte) (bool, error) {
	idx, found := p.findKeyIndex(oldKey)
	if !found {
		return false, fmt.Errorf("key not found")
	}

	// 检查新键的位置是否正确
	if idx > 0 && bytes.Compare(p.keys[idx-1], newKey) > 0 {
		return false, fmt.Errorf("new key would violate ordering")
	}
	if idx < len(p.keys)-1 && bytes.Compare(p.keys[idx+1], newKey) < 0 {
		return false, fmt.Errorf("new key would violate ordering")
	}

	p.keys[idx] = newKey
	p.version++
	return true, nil
}

// Split 分裂页面
// 返回：新页面，分裂键（提升到父节点）
// 均匀分裂策略：将键平均分配到两个页面，中间的键提升到父节点
func (p *InternalPage) Split() (*InternalPage, []byte, error) {
	if len(p.keys) < 2 {
		return nil, nil, fmt.Errorf("cannot split page with less than 2 keys")
	}

	mid := len(p.keys) / 2

	// 分裂键（提升到父节点）
	splitKey := p.keys[mid]

	// 创建新页面，包含中间键之后的键和子节点
	newPage := NewInternalPage(model.PageID(p.pageID + 1)) // 临时 ID

	// 复制后半部分键（不包含分裂键）
	newPage.keys = append(newPage.keys, p.keys[mid+1:]...)

	// 复制后半部分子节点（从 mid+1 到末尾）
	newPage.children = append(newPage.children, p.children[mid+1:]...)

	// 当前页面保留中间键之前的键和子节点
	p.keys = p.keys[:mid]
	p.children = p.children[:mid+1] // 保留前 mid+1 个子节点（0 到 mid）
	p.version++

	return newPage, splitKey, nil
}

// Clone 克隆页面（Copy-on-Write）
func (p *InternalPage) Clone() *InternalPage {
	newKeys := make([][]byte, len(p.keys))
	copy(newKeys, p.keys)

	newChildren := make([]*PageRef, len(p.children))
	copy(newChildren, p.children)

	return &InternalPage{
		pageID:   p.pageID,
		version:  p.version,
		keys:     newKeys,
		children: newChildren,
	}
}

// Serialize 序列化页面
func (p *InternalPage) Serialize() ([]byte, error) {
	var buf bytes.Buffer

	// 写入 pageID (8 bytes)
	if err := binaryWrite(&buf, uint64ToBytes(uint64(p.pageID))); err != nil {
		return nil, err
	}

	// 写入 version (8 bytes)
	if err := binaryWrite(&buf, uint64ToBytes(p.version)); err != nil {
		return nil, err
	}

	// 写入键数量 (4 bytes)
	numKeys := uint32(len(p.keys))
	if err := binaryWrite(&buf, uint32ToBytes(numKeys)); err != nil {
		return nil, err
	}

	// 写入子节点数量（4 bytes）
	numChildren := uint32(len(p.children))
	if err := binaryWrite(&buf, uint32ToBytes(numChildren)); err != nil {
		return nil, err
	}

	// 写入键
	for i := 0; i < len(p.keys); i++ {
		// 写入键长度 (2 bytes)
		keyLen := uint16(len(p.keys[i]))
		if err := binaryWrite(&buf, uint16ToBytes(keyLen)); err != nil {
			return nil, err
		}

		// 写入键数据
		if err := binaryWrite(&buf, p.keys[i]); err != nil {
			return nil, err
		}
	}

	// 写入子节点 ID（暂时使用 PageID，后续可以改为位置编码）
	for i := 0; i < len(p.children); i++ {
		var childID uint64
		if p.children[i] != nil {
			if info := p.children[i].GetPageInfo(); info != nil {
				childID = info.GetPageID()
			}
		}
		if err := binaryWrite(&buf, uint64ToBytes(childID)); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// DeserializeInternalPage 反序列化内部页面
func DeserializeInternalPage(data []byte) (*InternalPage, error) {
	reader := bytes.NewReader(data)

	// 读取 pageID
	pageIDBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, fmt.Errorf("failed to read pageID: %w", err)
	}
	pageID := model.PageID(bytesToUint64(pageIDBytes))

	// 读取 version
	versionBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	version := bytesToUint64(versionBytes)

	// 读取键数量
	numKeysBytes, err := readBytes(reader, 4)
	if err != nil {
		return nil, fmt.Errorf("failed to read numKeys: %w", err)
	}
	numKeys := bytesToUint32(numKeysBytes)

	// 读取子节点数量
	numChildrenBytes, err := readBytes(reader, 4)
	if err != nil {
		return nil, fmt.Errorf("failed to read numChildren: %w", err)
	}
	numChildren := bytesToUint32(numChildrenBytes)

	// 创建页面
	page := &InternalPage{
		pageID:   pageID,
		version:  version,
		keys:     make([][]byte, 0, numKeys),
		children: make([]*PageRef, 0, numChildren),
	}

	// 读取键
	for i := 0; i < int(numKeys); i++ {
		// 读取键长度
		keyLenBytes, err := readBytes(reader, 2)
		if err != nil {
			return nil, fmt.Errorf("failed to read key len: %w", err)
		}
		keyLen := bytesToUint16(keyLenBytes)

		// 读取键数据
		key, err := readBytes(reader, int(keyLen))
		if err != nil {
			return nil, fmt.Errorf("failed to read key data: %w", err)
		}
		page.keys = append(page.keys, key)
	}

	// 读取子节点 ID
	for i := 0; i < int(numChildren); i++ {
		childIDBytes, err := readBytes(reader, 8)
		if err != nil {
			return nil, fmt.Errorf("failed to read childID: %w", err)
		}
		childID := bytesToUint64(childIDBytes)

		// 创建 PageRef（暂时使用 PageID，后续可以通过位置编码加载）
		// TODO: 后续需要通过位置编码加载实际的 Page
		childRef := NewPageRef()
		// 注意：这里暂时不加载页面，只保存 PageID
		// 实际使用时需要通过 ChunkManager 加载

		// 临时：创建一个 PageInfo 包含 PageID
		// 这是为了反序列化的完整性，实际使用时需要重新设计
		_ = childID
		_ = childRef

		// Phase 1: 简化处理，暂时不创建 PageInfo
		// Phase 2: 实现通过位置编码加载 Page 的机制
		page.children = append(page.children, nil) // 暂时设为 nil
	}

	return page, nil
}

// UpdateChildrenRef 批量更新子节点引用
// 用于 Split 后更新子节点的 parentRef
func (p *InternalPage) UpdateChildrenRef(startIdx int) error {
	if startIdx < 0 || startIdx >= len(p.children) {
		return fmt.Errorf("start index %d out of range [0, %d]", startIdx, len(p.children)-1)
	}

	// 更新从 startIdx 开始的所有子节点的父引用
	for i := startIdx; i < len(p.children); i++ {
		if p.children[i] != nil {
			// TODO: 设置父引用
			// p.children[i].SetParentRef(parentRef)
		}
	}

	p.version++
	return nil
}

// Size 估算页面大小（字节）
func (p *InternalPage) Size() int {
	size := 8 + 8 + 4 + 4 // pageID + version + numKeys + numChildren

	for i := 0; i < len(p.keys); i++ {
		size += 2 + len(p.keys[i]) // keyLen + key
	}

	// 子节点引用（暂时使用 PageID，8 bytes）
	size += len(p.children) * 8

	return size
}

// IsFull 判断页面是否已满
func (p *InternalPage) IsFull(maxKeys int) bool {
	return len(p.keys) >= maxKeys
}

// GetMinKey 获取最小键
func (p *InternalPage) GetMinKey() []byte {
	if len(p.keys) == 0 {
		return nil
	}
	return p.keys[0]
}

// GetMaxKey 获取最大键
func (p *InternalPage) GetMaxKey() []byte {
	if len(p.keys) == 0 {
		return nil
	}
	return p.keys[len(p.keys)-1]
}
