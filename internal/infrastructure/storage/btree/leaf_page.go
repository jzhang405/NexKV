package btree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// LeafPage 叶子节点
// 存储键值对，是 BTree 的最底层节点
type LeafPage struct {
	pageID   model.PageID  // 页面 ID
	version  uint64        // 版本号（用于 CCOW）
	keys     [][]byte      // 键数组（有序）
	values   [][]byte      // 值数组（与 keys 一一对应）
	cowDelta *COWDeltaRef  // COW + Delta 引用，nil = 已物化/独立数据
	mu       sync.RWMutex  // 保护 cowDelta 的并发访问（仅用于 materialize）
}

// NewLeafPage 创建新的叶子页面
func NewLeafPage(pageID model.PageID) *LeafPage {
	return &LeafPage{
		pageID:   pageID,
		version:  0,
		keys:     make([][]byte, 0, InitialLeafCapacity), // 预分配容量
		values:   make([][]byte, 0, InitialLeafCapacity),
		cowDelta: nil, // 显式初始化为 nil（物化状态）
	}
}

// GetPageID 获取页面 ID
func (p *LeafPage) GetPageID() model.PageID {
	return p.pageID
}

// SetPageID 设置页面 ID
func (p *LeafPage) SetPageID(pageID model.PageID) {
	p.pageID = pageID
}

// GetVersion 获取版本号
func (p *LeafPage) GetVersion() uint64 {
	return p.version
}

// SetVersion 设置版本号
func (p *LeafPage) SetVersion(version uint64) {
	p.version = version
}

// IncrementVersion 递增版本号
func (p *LeafPage) IncrementVersion() {
	p.version++
}

// NumKeys 获取键值对数量
func (p *LeafPage) NumKeys() int {
	return len(p.keys)
}

// IsLeaf 判断是否为叶子节点（实现 Page 接口）
func (p *LeafPage) IsLeaf() bool {
	return true
}

// Get 获取键对应的值
func (p *LeafPage) Get(key []byte) ([]byte, bool) {
	// 如果有 COW 引用，先检查增量链
	if p.cowDelta != nil {
		// 反向遍历增量（最新的优先）
		deltas := p.cowDelta.GetDeltas()

		for i := len(deltas) - 1; i >= 0; i-- {
			delta := deltas[i]
			if bytes.Equal(delta.key, key) {
				switch delta.op {
				case DeltaInsert, DeltaUpdate:
					return delta.value, true
				case DeltaDelete:
					return nil, false
				}
			}
		}
	}

	// 增量链中未找到，查找基础数据
	idx, found := p.search(key)
	if !found {
		return nil, false
	}
	return p.values[idx], true
}

