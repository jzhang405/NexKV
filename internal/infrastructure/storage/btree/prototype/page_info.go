package prototype

// PageInfo 简化的页面信息（无缓存逻辑，仅用于原型测试）
type PageInfo struct {
	page  *Page // 页面对象
	pos   int64 // 在 Chunk 中的位置（0=未写入）
	state int32 // 状态：0=clean, 1=dirty
}

// NewPageInfo 创建新的 PageInfo
func NewPageInfo(page *Page) *PageInfo {
	return &PageInfo{
		page:  page,
		pos:   0,
		state: 0,
	}
}

// GetPage 获取页面对象
func (pi *PageInfo) GetPage() *Page {
	return pi.page
}

// IsDirty 判断是否为脏页
func (pi *PageInfo) IsDirty() bool {
	return pi.state == 1
}

// MarkDirty 标记为脏页
func (pi *PageInfo) MarkDirty() {
	pi.state = 1
}

// MarkClean 标记为干净页
func (pi *PageInfo) MarkClean() {
	pi.state = 0
}

// Clone 克隆 PageInfo（Copy-on-Write）
func (pi *PageInfo) Clone() *PageInfo {
	return &PageInfo{
		page:  pi.page.Clone(),
		pos:   pi.pos,
		state: pi.state,
	}
}
