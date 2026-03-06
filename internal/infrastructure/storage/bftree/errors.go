package bftree

import (
	"fmt"

	"errors"
)

// Bf-Tree 错误定义
var (
	// ErrKeyNotFound 键不存在
	ErrKeyNotFound = errors.New("key not found")

	// ErrKeyAlreadyExists 键已存在
	ErrKeyAlreadyExists = errors.New("key already exists")

	// ErrPageFull 页面已满
	ErrPageFull = errors.New("page full")

	// ErrPageNotFound 页面不存在

	// ErrPageStillReferenced 页面仍被引用
	ErrPageStillReferenced = errors.New("page still referenced")
	ErrPageNotFound        = errors.New("page not found")

	// ErrInvalidPageType 无效的页面类型
	ErrInvalidPageType = errors.New("invalid page type")

	// ErrDeltaChainFull Delta Chain 已满
	ErrDeltaChainFull = errors.New("delta chain full")

	// ErrInvalidConfig 无效的配置
	ErrInvalidConfig = errors.New("invalid config")

	// ErrTreeClosed Bf-Tree 已关闭
	ErrTreeClosed = errors.New("tree closed")
)

// PageCorruptError 页面损坏错误
type PageCorruptError struct {
	PageID uint64
	Reason string
}

func (e *PageCorruptError) Error() string {
	return fmt.Sprintf("page %d corrupted: %s", e.PageID, e.Reason)
}

// Is 判断错误是否为 PageCorruptError
func (e *PageCorruptError) Is(target error) bool {
	_, ok := target.(*PageCorruptError)
	return ok
}

// NewPageCorruptError 创建页面损坏错误
func NewPageCorruptError(pageID uint64, reason string) error {
	return &PageCorruptError{
		PageID: pageID,
		Reason: reason,
	}
}

// InvalidPageLevelError 无效的页面级别错误
type InvalidPageLevelError struct {
	Current PageLevel
	Target  PageLevel
}

func (e *InvalidPageLevelError) Error() string {
	return fmt.Sprintf("invalid page level: current=%s, target=%s", e.Current, e.Target)
}

// Is 判断错误是否为 InvalidPageLevelError
func (e *InvalidPageLevelError) Is(target error) bool {
	_, ok := target.(*InvalidPageLevelError)
	return ok
}

// NewInvalidPageLevelError 创建无效页面级别错误
func NewInvalidPageLevelError(current, target PageLevel) error {
	return &InvalidPageLevelError{
		Current: current,
		Target:  target,
	}
}

// ChecksumError 校验和错误
type ChecksumError struct {
	Expected uint32
	Actual   uint32
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("checksum mismatch: expected=%d, actual=%d", e.Expected, e.Actual)
}

// Is 判断错误是否为 ChecksumError
func (e *ChecksumError) Is(target error) bool {
	_, ok := target.(*ChecksumError)
	return ok
}

// NewChecksumError 创建校验和错误
func NewChecksumError(expected, actual uint32) error {
	return &ChecksumError{
		Expected: expected,
		Actual:   actual,
	}
}
