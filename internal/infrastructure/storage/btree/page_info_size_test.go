package btree

import (
	"fmt"
	"testing"
	"unsafe"
)

func TestPageInfoSize(t *testing.T) {
	var info PageInfo
	size := unsafe.Sizeof(info)
	fmt.Printf("PageInfo size: %d bytes\n", size)
	fmt.Printf("PageInfo alignment: %d bytes\n", unsafe.Alignof(info))

	// 打印字段偏移（Off-Heap 模式）
	fmt.Printf("pos offset: %d\n", unsafe.Offsetof(info.pos))
	fmt.Printf("nodeRef offset: %d\n", unsafe.Offsetof(info.nodeRef))
	fmt.Printf("pageLock offset: %d\n", unsafe.Offsetof(info.pageLock))
	fmt.Printf("parentRef offset: %d\n", unsafe.Offsetof(info.parentRef))
	fmt.Printf("flags offset: %d\n", unsafe.Offsetof(info.flags))
}
