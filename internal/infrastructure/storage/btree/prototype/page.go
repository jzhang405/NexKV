// Package prototype 提供 Phase 0.5 原型验证代码
// 目标：验证 atomic.Pointer 性能是否满足 <1μs 读延迟目标
package prototype

// Page 简化的页面结构（仅用于原型测试）
type Page struct {
	ID      int
	Keys    [][]byte
	Values  [][]byte
	Version uint64
}

// NewPage 创建新页面
func NewPage(id int) *Page {
	return &Page{
		ID:      id,
		Keys:    make([][]byte, 0),
		Values:  make([][]byte, 0),
		Version: 1,
	}
}

// Clone 克隆页面（Copy-on-Write）
func (p *Page) Clone() *Page {
	newPage := &Page{
		ID:      p.ID,
		Keys:    make([][]byte, len(p.Keys)),
		Values:  make([][]byte, len(p.Values)),
		Version: p.Version + 1,
	}
	copy(newPage.Keys, p.Keys)
	copy(newPage.Values, p.Values)
	return newPage
}
