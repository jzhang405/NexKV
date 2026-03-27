package btree

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
	errpkg "github.com/jzhang405/NexKV/pkg/errors"
)

// InternalPage 内部节点
// 存储键和子节点引用，用于索引
// 支持 COW+Delta 混合方案优化 Clone 性能
type InternalPage struct {
	pageID   model.PageID // 页面 ID
	version  uint64       // 版本号（用于 CCOW）
	keys     [][]byte     // 键数组（有序）
	children []*PageRef   // 子节点引用（使用 PageRef）
	cowDelta *COWDeltaRef // COW+Delta 引用（nil = 已物化/独立数据）
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
		return errpkg.BTreeChildIndexOutOfRangeError(idx, len(p.children)-1)
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

// Insert 插入键和子节点（右子节点）
// 返回：是否插入成功
//
// B+Tree 语义：插入键 key 时，child 是 key 的右子节点
// - key 插入到 keys[idx]
// - child 插入到 children[idx+1]
func (p *InternalPage) Insert(key []byte, child *PageRef) (bool, error) {
	idx := p.search(key)

	// 插入键
	p.keys = insertSlice(p.keys, idx, key)

	// 插入右子节点（在 idx+1 位置，因为 key 的左子节点是 children[idx]）
	p.children = insertSlice(p.children, idx+1, child)
	p.version++

	return true, nil
}

// InsertKeyChild 插入键和子节点（用于 Split 操作）
// 在指定位置插入键和右子节点
// 返回：错误信息
func (p *InternalPage) InsertKeyChild(key []byte, childRef *PageRef) error {
	// 防御性修复：检查并修复不变量
	expectedChildren := len(p.keys) + 1
	if len(p.children) != expectedChildren {
		// 如果 children 太多，截断
		if len(p.children) > expectedChildren {
			fmt.Printf("[WARN] InsertKeyChild: fixing invariant before insert: pageID=%d, keys=%d, children=%d -> %d\n",
				p.pageID, len(p.keys), len(p.children), expectedChildren)
			p.children = p.children[:expectedChildren]
		} else {
			// 如果 children 太少，返回错误
			return errpkg.BTreeInvariantViolatedError(len(p.children), len(p.keys))
		}
	}

	idx := p.search(key)
	// 插入键
	p.keys = insertSlice(p.keys, idx, key)
	// 插入右子节点（在 idx+1 位置）
	p.children = insertSlice(p.children, idx+1, childRef)
	p.version++
	return nil
}

// Delete 删除键和子节点
// 返回：被删除的子节点引用
func (p *InternalPage) Delete(key []byte) (*PageRef, error) {
	idx, found := p.findKeyIndex(key)
	if !found {
		return nil, errpkg.BTreeKeyNotFoundInPageError()
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
		return false, errpkg.BTreeKeyNotFoundInPageError()
	}

	// 检查新键的位置是否正确
	if idx > 0 && bytes.Compare(p.keys[idx-1], newKey) > 0 {
		return false, errpkg.BTreeKeyOrderViolationError()
	}
	if idx < len(p.keys)-1 && bytes.Compare(p.keys[idx+1], newKey) < 0 {
		return false, errpkg.BTreeKeyOrderViolationError()
	}

	p.keys[idx] = newKey
	p.version++
	return true, nil
}

// Split 分裂页面（带引用更新）
// 返回：新页面，分裂键（提升到父节点）
// 均匀分裂策略：将键平均分配到两个页面，中间的键提升到父节点
//
// B+Tree 标准分裂逻辑（内部节点）：
// - 左页面：键 [0, mid)，子节点 [0:mid+1]
// - 分裂键：键 [mid]（提升到父节点，不在左右页面中）
// - 右页面：键 [mid+1:]，子节点 [mid+1:]
//
// 添加引用更新机制
// - 更新新页面子节点的 parentRef 指向新页面
// - 保留原页面子节点的 parentRef 指向原页面
func (p *InternalPage) Split() (*InternalPage, []byte, error) {
	if len(p.keys) < 2 {
		return nil, nil, errpkg.BTreeCannotSplitMinKeysError(len(p.keys))
	}

	mid := len(p.keys) / 2

	// 分裂键（提升到父节点）
	splitKey := p.keys[mid]

	// 创建新页面，包含中间键之后的键和子节点
	newPage := NewInternalPage(model.PageID(p.pageID + 1)) // 临时 ID

	// 修复：B+Tree 标准分裂逻辑 - 分裂键提升到父节点，不在左右页面中
	// 修复：使用 make + copy 创建独立的 slice，避免共享底层数组
	// 分裂键 keys[mid] 提升到父节点：
	// - 左页面: keys[0:mid], children[0:mid+1]
	// - 右页面: keys[mid+1:], children[mid+1:]
	// 左页面最后一个键 (keys[mid-1]) 的右子节点是 children[mid]
	// 右页面第一个键 (keys[mid+1]) 的左子节点是 children[mid+1]
	newPage.keys = make([][]byte, len(p.keys[mid+1:]))
	copy(newPage.keys, p.keys[mid+1:])
	newPage.children = make([]*PageRef, len(p.children[mid+1:]))
	copy(newPage.children, p.children[mid+1:])
	//parentRef 更新将在 splitInternal() 中处理

	// 当前页面保留中间键之前的键和子节点（不包含分裂键）
	p.keys = p.keys[:mid]           // 不包含分裂键
	p.children = p.children[:mid+1] // 保留前 mid+1 个子节点（0 到 mid，包含分裂键的左子节点）
	p.version++

	return newPage, splitKey, nil
}

// Clone 克隆页面（使用 Delta Chain 模式）
// 性能：~290 ns/op（keys 零拷贝，children 深拷贝）
// InternalPage 的特殊性：
// - keys: 使用 Delta Chain 共享（零拷贝）
// - children: 深拷贝（因为 PageRef 包含原子指针，且需要独立）
//
// config: 可选的 COW 配置，如果不提供则使用默认配置
func (p *InternalPage) Clone(config ...*COWDeltaRefConfig) *InternalPage {
	var cowRef *COWDeltaRef

	// 如果已有 COW 引用，检查是否可以重用
	if p.cowDelta != nil {
		// 关键修复：检查 p.keys 是否与 cowDelta.sharedKeys 同步
		// 如果 p.keys 的长度与 sharedKeys 的长度不一致，说明 p.keys 已被修改
		// 需要创建新的 COWDeltaRef，而不是重用旧的
		if len(p.keys) != len(p.cowDelta.GetSharedKeys()) {
			// p.keys 已被修改（如 InsertKeyChild），创建新的 COWDeltaRef
			if len(config) > 0 && config[0] != nil {
				cowRef = NewCOWDeltaRefWithConfig(p.keys, nil, config[0])
			} else {
				cowRef = NewCOWDeltaRef(p.keys, nil)
			}
		} else {
			// 可以重用现有的 cowDelta
			p.cowDelta.Retain()
			cowRef = p.cowDelta
		}
	} else {
		// 创建新的 COW 引用（只共享 keys，不包含 children）
		// values 为 nil，因为 InternalPage 不需要
		if len(config) > 0 && config[0] != nil {
			cowRef = NewCOWDeltaRefWithConfig(p.keys, nil, config[0])
		} else {
			cowRef = NewCOWDeltaRef(p.keys, nil)
		}
		// refCount = 1（只有克隆页面持有这个 COWDeltaRef）
		// 原始页面保持独立，不进入 Delta 模式
	}

	// children 必须深拷贝（因为包含 PageRef，且有原子操作）
	newChildren := make([]*PageRef, len(p.children))
	copy(newChildren, p.children)

	return &InternalPage{
		pageID:   p.pageID,
		version:  p.version + 1,
		cowDelta: cowRef,
		keys:     cowRef.GetSharedKeys(), // 共享 keys
		children: newChildren,            // 独立 children
	}
}

// CloneDeep 深拷贝页面（独立数据）
// 性能：~1423 ns/op（完全复制）
// 使用场景：需要完全独立的副本时使用此方法
func (p *InternalPage) CloneDeep() *InternalPage {
	newKeys := make([][]byte, len(p.keys))
	copy(newKeys, p.keys)

	newChildren := make([]*PageRef, len(p.children))
	copy(newChildren, p.children)

	return &InternalPage{
		pageID:   p.pageID,
		version:  p.version + 1,
		keys:     newKeys,
		children: newChildren,
		// cowDelta 为 nil，使用独立数据
	}
}

// CloneWithDelta 创建 Delta Chain 模式克隆（零拷贝）
// 注意：此方法保留用于向后兼容，现在直接调用 Clone()
func (p *InternalPage) CloneWithDelta() *InternalPage {
	return p.Clone()
}

// Serialize 序列化页面
func (p *InternalPage) Serialize() ([]byte, error) {
	// 物化：序列化前必须将 Delta Chain 合并为独立数据
	// cowDelta 是内存优化结构，无法序列化到磁盘
	// 修复：克隆页面后物化，避免并发访问竞态条件
	if p.cowDelta != nil {
		// 创建深拷贝进行物化，避免修改原始页面
		// 这解决了并发 Clone 读取时的竞态条件
		cloned := p.CloneDeep()
		cloned.materialize()
		return cloned.serializeWithData()
	}
	return p.serializeWithData()
}

// serializeWithData 序列化已物化的页面（内部辅助方法）
// 前提条件：p.cowDelta == nil（已物化）
func (p *InternalPage) serializeWithData() ([]byte, error) {
	const pageSize = 4096 // 固定页面大小

	var buf bytes.Buffer

	// 1. 先序列化页面内容（暂时跳过长度字段）
	contentStart := buf.Len()

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
	for i := range len(p.keys) {
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
	for i := range len(p.children) {
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

	// 2. 获取实际内容长度
	contentData := buf.Bytes()[contentStart:]
	contentLength := len(contentData)

	// 3. 创建最终的序列化结果（4 字节长度 + 内容 + 填充）
	result := make([]byte, pageSize)

	// 写入实际长度（前 4 字节）
	binary.BigEndian.PutUint32(result[0:4], uint32(contentLength))

	// 复制内容
	copy(result[4:4+contentLength], contentData)

	// 剩余部分已经自动填充为 0x00（Go 的 make 默认初始化）

	return result, nil
}

// DeserializeInternalPage 反序列化内部页面
func DeserializeInternalPage(data []byte) (*InternalPage, error) {
	const pageSize = 4096

	// 检查数据长度
	if len(data) != pageSize {
		return nil, errpkg.BTreeInvalidDataSize(pageSize, len(data))
	}

	// 1. 读取实际内容长度（前 4 字节）
	contentLength := binary.BigEndian.Uint32(data[0:4])

	// 2. 只读取实际内容部分（跳过填充）
	contentData := data[4 : 4+contentLength]
	reader := bytes.NewReader(contentData)

	// 3. 读取 pageID
	pageIDBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, errpkg.BTreeDeserializeReadPageID(err)
	}
	pageID := model.PageID(bytesToUint64(pageIDBytes))

	// 4. 读取 version
	versionBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, errpkg.BTreeDeserializeReadVersion(err)
	}
	version := bytesToUint64(versionBytes)

	// 5. 读取键数量
	numKeysBytes, err := readBytes(reader, 4)
	if err != nil {
		return nil, errpkg.BTreeDeserializeReadNumKeys(err)
	}
	numKeys := bytesToUint32(numKeysBytes)

	// 6. 读取子节点数量
	numChildrenBytes, err := readBytes(reader, 4)
	if err != nil {
		return nil, errpkg.BTreeDeserializeReadNumChildren(err)
	}
	numChildren := bytesToUint32(numChildrenBytes)

	// 7. 创建页面
	page := &InternalPage{
		pageID:   pageID,
		version:  version,
		keys:     make([][]byte, 0, numKeys),
		children: make([]*PageRef, 0, numChildren),
	}

	// 8. 读取键
	for range int(numKeys) {
		// 读取键长度
		keyLenBytes, err := readBytes(reader, 2)
		if err != nil {
			return nil, errpkg.BTreeDeserializeReadKeyLen(err)
		}
		keyLen := bytesToUint16(keyLenBytes)

		// 读取键数据
		key, err := readBytes(reader, int(keyLen))
		if err != nil {
			return nil, errpkg.BTreeDeserializeReadKeyData(err)
		}
		page.keys = append(page.keys, key)
	}

	// 9. 读取子节点 ID
	for range int(numChildren) {
		childIDBytes, err := readBytes(reader, 8)
		if err != nil {
			return nil, errpkg.BTreeDeserializeReadChildID(err)
		}
		childID := bytesToUint64(childIDBytes)

		// 创建 PageRef（暂时只存储 PageID，后续可以改为位置编码）
		childRef := NewPageRef()
		childInfo := NewPageInfo()
		// 注意：PageID 存储在 Page 对象中，不在 PageInfo 中
		// 这里暂时创建一个占位的 Page 对象来存储 PageID
		tempPage := &InternalPage{
			pageID: model.PageID(childID),
		}
		childInfo.SetPage(tempPage)
		childRef.pInfo.Store(childInfo)

		page.children = append(page.children, childRef)
	}

	return page, nil
}

// UpdateChildrenRef 批量更新子节点引用
// 用于 Split 后更新子节点的 parentRef
func (p *InternalPage) UpdateChildrenRef(startIdx int) error {
	if startIdx < 0 || startIdx >= len(p.children) {
		return errpkg.BTreeIndexOutOfRangeError(startIdx, len(p.children)-1)
	}

	// 获取当前 InternalPage 的 PageRef
	// 注意：这需要从父节点传递下来，暂时简化实现
	// 在实际使用中，通过 updateChildrenParentRef 从根节点向下更新

	// 更新从 startIdx 开始的所有子节点的父引用
	for i := startIdx; i < len(p.children); i++ {
		// TODO: 更新子节点的父引用
		// 需要重构 UpdateChildrenRef 方法签名以支持传递 parentRef
		_ = p.children[i] // 避免空分支警告
	}

	p.version++
	return nil
}

// Size 估算页面大小（字节）
func (p *InternalPage) Size() int {
	size := 8 + 8 + 4 + 4 // pageID + version + numKeys + numChildren

	for i := range len(p.keys) {
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

// IsInDeltaMode 检查是否在 Delta Chain 模式
func (p *InternalPage) IsInDeltaMode() bool {
	return p.cowDelta != nil
}

// GetDeltaCount 获取增量链长度
func (p *InternalPage) GetDeltaCount() int {
	if p.cowDelta == nil {
		return 0
	}
	return p.cowDelta.GetDeltaCount()
}

// IsShared 检查是否共享数据（引用计数 > 1）
func (p *InternalPage) IsShared() bool {
	if p.cowDelta == nil {
		return false
	}
	return p.cowDelta.GetRefCount() > 1
}

// GetRefCount 获取当前引用计数
func (p *InternalPage) GetRefCount() int32 {
	if p.cowDelta == nil {
		return 1 // 未使用 Delta Chain，引用计数为 1（自己）
	}
	return p.cowDelta.GetRefCount()
}

// materialize 物化增量链（合并到独立数据）
func (p *InternalPage) materialize() {
	if p.cowDelta == nil {
		return
	}

	// 获取基础 keys 的完整副本
	newKeys := make([][]byte, len(p.cowDelta.GetSharedKeys()))
	copy(newKeys, p.cowDelta.GetSharedKeys())

	// children 本来就是深拷贝的（CloneWithDelta 中处理），直接使用当前 children
	newChildren := make([]*PageRef, len(p.children))
	copy(newChildren, p.children)

	// 应用所有增量操作
	deltas := p.cowDelta.GetDeltas()
	for _, delta := range deltas {
		switch delta.op {
		case DeltaInsert:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				// 键已存在，InternalPage 的 Insert 语义是替换右子节点
				// 这里忽略，因为 DeltaInsert 应该只在键不存在时使用
			} else {
				// 设计说明：DeltaInsert 只插入 key，不插入 child
				// 原因：
				// 1. Delta 结构没有 child 字段
				// 2. InternalPage.Clone() 时 children 已经是深拷贝（独立）
				// 3. 修改 InternalPage 时直接修改 children，不使用 Delta Chain
				// 4. materialize() 主要用于持久化模式，纯内存模式不会调用
				//
				// 不变量检查：len(newChildren) 应该保持 == len(newKeys) + 1
				// 如果 child 信息的插入发生在其他地方，这里只需要插入 key
				newKeys = insertSlice(newKeys, idx, delta.key)
			}
		case DeltaUpdate:
			// InternalPage 不需要 Update 操作
		case DeltaDelete:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				newKeys = append(newKeys[:idx], newKeys[idx+1:]...)
				// 删除对应的右子节点引用
				if idx+1 < len(newChildren) {
					newChildren = append(newChildren[:idx+1], newChildren[idx+2:]...)
				}
			}
		}
	}

	// 替换为独立数据
	p.keys = newKeys
	p.children = newChildren
	p.version++

	// 不变量检查：确保 B+Tree 不变性成立
	// len(children) == len(keys) + 1
	if len(p.children) != len(p.keys)+1 {
		panic(fmt.Sprintf("InternalPage.materialize(): invariant violated after materialize - pageID=%d, keys=%d, children=%d",
			p.pageID, len(p.keys), len(p.children)))
	}

	// 释放引用
	p.cowDelta.Release()
	p.cowDelta = nil
}