// search 二分查找键的位置
// 返回：索引，是否找到
func (p *LeafPage) search(key []byte) (int, bool) {
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

// Insert 插入键值对（COW + Delta Chain 混合方案）
// 返回：是否插入成功（false 表示键已存在，进行了更新）
func (p *LeafPage) Insert(key, value []byte) (bool, error) {
	// 如果有 COW 引用，使用增量模式
	if p.cowDelta != nil {
		// 检查是否需要物化
		if p.cowDelta.ShouldMaterialize(len(p.keys), p.cowDelta.GetRefCount()) {
			p.materialize()
			return p.insertDirect(key, value)
		}

		// 先检查键是否存在，决定是 Insert 还是 Update
		_, found := p.search(key)
		if found {
			// 键已存在，记录更新增量
			p.cowDelta.AppendDelta(Delta{
				op:    DeltaUpdate,
				key:   key,
				value: value,
			})
			return false, nil
		}

		// 键不存在，记录插入增量
		p.cowDelta.AppendDelta(Delta{
			op:    DeltaInsert,
			key:   key,
			value: value,
		})
		p.version++
		return true, nil
	}

	// 物化状态：直接修改
	return p.insertDirect(key, value)
}

// insertDirect 直接插入（物化状态）
func (p *LeafPage) insertDirect(key, value []byte) (bool, error) {
	idx, found := p.search(key)
	if found {
		p.values[idx] = value
		return false, nil
	}

	p.keys = insertSlice(p.keys, idx, key)
	p.values = insertSlice(p.values, idx, value)
	p.version++
	return true, nil
}

// insertSlice 在切片指定位置插入元素
func insertSlice[T any](slice []T, idx int, value T) []T {
	if len(slice) == cap(slice) {
		// 创建新切片时预留空间给新元素
		// 计算新容量：如果 cap 为 0，使用默认容量；否则翻倍
		newCap := cap(slice) * 2
		if newCap == 0 {
			newCap = DefaultSliceCapacity // 默认初始容量
		}
		newSlice := make([]T, len(slice)+1, newCap)
		copy(newSlice, slice[:idx])
		copy(newSlice[idx+1:], slice[idx:])
		newSlice[idx] = value
		return newSlice
	}

	slice = slice[:len(slice)+1]
	copy(slice[idx+1:], slice[idx:])
	slice[idx] = value
	return slice
}

// Delete 删除键值对（COW + Delta Chain 混合方案）
// 返回：是否删除成功（false 表示键不存在）
func (p *LeafPage) Delete(key []byte) (bool, error) {
	if p.cowDelta != nil {
		// 检查是否需要物化
		if p.cowDelta.ShouldMaterialize(len(p.keys), p.cowDelta.GetRefCount()) {
			p.materialize()
			return p.deleteDirect(key)
		}

		// 检查键是否存在
		_, found := p.search(key)
		if !found {
			return false, nil
		}

		// 记录删除增量
		p.cowDelta.AppendDelta(Delta{
			op:  DeltaDelete,
			key: key,
		})
		p.version++
		return true, nil
	}

	// 物化状态：直接删除
	return p.deleteDirect(key)
}

// deleteDirect 直接删除（物化状态）
func (p *LeafPage) deleteDirect(key []byte) (bool, error) {
	idx, found := p.search(key)
	if !found {
		return false, nil
	}

	p.keys = append(p.keys[:idx], p.keys[idx+1:]...)
	p.values = append(p.values[:idx], p.values[idx+1:]...)
	p.version++
	return true, nil
}

// Update 更新键的值
// 如果键不存在，会插入新键值对
// 返回：错误信息
func (p *LeafPage) Update(key, value []byte) error {
	idx, found := p.search(key)

	if found {
		// 键存在，更新值
		p.values[idx] = value
	} else {
		// 键不存在，插入新键值对
		p.keys = insertSlice(p.keys, idx, key)
		p.values = insertSlice(p.values, idx, value)
	}

	p.version++
	return nil
}

// Split 分裂页面
// 当页面满时，分裂为两个页面
// 返回：新页面，分裂键（提升到父节点）
// 均匀分裂策略：将键平均分配到两个页面，中间的键提升到父节点
//
// BTree 标准分裂逻辑：
// - 左页面：键 [0, mid)
// - 分裂键：键 [mid]（提升到父节点）
// - 右页面：键 (mid, end]
func (p *LeafPage) Split() (*LeafPage, []byte, error) {
	if len(p.keys) < 2 {
		return nil, nil, fmt.Errorf("cannot split page with less than 2 keys")
	}

	mid := len(p.keys) / 2

	// 分裂键（提升到父节点）
	// 对于奇数个键，取中间的键；对于偶数个键，取中间偏左的键
	splitKey := p.keys[mid]

	// 加锁保护 cowDelta 的检查和物化
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cowDelta != nil {
		p.materializeUnsafe()
	}

	// 创建新页面，包含分裂键及之后的键值对（包含分裂键）
	// ✅ Day 10-11: 修正分裂逻辑，右子节点包含分裂键
	newPage := NewLeafPage(model.PageID(p.pageID + 1))   // 临时 ID
	newPage.keys = append(newPage.keys, p.keys[mid:]...) // 包含分裂键
	newPage.values = append(newPage.values, p.values[mid:]...)
	newPage.cowDelta = nil // 分裂产生的新页面是独立数据

	// 当前页面保留分裂键之前的键值对（不包含分裂键）
	p.keys = p.keys[:mid]
	p.values = p.values[:mid]
	p.version++
	p.cowDelta = nil // 分裂后当前页面也是独立数据

	return newPage, splitKey, nil
}

// Clone 克隆页面（COW + Delta Chain 混合方案）
//
// 引用计数生命周期：
//   - NewCOWDeltaRef: refCount = 0（初始状态）
//   - Retain(): refCount += 1（首次调用后变为 1，表示自己持有）
//   - Clone(): refCount += 1（每次克隆增加引用）
//   - Release(): refCount -= 1（返回是否为最后一个引用）
//   - 当 refCount = 0 时，sharedKeys/sharedValues 可被回收
func (p *LeafPage) Clone() *LeafPage {
	// 加锁保护 cowDelta 的读取
	p.mu.RLock()
	defer p.mu.RUnlock()

	// 如果已有 COW 引用，增加引用计数
	if p.cowDelta != nil {
		// 验证 COWDeltaRef 的有效性
		if p.cowDelta.GetRefCount() <= 0 {
			// 引用计数无效，创建新的 COW 引用
			cowRef := NewCOWDeltaRef(p.keys, p.values)
			cowRef.Retain()
			return &LeafPage{
				pageID:   p.pageID,
				version:  p.version,
				cowDelta: cowRef,
				keys:     cowRef.sharedKeys,
				values:   cowRef.sharedValues,
			}
		}

		p.cowDelta.Retain()
		return &LeafPage{
			pageID:   p.pageID,
			version:  p.version,
			cowDelta: p.cowDelta,
			keys:     p.cowDelta.sharedKeys,
			values:   p.cowDelta.sharedValues,
		}
	}

	// 创建新的 COW 引用
	// NewCOWDeltaRef 将 refCount 初始化为 0
	// Retain() 将 refCount 增加到 1，表示当前页面持有此引用
	cowRef := NewCOWDeltaRef(p.keys, p.values)
	cowRef.Retain() // refCount: 0 → 1

	return &LeafPage{
		pageID:   p.pageID,
		version:  p.version, // 保持版本号一致
		cowDelta: cowRef,
		keys:     cowRef.sharedKeys,
		values:   cowRef.sharedValues,
	}
}

// forceCloneDeep 强制完整深拷贝（用于 CloneDeep）
// 不使用 COW 共享，确保完全独立
func (p *LeafPage) forceCloneDeep() *LeafPage {
	// 如果有 cowDelta，先物化
	p.mu.Lock()
	if p.cowDelta != nil {
		p.materializeUnsafe()
	}
	p.mu.Unlock()

	// 完整深拷贝 keys 和 values
	newKeys := make([][]byte, len(p.keys))
	copy(newKeys, p.keys)

	newValues := make([][]byte, len(p.values))
	copy(newValues, p.values)

	return &LeafPage{
		pageID:   p.pageID,
		version:  p.version,
		keys:     newKeys,
		values:   newValues,
		cowDelta: nil, // 深拷贝后没有 cowDelta
	}
}

// Serialize 序列化页面
// 返回：序列化后的字节数组
// 注意：如果 cowDelta 存在，会先物化
func (p *LeafPage) Serialize() ([]byte, error) {
	// 加锁保护 cowDelta 的检查和物化
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cowDelta != nil {
		p.materializeUnsafe()
	}

	ps := NewPageSerializer()

	// 使用 PageSerializer 写入公共头部
	if err := ps.WriteHeader(uint64(p.pageID), p.version); err != nil {
		return nil, err
	}

	// 写入键数量
	if err := ps.WriteKeyCount(len(p.keys)); err != nil {
		return nil, err
	}

	// 写入键值对
	for i := 0; i < len(p.keys); i++ {
		if err := ps.WriteKeyValue(p.keys[i], p.values[i]); err != nil {
			return nil, err
		}
	}

	// 使用 Finalize() 完成序列化（添加长度前缀和填充）
	return ps.Finalize()
}

// DeserializeLeafPage 反序列化叶子页面
func DeserializeLeafPage(data []byte) (*LeafPage, error) {
	const pageSize = 4096

	// 检查数据长度
	if len(data) != pageSize {
		return nil, fmt.Errorf("invalid data size: expected %d bytes, got %d", pageSize, len(data))
	}

	// 1. 读取实际内容长度（前 4 字节）
	contentLength := binary.BigEndian.Uint32(data[0:4])

	// 2. 只读取实际内容部分（跳过填充）
	contentData := data[4 : 4+contentLength]
	reader := bytes.NewReader(contentData)

	// 3. 读取 pageID
	pageIDBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, fmt.Errorf("failed to read pageID: %w", err)
	}
	pageID := model.PageID(bytesToUint64(pageIDBytes))

	// 4. 读取 version
	versionBytes, err := readBytes(reader, 8)
	if err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	version := bytesToUint64(versionBytes)

	// 5. 读取键数量
	numKeysBytes, err := readBytes(reader, 4)
	if err != nil {
		return nil, fmt.Errorf("failed to read numKeys: %w", err)
	}
	numKeys := bytesToUint32(numKeysBytes)

	// 6. 创建页面
	page := &LeafPage{
		pageID:   pageID,
		version:  version,
		keys:     make([][]byte, 0, numKeys),
		values:   make([][]byte, 0, numKeys),
		cowDelta: nil, // 反序列化后是物化状态
	}

	// 7. 读取键值对
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

		// 读取值长度
		valueLenBytes, err := readBytes(reader, 2)
		if err != nil {
			return nil, fmt.Errorf("failed to read value len: %w", err)
		}
		valueLen := bytesToUint16(valueLenBytes)

		// 读取值数据
		value, err := readBytes(reader, int(valueLen))
		if err != nil {
			return nil, fmt.Errorf("failed to read value data: %w", err)
		}
		page.values = append(page.values, value)
	}

	return page, nil
}

