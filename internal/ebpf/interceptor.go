// Package ebpf 基于 github.com/cilium/ebpf 提供 Linux 内核态 cgroup/connect4 透明 TCP 连接劫持探针与 BPF Map 遥测管理。
// 该模块无需外部 clang/llvm 依赖，直接通过 cilium/ebpf/asm 构建内核验证器友好的 eBPF 字节码，
// 实现对 flowbench 三级微服务入站 (Ingress: 9100) 与出站 (Egress: 9200) 二进制流量的零侵入透明重定向，
// 并利用 bpf_get_current_pid_tgid() 在内核态自动豁免监控器进程自身的透传连接，彻底消除代理死锁回环。
package ebpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

const (
	// StateMapName 为内核态与用户态共享的配置及统计 BPF Map 名称。
	StateMapName = "flowbench_state_map"
	// ProgramName 为挂载至 cgroup/connect4 的 eBPF 程序名称。
	ProgramName = "flowbench_connect4"

	// FlagInterceptIngress 启用入站目标端口 (如 127.0.0.1:9100) 透明重定向。
	FlagInterceptIngress uint64 = 1 << 0
	// FlagInterceptEgress 启用出站目标端口 (如 127.0.0.1:9200) 透明重定向。
	FlagInterceptEgress uint64 = 1 << 1

	// loopbackIPv4NetOrder 为 127.0.0.1 在小端架构下读取的网络字节序 uint32 值 (0x0100007f)。
	loopbackIPv4NetOrder int32 = 0x0100007f

	// struct bpf_sock_addr 字段字节偏移量 (参考 <linux/bpf.h>)
	sockAddrOffsetUserIP4  = 4
	sockAddrOffsetUserPort = 24
)

// BPFState 对应 BPF Array Map (Key=0, ValueSize=80) 中的 8 字节对齐内存布局。
type BPFState struct {
	MonitorPID            uint64 `json:"monitor_pid"`
	EnableFlags           uint64 `json:"enable_flags"`
	IngressOrigPortNet    uint64 `json:"ingress_orig_port_net"`
	IngressProxyPortNet   uint64 `json:"ingress_proxy_port_net"`
	EgressOrigPortNet     uint64 `json:"egress_orig_port_net"`
	EgressProxyPortNet    uint64 `json:"egress_proxy_port_net"`
	StatIngressRedirected uint64 `json:"stat_ingress_redirected"`
	StatEgressRedirected  uint64 `json:"stat_egress_redirected"`
	StatBypassedSelf      uint64 `json:"stat_bypassed_self"`
	StatTotalConnects     uint64 `json:"stat_total_connects"`
}

// Config 定义 eBPF 连接劫持器的挂载与端口重映射参数。
type Config struct {
	CgroupPath       string // 默认为 /sys/fs/cgroup
	MonitorPID       int    // 豁免防回环的监控器自身 PID，默认为 os.Getpid()
	EnableIngress    bool   // 是否劫持 Ingress 端口 (Client -> Server)
	IngressOrigPort  uint16 // 原始 Server 端口 (如 9100)
	IngressProxyPort uint16 // Ingress 协议感知代理端口 (如 19100)
	EnableEgress     bool   // 是否劫持 Egress 端口 (Server -> Backend)
	EgressOrigPort   uint16 // 原始 Backend 端口 (如 9200)
	EgressProxyPort  uint16 // Egress 协议感知代理/Mock 端口 (如 19200)
}

// RuntimeStatus 描述 eBPF 探针的当前运行态与内核统计指标。
type RuntimeStatus struct {
	KernelActive          bool   `json:"kernel_active"`
	Mode                  string `json:"mode"` // "KERNEL_EBPF_CGROUP_CONNECT4" 或 "USERSPACE_PROXY_FALLBACK"
	CgroupPath            string `json:"cgroup_path"`
	ProgramID             uint32 `json:"program_id"`
	MonitorPID            uint64 `json:"monitor_pid"`
	IngressIntercepted    bool   `json:"ingress_intercepted"`
	EgressIntercepted     bool   `json:"egress_intercepted"`
	IngressOrigPort       uint16 `json:"ingress_orig_port"`
	IngressProxyPort      uint16 `json:"ingress_proxy_port"`
	EgressOrigPort        uint16 `json:"egress_orig_port"`
	EgressProxyPort       uint16 `json:"egress_proxy_port"`
	StatTotalConnects     uint64 `json:"stat_total_connects"`
	StatIngressRedirected uint64 `json:"stat_ingress_redirected"`
	StatEgressRedirected  uint64 `json:"stat_egress_redirected"`
	StatBypassedSelf      uint64 `json:"stat_bypassed_self"`
	FallbackReason        string `json:"fallback_reason,omitempty"`
}

