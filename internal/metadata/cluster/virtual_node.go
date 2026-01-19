// Package cluster 提供集群管理功能
//
// 本包实现：
//   - 虚拟节点抽象：支持单机-分布式一体部署
//   - 节点 ID 管理：结构化节点标识符
//   - 树形拓扑协调：支持大规模集群（100-1000 节点）
//   - 故障检测：基于 φ 故障检测器
//   - 自愈机制：自动故障恢复
//
// 虚拟节点设计：
//   - 单机模式：1 物理节点 = N 虚拟节点（独立数据目录隔离）
//   - 分布式模式：M 物理节点 × N 虚拟节点
//   - 平滑切换：支持在线扩缩容，后台数据迁移
package cluster

import (
	"github.com/jzhang405/NexKV/internal/metadata/types"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
)

// VirtualNode 虚拟节点接口
//
// 虚拟节点是集群中的逻辑节点，可以是：
//   - 单机模式：同一物理节点上的多个隔离实例
//   - 分布式模式：不同物理节点上的实例
type VirtualNode interface {
	// GetID 获取虚拟节点 ID
	GetID() string

	// GetPhysicalNodeID 获取物理节点 ID
	GetPhysicalNodeID() string

	// GetDataDir 获取虚拟节点数据目录（独立隔离）
	GetDataDir() string

	// GetWalDir 获取 WAL 目录
	GetWalDir() string

	// GetSnapshotDir 获取快照目录
	GetSnapshotDir() string

	// GetSSTDir 获取 SSTable 目录
	GetSSTDir() string

	// Start 启动虚拟节点
	Start() error

	// Stop 停止虚拟节点
	Stop() error

	// IsRunning 检查节点是否运行中
	IsRunning() bool
}

// VirtualNodeConfig 虚拟节点配置
type VirtualNodeConfig struct {
	// VirtualNodeID 虚拟节点 ID（格式：[环境]_[主机]_[服务]_[端口]）
	VirtualNodeID string

	// PhysicalNodeID 物理节点 ID
	PhysicalNodeID string

	// RootDataDir 根数据目录（物理节点级别）
	// 虚拟节点数据目录：{RootDataDir}/{VirtualNodeID}/
	RootDataDir string

	// EnableDataIsolation 是否启用数据隔离（默认 true）
	EnableDataIsolation bool
}

// VirtualNodeImpl 虚拟节点实现
type VirtualNodeImpl struct {
	config *VirtualNodeConfig

	// 状态
	running atomic.Bool

	// 目录路径（独立隔离）
	dataDir     string
	walDir      string
	snapshotDir string
	sstDir      string
}

// NewVirtualNode 创建虚拟节点
func NewVirtualNode(config *VirtualNodeConfig) (VirtualNode, error) {
	if config == nil {
		return nil, types.NewClusterNilParameterError("config")
	}

	if config.VirtualNodeID == "" {
		return nil, types.NewClusterNilParameterError("virtualNodeID")
	}

	if config.PhysicalNodeID == "" {
		return nil, types.NewClusterNilParameterError("physicalNodeID")
	}

	if config.RootDataDir == "" {
		return nil, types.NewClusterNilParameterError("rootDataDir")
	}

	// 默认启用数据隔离
	if !config.EnableDataIsolation {
		config.EnableDataIsolation = true
	}

	vn := &VirtualNodeImpl{
		config: config,
	}

	// 初始化目录路径
	if err := vn.initPaths(); err != nil {
		return nil, types.NewClusterNodeManagementError("初始化路径", "", err)
	}

	logging.WithFields(map[string]any{
		"virtual_node_id":  config.VirtualNodeID,
		"physical_node_id": config.PhysicalNodeID,
		"data_dir":         vn.dataDir,
		"data_isolation":   config.EnableDataIsolation,
	}).Info("创建虚拟节点")

	return vn, nil
}

// initPaths 初始化目录路径
//
// 方案 A：独立数据目录
//
//	每个 VN 有独立的数据目录：{RootDataDir}/{VirtualNodeID}/
//	- WAL:    {RootDataDir}/{VirtualNodeID}/wal
//	- 快照:   {RootDataDir}/{VirtualNodeID}/snapshots
//	- SST:    {RootDataDir}/{VirtualNodeID}/sst
func (vn *VirtualNodeImpl) initPaths() error {
	vn.dataDir = filepath.Join(vn.config.RootDataDir, vn.config.VirtualNodeID)
	vn.walDir = filepath.Join(vn.dataDir, "wal")
	vn.snapshotDir = filepath.Join(vn.dataDir, "snapshots")
	vn.sstDir = filepath.Join(vn.dataDir, "sst")

	// 创建所有目录
	dirs := []string{
		vn.dataDir,
		vn.walDir,
		vn.snapshotDir,
		vn.sstDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return types.NewClusterNodeManagementError("创建目录", dir, err)
		}
	}

	return nil
}

// GetID 获取虚拟节点 ID
func (vn *VirtualNodeImpl) GetID() string {
	return vn.config.VirtualNodeID
}

