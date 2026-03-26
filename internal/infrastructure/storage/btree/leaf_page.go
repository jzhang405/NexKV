package btree

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// LeafPage 叶子节点
// 存储键值对，是 BTree 的最底层节点
// 支持 COW+Delta 混合方案优化 Clone 性能
type LeafPage struct {
	pageID   model.PageID // 页面 ID
	version  uint64       // 版本号（用于 CCOW）
	keys     [][]byte     // 键数组（有序）
	values   [][]byte     // 值数组（与 keys 一一对应）
	pageLock *PageLock    // 页面锁（用于避免重复深拷贝）
	cowDelta *COWDeltaRef // COW+Delta 引用（nil = 已物化/独立数据）
}

// NewLeafPage 创建新的叶子页面
func NewLeafPage(pageID model.PageID) *LeafPage {
	return &LeafPage{
		pageID:   pageID,
		version:  0,
		keys:     make([][]byte, 0, InitialLeafCapacity), // 预分配容量
		values:   make([][]byte, 0, InitialLeafCapacity),
		pageLock: nil, // 性能优化：延迟创建页面锁（通过 PageInfo.GetLock 访问）
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

// Get 获取键对应的值（支持增量链）
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

// Insert 插入键值对（支持增量模式）
// 返回：是否插入成功（false 表示键已存在）
func (p *LeafPage) Insert(key, value []byte) (bool, error) {
	// 如果有 COW 引用，使用增量模式
	if p.cowDelta != nil {
		// 检查是否需要物化
		if p.cowDelta.ShouldMaterialize(len(p.keys), p.cowDelta.GetRefCount()) {
			p.materialize()
		} else {
			// 添加增量
			_, found := p.search(key)
			if found {
				// 键已存在，添加 Update 增量
				p.cowDelta.AppendDelta(Delta{
					op:    DeltaUpdate,
					key:   key,
					value: value,
				})
				p.version++
				return false, nil
			}
			// 新键，添加 Insert 增量
			p.cowDelta.AppendDelta(Delta{
				op:    DeltaInsert,
				key:   key,
				value: value,
			})
			p.version++
			return true, nil
		}
	}

	// 物化状态：直接修改
	return p.insertDirect(key, value)
}

// insertDirect 直接插入（物化状态）
func (p *LeafPage) insertDirect(key, value []byte) (bool, error) {
	// 调试：检查前置条件
	if len(p.keys) != len(p.values) {
		panic(fmt.Sprintf("Insert: precondition violated - pageID=%d, keys=%d, values=%d",
			p.pageID, len(p.keys), len(p.values)))
	}

	idx, found := p.search(key)

	if found {
		// 键已存在，更新值
		p.values[idx] = value
		p.version++
		return false, nil
	}

	// 插入新键值对
	oldKeysLen := len(p.keys)
	oldValuesLen := len(p.values)
	p.keys = insertSlice(p.keys, idx, key)
	p.values = insertSlice(p.values, idx, value)

	// 调试：检查后置条件
	if len(p.keys) != len(p.values) {
		panic(fmt.Sprintf("Insert: postcondition violated - pageID=%d, oldKeys=%d->%d, oldValues=%d->%d",
			p.pageID, oldKeysLen, len(p.keys), oldValuesLen, len(p.values)))
	}

	p.version++
	return true, nil
}

// materialize 物化增量链（合并到独立数据）
func (p *LeafPage) materialize() {
	if p.cowDelta == nil {
		return
	}

	// 获取基础数据的完整副本
	newKeys := make([][]byte, len(p.cowDelta.GetSharedKeys()))
	copy(newKeys, p.cowDelta.GetSharedKeys())

	newValues := make([][]byte, len(p.cowDelta.GetSharedValues()))
	copy(newValues, p.cowDelta.GetSharedValues())

	// 应用所有增量操作
	deltas := p.cowDelta.GetDeltas()
	for _, delta := range deltas {
		switch delta.op {
		case DeltaInsert:
			idx, found := binarySearch(newKeys, delta.key)
			if found {
				// 已存在，更新
				newValues[idx] = delta.value
			} else {
				// 插入新键
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

	// 释放引用
	p.cowDelta.Release()
	p.cowDelta = nil
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

// insertSlice 在切片指定位置插入元素
func insertSlice[T any](slice []T, idx int, value T) []T {
	// 修复：检查索引范围
	if idx < 0 || idx > len(slice) {
		panic(fmt.Sprintf("insertSlice: index %d out of bounds [0, %d]", idx, len(slice)))
	}

	// 修复：无论容量是否足够，都使用创建新切片的方式
	// 避免在 len(slice) == cap(slice) 时的边界情况
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

	// 修复：即使容量足够，也使用 append 方式避免边界问题
	// 这比直接操作 slice 更安全
	if idx == len(slice) {
		// 在末尾追加
		return append(slice, value)
	}

	// 在中间插入
	slice = append(slice, value)     // 扩展切片
	copy(slice[idx+1:], slice[idx:]) // 移动元素
	slice[idx] = value               // 设置新值
	return slice
}

// Delete 删除键值对（支持增量模式）
// 返回：是否删除成功
func (p *LeafPage) Delete(key []byte) (bool, error) {
	// 如果有 COW 引用，使用增量模式
	if p.cowDelta != nil {
		// 先检查键是否存在（包括增量链）
		if !p.keyExistsInDeltas(key) {
			// 键不存在，直接返回
			return false, nil
		}

		// 检查是否需要物化
		if p.cowDelta.ShouldMaterialize(len(p.keys), p.cowDelta.GetRefCount()) {
			p.materialize()
			return p.deleteDirect(key)
		}

		// 添加 Delete 增量
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

// keyExistsInDeltas 检查键是否存在（包括增量链）
func (p *LeafPage) keyExistsInDeltas(key []byte) bool {
	// 先检查增量链（反向遍历，最新的优先）
	if p.cowDelta != nil {
		deltas := p.cowDelta.GetDeltas()
		for i := len(deltas) - 1; i >= 0; i-- {
			delta := deltas[i]
			if bytes.Equal(delta.key, key) {
				switch delta.op {
				case DeltaInsert, DeltaUpdate:
					return true // 键存在
				case DeltaDelete:
					return false // 键已被删除
				}
			}
		}
	}

	// 检查基础数据
	_, found := p.search(key)
	return found
}

// deleteDirect 直接删除（物化状态）
func (p *LeafPage) deleteDirect(key []byte) (bool, error) {
	idx, found := p.search(key)
	if !found {
		return false, nil
	}

	// 删除键值对
	p.keys = append(p.keys[:idx], p.keys[idx+1:]...)
	p.values = append(p.values[:idx], p.values[idx+1:]...)
	p.version++
	return true, nil
}

// Update 更新键的值
// 如果键不存在，会插入新键值对
// 返回：错误信息
func (p *LeafPage) Update(key, value []byte) error {
	// 复用 Insert 的增量逻辑
	_, err := p.Insert(key, value)
	return err
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

	// 创建新页面，包含分裂键及之后的键值对（包含分裂键）
	// 修复：使用 make + copy 创建独立的 slice，避免共享底层数组
	// 这防止并发修改时 p.keys 和 newPage.keys 的底层数组被重新分配导致长度不一致
	newPage := NewLeafPage(model.PageID(p.pageID + 1)) // 临时 ID
	newPage.keys = make([][]byte, len(p.keys[mid:]))
	copy(newPage.keys, p.keys[mid:])
	newPage.values = make([][]byte, len(p.values[mid:]))
	copy(newPage.values, p.values[mid:])

	// 当前页面保留分裂键之前的键值对（不包含分裂键）
	p.keys = p.keys[:mid]
	p.values = p.values[:mid]
	p.version++

	return newPage, splitKey, nil
}

// Clone 克隆页面（使用 Delta Chain 模式）
// 性能：~290 ns/op（零拷贝，共享数据）
// 与 CloneDeep() 的区别：
// - Clone(): Delta Chain 模式，共享 keys/values，使用增量链记录修改
// - CloneDeep(): 深拷贝模式，返回完全独立的副本（~1423 ns/op）
//
// config: 可选的 COW 配置，如果不提供则使用默认配置
func (p *LeafPage) Clone(config ...*COWDeltaRefConfig) *LeafPage {
	// 如果已有 COW 引用，增加引用计数并返回新克隆
	if p.cowDelta != nil {
		p.cowDelta.Retain()
		return &LeafPage{
			pageID:   p.pageID,
			version:  p.version + 1,
			cowDelta: p.cowDelta,
			keys:     p.cowDelta.GetSharedKeys(),
			values:   p.cowDelta.GetSharedValues(),
			pageLock: nil, // 性能优化：延迟创建页面锁
		}
	}

	// 创建新的 COW 引用（共享 keys/values）
	// 注意：克隆页面有自己的 COWDeltaRef，但共享相同的 keys/values
	var cowRef *COWDeltaRef
	if len(config) > 0 && config[0] != nil {
		cowRef = NewCOWDeltaRefWithConfig(p.keys, p.values, config[0])
	} else {
		cowRef = NewCOWDeltaRef(p.keys, p.values)
	}
	// refCount = 1（只有克隆页面持有这个 COWDeltaRef）
	// 原始页面保持独立，不进入 Delta 模式

	return &LeafPage{
		pageID:   p.pageID,
		version:  p.version + 1,
		cowDelta: cowRef,
		keys:     cowRef.GetSharedKeys(),
		values:   cowRef.GetSharedValues(),
		pageLock: nil, // 性能优化：延迟创建页面锁
	}
}

// CloneDeep 深拷贝页面（独立数据）
// 性能：~1423 ns/op（完全复制）
// 使用场景：需要完全独立的副本时使用此方法
func (p *LeafPage) CloneDeep() *LeafPage {
	newKeys := make([][]byte, len(p.keys))
	copy(newKeys, p.keys)

	newValues := make([][]byte, len(p.values))
	copy(newValues, p.values)

	return &LeafPage{
		pageID:   p.pageID,
		version:  p.version + 1,
		keys:     newKeys,
		values:   newValues,
		pageLock: nil, // 性能优化：延迟创建页面锁
		// cowDelta 为 nil，使用独立数据
	}
}

// CloneWithDelta 创建 Delta Chain 模式克隆（零拷贝）
// 注意：此方法保留用于向后兼容，现在直接调用 Clone()
// 建议新代码直接使用 Clone() 或 CloneDeep()
func (p *LeafPage) CloneWithDelta() *LeafPage {
	return p.Clone()
}

// Serialize 序列化页面
// 返回：序列化后的字节数组
func (p *LeafPage) Serialize() ([]byte, error) {
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
func (p *LeafPage) serializeWithData() ([]byte, error) {

	// 调试：检查 keys 和 values 长度是否一致
	if len(p.keys) != len(p.values) {
		panic(fmt.Sprintf("Serialize: inconsistent page state - len(keys)=%d, len(values)=%d, pageID=%d",
			len(p.keys), len(p.values), p.pageID))
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
	for i := range len(p.keys) {
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
		pageID:  pageID,
		version: version,
		keys:    make([][]byte, 0, numKeys),
		values:  make([][]byte, 0, numKeys),
	}

	// 7. 读取键值对
	for range int(numKeys) {
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
	for i := range len(p.keys) {
		if err := fn(p.keys[i], p.values[i]); err != nil {
			return err
		}
	}
	return nil
}

// Size 估算页面大小（字节）
func (p *LeafPage) Size() int {
	size := 8 + 8 + 4 // pageID + version + numKeys

	for i := range len(p.keys) {
		size += 2 + len(p.keys[i])   // keyLen + key
		size += 2 + len(p.values[i]) // valueLen + value
	}

	return size
}

// IsFull 判断页面是否已满
func (p *LeafPage) IsFull(maxKeys int) bool {
	return len(p.keys) >= maxKeys
}

// IsInDeltaMode 检查是否在 Delta Chain 模式
func (p *LeafPage) IsInDeltaMode() bool {
	return p.cowDelta != nil
}

// GetDeltaCount 获取增量链长度
func (p *LeafPage) GetDeltaCount() int {
	if p.cowDelta == nil {
		return 0
	}
	return p.cowDelta.GetDeltaCount()
}

// IsShared 检查是否共享数据（引用计数 > 1）
func (p *LeafPage) IsShared() bool {
	if p.cowDelta == nil {
		return false
	}
	return p.cowDelta.GetRefCount() > 1
}

// GetRefCount 获取当前引用计数
func (p *LeafPage) GetRefCount() int32 {
	if p.cowDelta == nil {
		return 1 // 未使用 Delta Chain，引用计数为 1（自己）
	}
	return p.cowDelta.GetRefCount()
}