// Interceptor 管理 cilium/ebpf 内核程序、BPF Map 与 Cgroup Link 的生命周期。
type Interceptor struct {
	mu             sync.RWMutex
	cfg            Config
	stateMap       *ebpf.Map
	prog           *ebpf.Program
	cgroupLink     link.Link
	progID         uint32
	kernelActive   bool
	fallbackReason string
}

// Htons 将主机字节序端口转换为网络字节序 (Big-Endian uint16)。
func Htons(port uint16) uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], port)
	return binary.LittleEndian.Uint16(b[:])
}

// DetectCgroupV2Path 自动探测本机可用的 cgroup v2 挂载路径。
func DetectCgroupV2Path() string {
	candidates := []string{
		"/sys/fs/cgroup",
		"/sys/fs/cgroup/unified",
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			return p
		}
	}
	return "/sys/fs/cgroup"
}

// NewInterceptor 创建并尝试在内核加载挂载 cilium/ebpf cgroup/connect4 探针。
func NewInterceptor(cfg Config) (*Interceptor, error) {
	if cfg.CgroupPath == "" {
		cfg.CgroupPath = DetectCgroupV2Path()
	}
	if cfg.MonitorPID <= 0 {
		cfg.MonitorPID = os.Getpid()
	}

	ic := &Interceptor{
		cfg: cfg,
	}

	if err := rlimit.RemoveMemlock(); err != nil {
		ic.fallbackReason = fmt.Sprintf("remove memlock rlimit failed: %v", err)
		return ic, err
	}

	mapSpec := &ebpf.MapSpec{
		Name:       StateMapName,
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  80, // sizeof(BPFState) = 10 * 8 bytes
		MaxEntries: 1,
	}

	stateMap, err := ebpf.NewMap(mapSpec)
	if err != nil {
		ic.fallbackReason = fmt.Sprintf("create bpf map failed: %v", err)
		return ic, err
	}

	initialState := buildBPFState(cfg)
	keyZero := uint32(0)
	if err := stateMap.Put(&keyZero, &initialState); err != nil {
		_ = stateMap.Close()
		ic.fallbackReason = fmt.Sprintf("init bpf map state failed: %v", err)
		return ic, err
	}

	progSpec := &ebpf.ProgramSpec{
		Name:       ProgramName,
		Type:       ebpf.CGroupSockAddr,
		AttachType: ebpf.AttachCGroupInet4Connect,
		License:    "Dual BSD/GPL",
		Instructions: buildConnect4Instructions(
			stateMap.FD(),
		),
	}

	prog, err := ebpf.NewProgram(progSpec)
	if err != nil {
		_ = stateMap.Close()
		ic.fallbackReason = fmt.Sprintf("load cgroup/connect4 bpf program failed: %v", err)
		return ic, err
	}

	cgLink, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cfg.CgroupPath,
		Attach:  ebpf.AttachCGroupInet4Connect,
		Program: prog,
	})
	if err != nil {
		_ = prog.Close()
		_ = stateMap.Close()
		ic.fallbackReason = fmt.Sprintf("attach cgroup (%s) failed: %v", cfg.CgroupPath, err)
		return ic, err
	}

	var progInfoID uint32
	if info, infoErr := prog.Info(); infoErr == nil {
		if id, ok := info.ID(); ok {
			progInfoID = uint32(id)
		}
	}

	ic.stateMap = stateMap
	ic.prog = prog
	ic.cgroupLink = cgLink
	ic.progID = progInfoID
	ic.kernelActive = true
	return ic, nil
}

func buildBPFState(cfg Config) BPFState {
	var flags uint64
	if cfg.EnableIngress {
		flags |= FlagInterceptIngress
	}
	if cfg.EnableEgress {
		flags |= FlagInterceptEgress
	}
	return BPFState{
		MonitorPID:          uint64(cfg.MonitorPID),
		EnableFlags:         flags,
		IngressOrigPortNet:  uint64(Htons(cfg.IngressOrigPort)),
		IngressProxyPortNet: uint64(Htons(cfg.IngressProxyPort)),
		EgressOrigPortNet:   uint64(Htons(cfg.EgressOrigPort)),
		EgressProxyPortNet:  uint64(Htons(cfg.EgressProxyPort)),
	}
}

