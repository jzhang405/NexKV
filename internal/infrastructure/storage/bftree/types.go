package bftree

// PageLevel Mini-Page 级别
type PageLevel int

const (
	// L1 64 字节 Mini-Page
	// 可存储约 1 个键值对
	L1 PageLevel = iota
	// L2 128 字节 Mini-Page
	// 可存储约 2 个键值对
	L2
	// L3 256 字节 Mini-Page
	// 可存储约 4 个键值对
	L3
	// L4 512 字节 Mini-Page
	// 可存储约 8 个键值对
	L4
	// L5 1KB Mini-Page
	// 可存储约 16 个键值对
	L5
	// L6 2KB Mini-Page
	// 可存储约 32 个键值对
	L6
	// Full 4KB 完整页面
	// 可存储约 64 个键值对
	Full
)

// String 返回 PageLevel 的字符串表示
func (l PageLevel) String() string {
	switch l {
	case L1:
		return "L1(64B)"
	case L2:
		return "L2(128B)"
	case L3:
		return "L3(256B)"
	case L4:
		return "L4(512B)"
	case L5:
		return "L5(1KB)"
	case L6:
		return "L6(2KB)"
	case Full:
		return "Full(4KB)"
	default:
		return "Unknown"
	}
}

// PageSize 返回对应级别的大小（字节）
func (l PageLevel) PageSize() int {
	switch l {
	case L1:
		return 64
	case L2:
		return 128
	case L3:
		return 256
	case L4:
		return 512
	case L5:
		return 1024
	case L6:
		return 2048
	case Full:
		return 4096
	default:
		return 64
	}
}

// Valid 检查 PageLevel 是否有效
func (l PageLevel) Valid() bool {
	return l >= L1 && l <= Full
}

// NextLevel 返回下一个级别
func (l PageLevel) NextLevel() PageLevel {
	if l < Full {
		return l + 1
	}
	return Full
}

// PageType 页面类型
type PageType int

const (
	// PageTypeLeaf 叶子节点页面
	PageTypeLeaf PageType = iota
	// PageTypeInner 内部节点页面
	PageTypeInner
)

// String 返回 PageType 的字符串表示
func (t PageType) String() string {
	switch t {
	case PageTypeLeaf:
		return "Leaf"
	case PageTypeInner:
		return "Inner"
	default:
		return "Unknown"
	}
}
