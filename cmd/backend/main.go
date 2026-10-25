// flowbench-backend 是三级微服务调用链中的下游基础数据与原子规则服务。
// 默认监听 127.0.0.1:9200，通过自定义 24B 二进制帧 + Protobuf 协议响应 Server 请求。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fugui/flowbench/internal/backend"
	"github.com/fugui/flowbench/internal/transport"
)

func main() {
	listenAddr := flag.String("addr", "127.0.0.1:9200", "Backend TCP 监听地址 (默认仅绑定本地环回 127.0.0.1:9200)")
	ioTimeout := flag.Duration("io-timeout", 5*time.Second, "连接读写超时时间")
	debugFrame := flag.Bool("debug-frame", false, "是否输出 24B 二进制帧头与载荷 Hex 调试日志")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store := backend.NewDefaultStore()
	handler := backend.NewHandler(store, logger)

	srv := transport.NewServer(transport.ServerConfig{
		Name:       "flowbench-backend",
		ListenAddr: *listenAddr,
		IOTimeout:  *ioTimeout,
		DebugFrame: *debugFrame,
		Logger:     logger,
	}, handler.HandleFrame)

	if _, err := srv.Start(); err != nil {
		logger.Error("启动 flowbench-backend 失败", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("收到停止信号，正在优雅关闭 flowbench-backend...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("关闭 flowbench-backend 超时", "err", err)
		os.Exit(1)
	}
}
