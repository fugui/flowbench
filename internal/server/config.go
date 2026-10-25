// Package server 实现核心被测服务（Server SUT）的前置参数校验、下游 Backend 调用编排与四步二次加工引擎。
package server

import "time"

// 故障突变模式常量，用于验证 Keploy 能否检出 Server 侧业务逻辑回归缺陷
const (
	MutationNone        = ""
	MutationDiscountBug = "discount_bug" // 模拟折扣多减 100 分 (1.00 元) 的回归缺陷
)

// Config 定义 Server 子系统的运行配置
type Config struct {
	ListenAddr    string
	BackendAddr   string
	ConnMode      string
	IOTimeout     time.Duration
	Deterministic bool
	FaultMutation string
	DebugFrame    bool
}

// DefaultConfig 返回 Server 默认安全配置（默认监听 127.0.0.1:9100，连接 Backend 127.0.0.1:9200）
func DefaultConfig() Config {
	return Config{
		ListenAddr:    "127.0.0.1:9100",
		BackendAddr:   "127.0.0.1:9200",
		ConnMode:      "short",
		IOTimeout:     5 * time.Second,
		Deterministic: true,
		FaultMutation: MutationNone,
		DebugFrame:    false,
	}
}
