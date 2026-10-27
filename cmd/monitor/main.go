// flowbench-monitor 是基于 cilium/ebpf 的原生自定义二进制协议监控、智能回放与实时 Web 可视化控制台入口。
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fugui/flowbench/internal/monitor"
)

func main() {
	var (
		listenAddr string
		assetPath  string
		autoSeed   bool
	)
	flag.StringVar(&listenAddr, "addr", "127.0.0.1:9300", "Web 可视化监控控制台监听地址 (仅限本地环回)")
	flag.StringVar(&assetPath, "assets", "data/native-ebpf-assets.json", "原生协议感知录制资产文件路径")
	flag.BoolVar(&autoSeed, "auto-seed", true, "启动时自动执行一次全链路 eBPF 录制以初始化控制台基线数据")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	eng := monitor.NewEngine(monitor.EngineConfig{
		AssetFilePath: assetPath,
	})
	defer func() {
		if err := eng.Close(); err != nil {
			logger.Error("卸载 cilium/ebpf 探针失败", "err", err)
		}
	}()

	if autoSeed {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if rep, err := eng.RunExperiment(ctx, "record"); err != nil {
			logger.Warn("启动初始录制未完成", "err", err)
		} else {
			logger.Info("已完成初始全链路 eBPF 录制与资产落盘",
				"ebpf_mode", rep.EBPFStatus.Mode,
				"prog_id", rep.EBPFStatus.ProgramID,
				"ingress_redirected", rep.EBPFStatus.StatIngressRedirected,
				"egress_redirected", rep.EBPFStatus.StatEgressRedirected,
				"scenarios_passed", rep.PassedScenarios,
				"mocks_recorded", rep.MocksLoaded,
			)
		}
		cancel()
	}

	webSrv := monitor.NewWebServer(listenAddr, eng)
	actualAddr, err := webSrv.Start()
	if err != nil {
		logger.Error("启动 Web 监控控制台失败", "addr", listenAddr, "err", err)
		os.Exit(1)
	}

	logger.Info("flowbench cilium/ebpf 实时可视化监控控制台已就绪", "url", "http://"+actualAddr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("收到停止信号，正在卸载 cilium/ebpf 探针并关闭 Web 控制台...", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := webSrv.Shutdown(ctx); err != nil {
		logger.Error("关闭 Web 控制台异常", "err", err)
	}
}