// buildConnect4Instructions 使用 cilium/ebpf/asm 构建内核态 cgroup/connect4 字节码。
func buildConnect4Instructions(mapFD int) asm.Instructions {
	const (
		offMonitorPID       = 0
		offEnableFlags      = 8
		offIngressOrig      = 16
		offIngressProxy     = 24
		offEgressOrig       = 32
		offEgressProxy      = 40
		offStatIngressRedir = 48
		offStatEgressRedir  = 56
		offStatBypassSelf   = 64
		offStatTotalConn    = 72
	)

	return asm.Instructions{
		// R6 = R1 (保存 struct bpf_sock_addr *ctx)
		asm.Mov.Reg(asm.R6, asm.R1),

		// 仅处理目标地址为 127.0.0.1 (0x0100007f) 的本地环回连接，绝不影响外部网络
		asm.LoadMem(asm.R2, asm.R6, sockAddrOffsetUserIP4, asm.Word),
		asm.JNE.Imm(asm.R2, loopbackIPv4NetOrder, "exit_allow"),

		// 在栈上准备 key = 0: *(uint32 *)(FP - 4) = 0
		asm.StoreImm(asm.RFP, -4, 0, asm.Word),
		asm.LoadMapPtr(asm.R1, mapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "exit_allow"),

		// R7 = R0 (保存 BPFState 指针)
		asm.Mov.Reg(asm.R7, asm.R0),

		// StatTotalConnects++
		asm.LoadMem(asm.R1, asm.R7, offStatTotalConn, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatTotalConn, asm.R1, asm.DWord),

		// 获取当前调用 connect() 的进程 TGID (用户态 PID)
		asm.FnGetCurrentPidTgid.Call(),
		asm.RSh.Imm(asm.R0, 32),

		// 若 current_tgid == MonitorPID，说明是监控代理自身发起的透传连接，豁免放行防回环
		asm.LoadMem(asm.R1, asm.R7, offMonitorPID, asm.DWord),
		asm.JNE.Reg(asm.R0, asm.R1, "check_ports"),
		asm.LoadMem(asm.R1, asm.R7, offStatBypassSelf, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatBypassSelf, asm.R1, asm.DWord),
		asm.Ja.Label("exit_allow"),

		// 读取 ctx->user_port 至 R8，读取 EnableFlags 至 R9
		asm.LoadMem(asm.R8, asm.R6, sockAddrOffsetUserPort, asm.Word).WithSymbol("check_ports"),
		asm.LoadMem(asm.R9, asm.R7, offEnableFlags, asm.DWord),

		// 1. 检查是否启用并匹配 Egress 端口 (如 Server -> Backend:9200)
		asm.Mov.Reg(asm.R1, asm.R9),
		asm.And.Imm(asm.R1, int32(FlagInterceptEgress)),
		asm.JEq.Imm(asm.R1, 0, "check_ingress"),
		asm.LoadMem(asm.R2, asm.R7, offEgressOrig, asm.DWord),
		asm.JNE.Reg(asm.R8, asm.R2, "check_ingress"),

		// 命中 Egress：将 ctx->user_port 改写为 EgressProxyPort，并递增 StatEgressRedirected
		asm.LoadMem(asm.R3, asm.R7, offEgressProxy, asm.DWord),
		asm.StoreMem(asm.R6, sockAddrOffsetUserPort, asm.R3, asm.Word),
		asm.LoadMem(asm.R1, asm.R7, offStatEgressRedir, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatEgressRedir, asm.R1, asm.DWord),
		asm.Ja.Label("exit_allow"),

		// 2. 检查是否启用并匹配 Ingress 端口 (如 Client -> Server:9100)
		asm.Mov.Reg(asm.R1, asm.R9).WithSymbol("check_ingress"),
		asm.And.Imm(asm.R1, int32(FlagInterceptIngress)),
		asm.JEq.Imm(asm.R1, 0, "exit_allow"),
		asm.LoadMem(asm.R2, asm.R7, offIngressOrig, asm.DWord),
		asm.JNE.Reg(asm.R8, asm.R2, "exit_allow"),

		// 命中 Ingress：将 ctx->user_port 改写为 IngressProxyPort，并递增 StatIngressRedirected
		asm.LoadMem(asm.R3, asm.R7, offIngressProxy, asm.DWord),
		asm.StoreMem(asm.R6, sockAddrOffsetUserPort, asm.R3, asm.Word),
		asm.LoadMem(asm.R1, asm.R7, offStatIngressRedir, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatIngressRedir, asm.R1, asm.DWord),

		// 返回 1 允许连接继续
		asm.Mov.Imm(asm.R0, 1).WithSymbol("exit_allow"),
		asm.Return(),
	}
}

