// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/stretchr/testify/suite"
)

// E2ETestSuite E2E 测试基础套件
// 所有 E2E 测试都应该嵌入此结构体
type E2ETestSuite struct {
	suite.Suite

	// 基础组件
	PortAllocator  *TestPortAllocator
	DataDirManager *DataDirManager
	ProcessManager *ProcessManager
	Logger         *log.Logger
}

// NewE2ETestSuite 创建 E2E 测试套件
func NewE2ETestSuite() *E2ETestSuite {
	logger := log.New(os.Stderr, "[E2E] ", log.LstdFlags|log.Lshortfile)
	return &E2ETestSuite{
		PortAllocator:  NewTestPortAllocator(),
		DataDirManager: NewDataDirManager(""),
		ProcessManager: NewProcessManager(logger),
		Logger:         logger,
	}
}

// SetupSuite 在所有测试前执行一次
func (s *E2ETestSuite) SetupSuite() {
	s.Logger = log.New(os.Stderr, "[E2E] ", log.LstdFlags|log.Lshortfile)
	s.Logger.Println("Setting up E2E test suite")

	s.PortAllocator = NewTestPortAllocator()
	s.DataDirManager = NewDataDirManager("")
	s.ProcessManager = NewProcessManager(s.Logger)
}

// TearDownSuite 在所有测试后执行一次
func (s *E2ETestSuite) TearDownSuite() {
	s.Logger.Println("Tearing down E2E test suite")

	// 停止所有进程
	if s.ProcessManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.ProcessManager.StopAll(ctx); err != nil {
			s.Logger.Printf("Failed to stop all processes: %v", err)
		}
	}

	// 释放所有端口
	if s.PortAllocator != nil {
		if err := s.PortAllocator.ReleaseAll(); err != nil {
			s.Logger.Printf("Failed to release all ports: %v", err)
		}
	}

	// 清理所有数据目录
	if s.DataDirManager != nil {
		if err := s.DataDirManager.CleanupAll(); err != nil {
			s.Logger.Printf("Failed to cleanup data dirs: %v", err)
		}
	}
}

// BeforeTest 在每个测试前执行
func (s *E2ETestSuite) BeforeTest(suiteName, testName string) {
	s.Logger.Printf("Starting test: %s/%s", suiteName, testName)
}

// AfterTest 在每个测试后执行
func (s *E2ETestSuite) AfterTest(suiteName, testName string) {
	s.Logger.Printf("Finished test: %s/%s", suiteName, testName)
}