// GetPhysicalNodeID 获取物理节点 ID
func (vn *VirtualNodeImpl) GetPhysicalNodeID() string {
	return vn.config.PhysicalNodeID
}

// GetDataDir 获取虚拟节点数据目录
func (vn *VirtualNodeImpl) GetDataDir() string {
	return vn.dataDir
}

// GetWalDir 获取 WAL 目录
func (vn *VirtualNodeImpl) GetWalDir() string {
	return vn.walDir
}

// GetSnapshotDir 获取快照目录
func (vn *VirtualNodeImpl) GetSnapshotDir() string {
	return vn.snapshotDir
}

// GetSSTDir 获取 SSTable 目录
func (vn *VirtualNodeImpl) GetSSTDir() string {
	return vn.sstDir
}

// Start 启动虚拟节点
func (vn *VirtualNodeImpl) Start() error {
	if !vn.running.CompareAndSwap(false, true) {
		return types.NewClusterServiceStateError("虚拟节点", "已经在运行")
	}

	logging.WithField("virtual_node_id", vn.config.VirtualNodeID).Info("启动虚拟节点")
	return nil
}

// Stop 停止虚拟节点
func (vn *VirtualNodeImpl) Stop() error {
	if !vn.running.CompareAndSwap(true, false) {
		return nil // 已经停止
	}

	logging.WithField("virtual_node_id", vn.config.VirtualNodeID).Info("停止虚拟节点")
	return nil
}

// IsRunning 检查节点是否运行中
func (vn *VirtualNodeImpl) IsRunning() bool {
	return vn.running.Load()
}

// ========================================
// 虚拟节点管理器
// ========================================

// VirtualNodeManager 虚拟节点管理器
//
// 负责管理物理节点上的所有虚拟节点
type VirtualNodeManager struct {
	mu             sync.RWMutex
	physicalNodeID string
	rootDataDir    string
	virtualNodes   map[string]VirtualNode // virtualNodeID -> VirtualNode
}

// NewVirtualNodeManager 创建虚拟节点管理器
func NewVirtualNodeManager(physicalNodeID, rootDataDir string) *VirtualNodeManager {
	return &VirtualNodeManager{
		physicalNodeID: physicalNodeID,
		rootDataDir:    rootDataDir,
		virtualNodes:   make(map[string]VirtualNode),
	}
}

// CreateVirtualNode 创建新的虚拟节点
func (vm *VirtualNodeManager) CreateVirtualNode(virtualNodeID string) (VirtualNode, error) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// 检查是否已存在
	if _, exists := vm.virtualNodes[virtualNodeID]; exists {
		return nil, types.NewClusterTreeManagementError("虚拟节点已存在: " + virtualNodeID)
	}

	config := &VirtualNodeConfig{
		VirtualNodeID:       virtualNodeID,
		PhysicalNodeID:      vm.physicalNodeID,
		RootDataDir:         vm.rootDataDir,
		EnableDataIsolation: true,
	}

	vn, err := NewVirtualNode(config)
	if err != nil {
		return nil, err
	}

	vm.virtualNodes[virtualNodeID] = vn

	logging.WithFields(map[string]any{
		"virtual_node_id":  virtualNodeID,
		"physical_node_id": vm.physicalNodeID,
		"total_vns":        len(vm.virtualNodes),
	}).Info("创建虚拟节点")

	return vn, nil
}

// GetVirtualNode 获取虚拟节点
func (vm *VirtualNodeManager) GetVirtualNode(virtualNodeID string) (VirtualNode, error) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	vn, exists := vm.virtualNodes[virtualNodeID]
	if !exists {
		return nil, types.NewClusterNodeNotFoundError(virtualNodeID)
	}

	return vn, nil
}

// RemoveVirtualNode 移除虚拟节点
func (vm *VirtualNodeManager) RemoveVirtualNode(virtualNodeID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vn, exists := vm.virtualNodes[virtualNodeID]
	if !exists {
		return types.NewClusterNodeNotFoundError(virtualNodeID)
	}

	// 停止节点
	if err := vn.Stop(); err != nil {
		return types.NewClusterNodeManagementError("停止虚拟节点", "", err)
	}

	delete(vm.virtualNodes, virtualNodeID)

	logging.WithFields(map[string]any{
		"virtual_node_id":  virtualNodeID,
		"physical_node_id": vm.physicalNodeID,
		"remaining_vns":    len(vm.virtualNodes),
	}).Info("移除虚拟节点")

	return nil
}

// ListVirtualNodes 列出所有虚拟节点
func (vm *VirtualNodeManager) ListVirtualNodes() []VirtualNode {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	result := make([]VirtualNode, 0, len(vm.virtualNodes))
	for _, vn := range vm.virtualNodes {
		result = append(result, vn)
	}

	return result
}

// GetVirtualNodeCount 获取虚拟节点数量
func (vm *VirtualNodeManager) GetVirtualNodeCount() int {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return len(vm.virtualNodes)
}