// UpdateConfig 动态更新 BPF Map 中的拦截开关与端口配置（保留已有累计计数）。
func (ic *Interceptor) UpdateConfig(enableIngress, enableEgress bool, ingressProxyPort, egressProxyPort uint16) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	ic.cfg.EnableIngress = enableIngress
	ic.cfg.EnableEgress = enableEgress
	if ingressProxyPort > 0 {
		ic.cfg.IngressProxyPort = ingressProxyPort
	}
	if egressProxyPort > 0 {
		ic.cfg.EgressProxyPort = egressProxyPort
	}

	if !ic.kernelActive || ic.stateMap == nil {
		return nil
	}

	keyZero := uint32(0)
	var cur BPFState
	if err := ic.stateMap.Lookup(&keyZero, &cur); err != nil {
		cur = buildBPFState(ic.cfg)
	} else {
		var flags uint64
		if enableIngress {
			flags |= FlagInterceptIngress
		}
		if enableEgress {
			flags |= FlagInterceptEgress
		}
		cur.EnableFlags = flags
		cur.IngressOrigPortNet = uint64(Htons(ic.cfg.IngressOrigPort))
		cur.IngressProxyPortNet = uint64(Htons(ic.cfg.IngressProxyPort))
		cur.EgressOrigPortNet = uint64(Htons(ic.cfg.EgressOrigPort))
		cur.EgressProxyPortNet = uint64(Htons(ic.cfg.EgressProxyPort))
	}
	return ic.stateMap.Put(&keyZero, &cur)
}

// Status 读取当前 eBPF 探针状态及内核 BPF Map 实时统计指标。
func (ic *Interceptor) Status() RuntimeStatus {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	st := RuntimeStatus{
		KernelActive:       ic.kernelActive,
		Mode:               "USERSPACE_PROXY_FALLBACK",
		CgroupPath:         ic.cfg.CgroupPath,
		ProgramID:          ic.progID,
		MonitorPID:         uint64(ic.cfg.MonitorPID),
		IngressIntercepted: ic.cfg.EnableIngress,
		EgressIntercepted:  ic.cfg.EnableEgress,
		IngressOrigPort:    ic.cfg.IngressOrigPort,
		IngressProxyPort:   ic.cfg.IngressProxyPort,
		EgressOrigPort:     ic.cfg.EgressOrigPort,
		EgressProxyPort:    ic.cfg.EgressProxyPort,
		FallbackReason:     ic.fallbackReason,
	}

	if ic.kernelActive && ic.stateMap != nil {
		st.Mode = "KERNEL_EBPF_CGROUP_CONNECT4"
		keyZero := uint32(0)
		var bpfSt BPFState
		if err := ic.stateMap.Lookup(&keyZero, &bpfSt); err == nil {
			st.StatTotalConnects = bpfSt.StatTotalConnects
			st.StatIngressRedirected = bpfSt.StatIngressRedirected
			st.StatEgressRedirected = bpfSt.StatEgressRedirected
			st.StatBypassedSelf = bpfSt.StatBypassedSelf
		}
	}
	return st
}

// Close 卸载 cgroup/connect4 链接并释放内核 BPF 程序与 Map 资源。
func (ic *Interceptor) Close() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	var errs []error
	if ic.cgroupLink != nil {
		if err := ic.cgroupLink.Close(); err != nil {
			errs = append(errs, err)
		}
		ic.cgroupLink = nil
	}
	if ic.prog != nil {
		if err := ic.prog.Close(); err != nil {
			errs = append(errs, err)
		}
		ic.prog = nil
	}
	if ic.stateMap != nil {
		if err := ic.stateMap.Close(); err != nil {
			errs = append(errs, err)
		}
		ic.stateMap = nil
	}
	ic.kernelActive = false
	return errors.Join(errs...)
}