// Range 遍历所有键值对
func (p *LeafPage) Range(fn func(key, value []byte) error) error {
	for i := 0; i < len(p.keys); i++ {
		if err := fn(p.keys[i], p.values[i]); err != nil {
			return err
		}
	}
	return nil
}

// Size 估算页面大小（字节）
func (p *LeafPage) Size() int {
	size := 8 + 8 + 4 // pageID + version + numKeys

	for i := 0; i < len(p.keys); i++ {
		size += 2 + len(p.keys[i])   // keyLen + key
		size += 2 + len(p.values[i]) // valueLen + value
	}

	return size
}

// IsFull 判断页面是否已满
func (p *LeafPage) IsFull(maxKeys int) bool {
	return len(p.keys) >= maxKeys
}

// materialize 物化增量链（合并到独立数据）
// 内部使用锁确保原子性
func (p *LeafPage) materialize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.materializeUnsafe()
}

// materializeUnsafe 物化增量链（合并到独立数据）
// ⚠️ 调用此方法时必须持有 p.mu 锁！
func (p *LeafPage) materializeUnsafe() {
	// 双重检查：先读取 cowDelta 到局部变量
	cow := p.cowDelta
	if cow == nil {
		return
	}

	// 再次确认，确保在应用 deltas 前 cowDelta 没有被修改
	if p.cowDelta == nil {
		return
	}

	// 获取基础数据的完整副本
	newKeys := make([][]byte, len(p.cowDelta.sharedKeys))
	copy(newKeys, p.cowDelta.sharedKeys)

	newValues := make([][]byte, len(p.cowDelta.sharedValues))
	copy(newValues, p.cowDelta.sharedValues)

	// 应用所有增量操作
	deltas := p.cowDelta.GetDeltas()

	for _, delta := range deltas {
		switch delta.op {
		case DeltaInsert:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				// 更新
				newValues[idx] = delta.value
			} else {
				// 插入
				newKeys = insertSlice(newKeys, idx, delta.key)
				newValues = insertSlice(newValues, idx, delta.value)
			}
		case DeltaUpdate:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				newValues[idx] = delta.value
			}
		case DeltaDelete:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				newKeys = append(newKeys[:idx], newKeys[idx+1:]...)
				newValues = append(newValues[:idx], newValues[idx+1:]...)
			}
		}
	}

	// 替换为独立数据
	p.keys = newKeys
	p.values = newValues
	p.version++

	// 释放当前页面对 COWDeltaRef 的引用
	// 注意：如果其他页面共享此 COWDeltaRef（refCount > 1），
	//       sharedKeys/sharedValues 不会被立即回收
	//       只有当最后一个引用 Release() 时，底层数据才会被 GC 回收
	if p.cowDelta != nil {
		p.cowDelta.Release()
		p.cowDelta = nil
	}
}

// IsShared 检查是否共享数据
func (p *LeafPage) IsShared() bool {
	return p.cowDelta != nil && p.cowDelta.GetRefCount() > 1
}

// GetDeltaCount 获取增量链长度
func (p *LeafPage) GetDeltaCount() int {
	if p.cowDelta == nil {
		return 0
	}
	return p.cowDelta.GetDeltaCount()
}

// binarySearch 辅助函数（在切片中搜索）
func binarySearch(slice [][]byte, key []byte) (int, bool) {
	left, right := 0, len(slice)-1

	for left <= right {
		mid := left + (right-left)/2
		cmp := bytes.Compare(slice[mid], key)

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
