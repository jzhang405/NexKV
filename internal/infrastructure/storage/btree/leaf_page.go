package btree

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/jzhang405/NexKV/internal/domain/model"
)

// 类型转换辅助函数
func uint64ToBytes(v uint64) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, v)
	return buf
}

func bytesToUint64(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b)
}

func uint32ToBytes(v uint32) []byte {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, v)
	return buf
}

func bytesToUint32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}

func uint16ToBytes(v uint16) []byte {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, v)
	return buf
}

func bytesToUint16(b []byte) uint16 {
	return binary.LittleEndian.Uint16(b)
}

// LeafPage 叶子节点
// 存储键值对，是 BTree 的最底层节点
type LeafPage struct {
	pageID  model.PageID // 页面 ID
	version uint64       // 版本号（用于 CCOW）
	keys    [][]byte     // 键数组（有序）
	values  [][]byte     // 值数组（与 keys 一一对应）
}

// NewLeafPage 创建新的叶子页面
func NewLeafPage(pageID model.PageID) *LeafPage {
	return &LeafPage{
		pageID:  pageID,
		version: 0,
		keys:    make([][]byte, 0, 16), // 预分配容量
		values:  make([][]byte, 0, 16),
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
	// 二分查找
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

// Insert 插入键值对
// 返回：是否插入成功（false 表示键已存在）
func (p *LeafPage) Insert(key, value []byte) (bool, error) {
	idx, found := p.search(key)

	if found {
		// 键已存在，更新值
		p.values[idx] = value
		return false, nil
	}

	// 插入新键值对
	p.keys = insertSlice(p.keys, idx, key)
	p.values = insertSlice(p.values, idx, value)
	p.version++
	return true, nil
}

// insertSlice 在切片指定位置插入元素
func insertSlice[T any](slice []T, idx int, value T) []T {
	if len(slice) == cap(slice) {
		// 创建新切片时预留空间给新元素
		// 计算新容量：如果 cap 为 0，使用默认容量 16；否则翻倍
		newCap := cap(slice) * 2
		if newCap == 0 {
			newCap = 16 // 默认初始容量
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

// Delete 删除键值对
// 返回：是否删除成功
func (p *LeafPage) Delete(key []byte) (bool, error) {
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

	// 创建新页面，包含分裂键及之后的键值对（包含分裂键）
	// ✅ Day 10-11: 修正分裂逻辑，右子节点包含分裂键
	newPage := NewLeafPage(model.PageID(p.pageID + 1))   // 临时 ID
	newPage.keys = append(newPage.keys, p.keys[mid:]...) // 包含分裂键
	newPage.values = append(newPage.values, p.values[mid:]...)

	// 当前页面保留分裂键之前的键值对（不包含分裂键）
	p.keys = p.keys[:mid]
	p.values = p.values[:mid]
	p.version++

	return newPage, splitKey, nil
}

// Clone 克隆页面（Copy-on-Write）
func (p *LeafPage) Clone() *LeafPage {
	newKeys := make([][]byte, len(p.keys))
	copy(newKeys, p.keys)

	newValues := make([][]byte, len(p.values))
	copy(newValues, p.values)

	return &LeafPage{
		pageID:  p.pageID,
		version: p.version,
		keys:    newKeys,
		values:  newValues,
	}
}

// Serialize 序列化页面
// 返回：序列化后的字节数组
func (p *LeafPage) Serialize() ([]byte, error) {
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

	// 写入键值对
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

		// 写入值长度 (2 bytes)
		valueLen := uint16(len(p.values[i]))
		if err := binaryWrite(&buf, uint16ToBytes(valueLen)); err != nil {
			return nil, err
		}

		// 写入值数据
		if err := binaryWrite(&buf, p.values[i]); err != nil {
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

// binaryWrite 辅助函数：写入字节数组到 buffer
func binaryWrite(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

// readBytes 辅助函数：从 reader 读取指定长度的字节
func readBytes(r io.Reader, length int) ([]byte, error) {
	data := make([]byte, length)
	_, err := io.ReadFull(r, data)
	return data, err
}
