// Package e2e 提供 E2E 测试基础设施
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

// ExampleSuite 示例测试套件
type ExampleSuite struct {
	E2ETestSuite
}

func TestExampleSuite(t *testing.T) {
	suite.Run(t, new(ExampleSuite))
}

func (s *ExampleSuite) TestSetupSuccessful() {
	// 验证基础组件已初始化
	s.NotNil(s.PortAllocator, "PortAllocator 应初始化")
	s.NotNil(s.DataDirManager, "DataDirManager 应初始化")
	s.NotNil(s.ProcessManager, "ProcessManager 应初始化")
	s.NotNil(s.Logger, "Logger 应初始化")
}

func (s *ExampleSuite) TestPortAllocation() {
	port, err := s.PortAllocator.AllocatePort("test-example")
	s.NoError(err)
	s.Greater(port, 0)

	// 验证端口绑定
	binding := s.PortAllocator.GetBinding(port)
	s.NotNil(binding)
	s.Equal("test-example", binding.TestID)
}

func (s *ExampleSuite) TestDataDirCreation() {
	testDir, err := s.DataDirManager.CreateTestDir("test-example-dir")
	s.NoError(err)
	s.NotEmpty(testDir)

	// 验证目录绑定
	dir := s.DataDirManager.GetTestDir("test-example-dir")
	s.Equal(testDir, dir)
}

// E2ETestSuite 基础测试套件测试
type E2ETestSuiteTest struct {
	suite.Suite
}

func TestE2ETestSuite(t *testing.T) {
	suite.Run(t, new(E2ETestSuiteTest))
}

func (s *E2ETestSuiteTest) TestNewE2ETestSuite() {
	suite := NewE2ETestSuite()
	assert.NotNil(s.T(), suite)
	assert.NotNil(s.T(), suite.PortAllocator)
	assert.NotNil(s.T(), suite.DataDirManager)
	assert.NotNil(s.T(), suite.ProcessManager)
	assert.NotNil(s.T(), suite.Logger)
}
