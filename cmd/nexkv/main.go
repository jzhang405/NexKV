// Package main NexKV 演示程序
package main

import (
	"fmt"
	"time"

	"github.com/jzhang405/NexKV/internal/metadata/clock"
	"github.com/jzhang405/NexKV/internal/metadata/config"
	"github.com/jzhang405/NexKV/internal/metadata/config/logging"
	"github.com/jzhang405/NexKV/internal/metadata/uuid"
)

func main() {
	logging.Info("=== NexKV Phase 1 演示程序 ===")
	logging.Info("展示 HLC 混合逻辑时钟和 UUID 生成器")

	// 1. 演示 HLC 混合逻辑时钟
	logging.Info("\n--- HLC 混合逻辑时钟演示 ---")
	demonstrateHLC()

	// 2. 演示 UUID v4（随机）
	logging.Info("\n--- UUID v4（随机）演示 ---")
	demonstrateUUIDv4()

	// 3. 演示 UUID v7（时间有序）
	logging.Info("\n--- UUID v7（时间有序）演示 ---")
	demonstrateUUIDv7()

	// 4. 演示 Snowflake ID（高性能）
	logging.Info("\n--- Snowflake ID（高性能）演示 ---")
	demonstrateSnowflake()

	// 5. 演示配置加载
	logging.Info("\n--- 配置加载演示 ---")
	demonstrateConfig()

	logging.Info("\n=== Phase 1 演示完成 ===")
}

// demonstrateHLC 演示 HLC 混合逻辑时钟
func demonstrateHLC() {
	hlc := clock.NewHLC()

	logging.Info("生成 10 个 HLC 时间戳:")
	for i := 0; i < 10; i++ {
		ts := hlc.Now()
		logging.WithFields(map[string]interface{}{
			"index":   i + 1,
			"pt":      ts.PhysicalTime(),
			"c":       ts.LogicalCounter(),
			"time":    ts.ToTime().Format("15:04:05.000"),
			"string":  ts.String(),
		}).Info("HLC 时间戳")
		time.Sleep(time.Microsecond * 100)
	}
}

// demonstrateUUIDv4 演示 UUID v4
func demonstrateUUIDv4() {
	logging.Info("生成 10 个 UUID v4（密码学安全随机）:")
	for i := 0; i < 10; i++ {
		id := uuid.GenerateUUIDv4()
		logging.WithField("index", i+1).Info(fmt.Sprintf("UUID v4: %s", id))
	}
}

// demonstrateUUIDv7 演示 UUID v7
func demonstrateUUIDv7() {
	logging.Info("生成 10 个 UUID v7（时间有序，适合 2PC 事务 ID）:")
	for i := 0; i < 10; i++ {
		id := uuid.GenerateUUIDv7()
		timestamp, _ := uuid.ExtractTimestamp(id)
		logging.WithFields(map[string]interface{}{
			"index":     i + 1,
			"uuid":      id,
			"timestamp": timestamp,
			"datetime":  time.Unix(timestamp/1000, (timestamp%1000)*1e6).Format("15:04:05.000"),
		}).Info("UUID v7")
		time.Sleep(time.Microsecond * 100)
	}
}

// demonstrateSnowflake 演示 Snowflake ID
func demonstrateSnowflake() {
	snowflake, err := uuid.NewSnowflake(0, 0)
	if err != nil {
		logging.Errorf("创建 Snowflake 生成器失败: %v", err)
		return
	}

	logging.Info("生成 10 个 Snowflake ID（高性能短 ID，适合节点 ID）:")
	for i := 0; i < 10; i++ {
		id, err := snowflake.Generate()
		if err != nil {
			logging.Errorf("生成 Snowflake ID 失败: %v", err)
			continue
		}

		parsed := uuid.ParseSnowflake(id)
		logging.WithFields(map[string]interface{}{
			"index":        i + 1,
			"id":           id,
			"datetime":     parsed["datetime"],
			"datacenterID": parsed["datacenterID"],
			"workerID":     parsed["workerID"],
			"sequence":     parsed["sequence"],
		}).Info("Snowflake ID")
	}
}

// demonstrateConfig 演示配置加载
func demonstrateConfig() {
	loader := config.NewLoader("configs/config.yaml")
	cfg, err := loader.Load()
	if err != nil {
		logging.Errorf("加载配置失败: %v", err)
		return
	}

	logging.WithFields(map[string]interface{}{
		"cluster_name":    cfg.Cluster.Name,
		"node_id":         cfg.Cluster.NodeID,
		"node_addr":       cfg.Cluster.NodeAddr,
		"group_size":      cfg.Cluster.TreeCoord.GroupSize,
		"gossip_interval": cfg.Metadata.GossipInterval,
		"quorum_timeout":  cfg.Metadata.QuorumTimeout,
	}).Info("配置加载成功")
}
