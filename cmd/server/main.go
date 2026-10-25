// flowbench-server 是三级微服务调用链中的核心被测服务（System Under Test, SUT）。
// 接收 Client 的自定义二进制 Protobuf 请求，执行前置校验、调用下游 Backend、完成业务二次加工后返回 Client。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fugui/flowbench/internal/server"
	"github.com/fugui/flowbench/internal/transport"
)

func main() {
	cfg := server.DefaultConfig()
	flag.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "Server TCP 监听地址 (默认 127.0.0.1:9100)")
	flag.StringVar(&cfg.BackendAddr, "backend-addr", cfg.BackendAddr, "下游 Backend TCP 地址 (默认 127.0.0.1:9200)")
	flag.StringVar(&cfg.ConnMode, "conn-mode", cfg.ConnMode, "连接下游模式: short | keepalive")
	flag.DurationVar(&cfg.IOTimeout, "io-timeout", cfg.IOTimeout, "网络读写超时时间")
	flag.BoolVar(&cfg.Deterministic, "deterministic", cfg.Deterministic, "是否启用纯确定性时钟与流水号模式")
	flag.StringVar(&cfg.FaultMutation, "fault-mutation", cfg.FaultMutation, "故障突变注入开关 (例如 discount_bug)")
	flag.BoolVar(&cfg.DebugFrame, "debug-frame", cfg.DebugFrame, "是否输出 24B 二进制帧头与载荷 Hex 调试日志")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	handler := server.NewHandler(cfg, logger)
	defer func() {
		if err := handler.Close(); err != nil {
			logger.Warn("关闭下游客户端连接提示", "err", err)
		}
	}()

	srv := transport.NewServer(transport.ServerConfig{
		Name:       "flowbench-server",
		ListenAddr: cfg.ListenAddr,
		IOTimeout:  cfg.IOTimeout,
		DebugFrame: cfg.DebugFrame,
		Logger:     logger,
	}, handler.HandleFrame)

	if _, err := srv.Start(); err != nil {
		logger.Error("启动 flowbench-server 失败", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("收到停止信号，正在优雅关闭 flowbench-server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("关闭 flowbench-server 超时", "err", err)
		os.Exit(1)
	}
}
