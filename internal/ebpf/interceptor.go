// Package ebpf 基于 github.com/cilium/ebpf 提供 Linux 内核态 cgroup/connect4 透明 TCP 连接劫持探针与 BPF Map 遥测管理。
// 该模块无需外部 clang/llvm 依赖，直接通过 cilium/ebpf/asm 构建内核验证器友好的 eBPF 字节码，
// 支持：
// 1. MonitorPID 防死锁回环豁免；
// 2. TargetPID 被测进程级精准过滤（同 Cgroup 下其他进程零误伤）；
// 3. 任意下游 IPv4 地址 (EgressOrigIP:Port -> 127.0.0.1:EgressProxyPort) 的跨 IP 透明重写。
package ebpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

const (
	// StateMapName 为内核态与用户态共享的配置及统计 BPF Map 名称。
	StateMapName = "flowbench_state_map"
	// TargetsMapName 为多下游 Egress 目标哈希表 (DestIP, DestPort -> ProxyPort) 名称。
	TargetsMapName = "flowbench_targets_map"
	// ProgramName 为挂载至 cgroup/connect4 的 eBPF 程序名称。
	ProgramName = "flowbench_connect4"

	// FlagInterceptIngress 启用入站目标端口 (如 127.0.0.1:9100) 透明重定向。
	FlagInterceptIngress uint64 = 1 << 0
	// FlagInterceptEgress 启用出站目标端口 (如 TargetEgressIP:9200) 透明重定向。
	FlagInterceptEgress uint64 = 1 << 1

	// loopbackIPv4NetOrder 为 127.0.0.1 在小端架构下读取的网络字节序 uint32 值 (0x0100007f)。
	loopbackIPv4NetOrder int32 = 0x0100007f

	// struct bpf_sock_addr 字段字节偏移量 (参考 <linux/bpf.h>)
	sockAddrOffsetUserIP4  = 4
	sockAddrOffsetUserPort = 24
)

// BPFState 对应 BPF Array Map (Key=0, ValueSize=96) 中的 8 字节对齐内存布局。
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
	TargetPID             uint64 `json:"target_pid"`         // 0 表示不限制 PID；>0 时仅拦截该 PID 发起的 Egress 连接
	EgressOrigIPNet       uint64 `json:"egress_orig_ip_net"` // 被劫持下游依赖 IPv4 网络字节序 uint32 (默认 127.0.0.1 = 0x0100007f)
}

// EgressTargetKey 对应 BPF Hash Map (KeySize=8) 中的查询键 (DestIP + DestPort 网络字节序)。
type EgressTargetKey struct {
	DestIP   uint32 `json:"dest_ip"`
	DestPort uint32 `json:"dest_port"` // 存储网络字节序 uint16 在低 16 位
}

// EgressTargetVal 对应 BPF Hash Map (ValueSize=4) 中的重定向值 (ProxyPort 网络字节序)。
type EgressTargetVal struct {
	ProxyPort uint32 `json:"proxy_port"` // 存储网络字节序 uint16 在低 16 位
}

// EgressTargetInfo 描述一个出站拦截目标的业务信息。
type EgressTargetInfo struct {
	DestIP    string `json:"dest_ip"`
	DestPort  uint16 `json:"dest_port"`
	ProxyPort uint16 `json:"proxy_port"`
}

// Config 定义 eBPF 连接劫持器的挂载、目标进程过滤与端口重映射参数。
type Config struct {
	CgroupPath       string // 默认为 /sys/fs/cgroup
	MonitorPID       int    // 豁免防回环的监控器自身 PID，默认为 os.Getpid()
	TargetPID        int    // 被测目标业务进程 PID (0 表示全沙箱模式，>0 表示单进程精准过滤)
	EnableIngress    bool   // 是否劫持 Ingress 端口 (Client -> Server)
	IngressOrigPort  uint16 // 原始被测服务监听端口 (如 9100)
	IngressProxyPort uint16 // Ingress 协议感知代理端口 (如 19100)
	EnableEgress     bool   // 是否劫持 Egress 下游连接 (Server -> Downstream)
	EgressOrigIP     string // 原始下游依赖 IPv4 地址 (默认 "127.0.0.1"，支持如 "10.0.1.20")
	EgressOrigPort   uint16 // 原始下游依赖端口 (如 9200)
	EgressProxyPort  uint16 // Egress 协议感知代理/Mock 端口 (如 19200)
}

// RuntimeStatus 描述 eBPF 探针的当前运行态与内核统计指标。
type RuntimeStatus struct {
	KernelActive          bool               `json:"kernel_active"`
	Mode                  string             `json:"mode"` // "KERNEL_EBPF_CGROUP_CONNECT4" 或 "USERSPACE_PROXY_FALLBACK"
	CgroupPath            string             `json:"cgroup_path"`
	ProgramID             uint32             `json:"program_id"`
	MonitorPID            uint64             `json:"monitor_pid"`
	TargetPID             uint64             `json:"target_pid"`
	IngressIntercepted    bool               `json:"ingress_intercepted"`
	EgressIntercepted     bool               `json:"egress_intercepted"`
	IngressOrigPort       uint16             `json:"ingress_orig_port"`
	IngressProxyPort      uint16             `json:"ingress_proxy_port"`
	EgressOrigIP          string             `json:"egress_orig_ip"`
	EgressOrigPort        uint16             `json:"egress_orig_port"`
	EgressProxyPort       uint16             `json:"egress_proxy_port"`
	Targets               []EgressTargetInfo `json:"targets,omitempty"`
	StatTotalConnects     uint64             `json:"stat_total_connects"`
	StatIngressRedirected uint64             `json:"stat_ingress_redirected"`
	StatEgressRedirected  uint64             `json:"stat_egress_redirected"`
	StatBypassedSelf      uint64             `json:"stat_bypassed_self"`
	FallbackReason        string             `json:"fallback_reason,omitempty"`
}

// Interceptor 管理 cilium/ebpf 内核程序、BPF Map 与 Cgroup Link 的生命周期。
type Interceptor struct {
	mu             sync.RWMutex
	cfg            Config
	stateMap       *ebpf.Map
	targetsMap     *ebpf.Map
	targets        map[string]EgressTargetInfo
	prog           *ebpf.Program
	cgroupLink     link.Link
	progID         uint32
	kernelActive   bool
	fallbackReason string
}

// Htons 将主机字节序端口转换为网络字节序 (Big-Endian uint16 在小端机器上的内存表示)。
func Htons(port uint16) uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], port)
	return binary.LittleEndian.Uint16(b[:])
}

// IPv4ToNetOrderUint32 将点分十进制 IPv4 地址转换为 struct bpf_sock_addr.user_ip4 的网络字节序 uint32 值。
func IPv4ToNetOrderUint32(ipStr string) uint32 {
	trimmed := strings.TrimSpace(ipStr)
	if trimmed == "" || trimmed == "localhost" {
		return uint32(loopbackIPv4NetOrder)
	}
	parsed := net.ParseIP(trimmed)
	if parsed == nil {
		return uint32(loopbackIPv4NetOrder)
	}
	ip4 := parsed.To4()
	if ip4 == nil {
		return uint32(loopbackIPv4NetOrder)
	}
	return binary.LittleEndian.Uint32(ip4)
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
	if strings.TrimSpace(cfg.EgressOrigIP) == "" {
		cfg.EgressOrigIP = "127.0.0.1"
	}

	ic := &Interceptor{
		cfg:     cfg,
		targets: make(map[string]EgressTargetInfo),
	}

	if cfg.EgressOrigPort > 0 {
		trimmed := strings.TrimSpace(cfg.EgressOrigIP)
		ic.targets[fmt.Sprintf("%s:%d", trimmed, cfg.EgressOrigPort)] = EgressTargetInfo{
			DestIP:    trimmed,
			DestPort:  cfg.EgressOrigPort,
			ProxyPort: cfg.EgressProxyPort,
		}
	}

	if err := rlimit.RemoveMemlock(); err != nil {
		ic.fallbackReason = fmt.Sprintf("remove memlock rlimit failed: %v", err)
		return ic, err
	}

	mapSpec := &ebpf.MapSpec{
		Name:       StateMapName,
		Type:       ebpf.Array,
		KeySize:    4,
		ValueSize:  96, // sizeof(BPFState) = 12 * 8 bytes
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

	targetsMapSpec := &ebpf.MapSpec{
		Name:       TargetsMapName,
		Type:       ebpf.Hash,
		KeySize:    8, // uint32 DestIP + uint32 DestPort
		ValueSize:  4, // uint32 ProxyPort
		MaxEntries: 128,
	}

	targetsMap, err := ebpf.NewMap(targetsMapSpec)
	if err != nil {
		_ = stateMap.Close()
		ic.fallbackReason = fmt.Sprintf("create egress targets bpf map failed: %v", err)
		return ic, err
	}

	// 初始化已有 targets
	for _, t := range ic.targets {
		k := EgressTargetKey{
			DestIP:   IPv4ToNetOrderUint32(t.DestIP),
			DestPort: uint32(Htons(t.DestPort)),
		}
		v := EgressTargetVal{
			ProxyPort: uint32(Htons(t.ProxyPort)),
		}
		_ = targetsMap.Put(&k, &v)
	}

	progSpec := &ebpf.ProgramSpec{
		Name:       ProgramName,
		Type:       ebpf.CGroupSockAddr,
		AttachType: ebpf.AttachCGroupInet4Connect,
		License:    "Dual BSD/GPL",
		Instructions: buildConnect4Instructions(
			stateMap.FD(),
			targetsMap.FD(),
		),
	}

	prog, err := ebpf.NewProgram(progSpec)
	if err != nil {
		_ = targetsMap.Close()
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
		_ = targetsMap.Close()
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
	ic.targetsMap = targetsMap
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
	var targetPID uint64
	if cfg.TargetPID > 0 {
		targetPID = uint64(cfg.TargetPID)
	}
	return BPFState{
		MonitorPID:          uint64(cfg.MonitorPID),
		EnableFlags:         flags,
		IngressOrigPortNet:  uint64(Htons(cfg.IngressOrigPort)),
		IngressProxyPortNet: uint64(Htons(cfg.IngressProxyPort)),
		EgressOrigPortNet:   uint64(Htons(cfg.EgressOrigPort)),
		EgressProxyPortNet:  uint64(Htons(cfg.EgressProxyPort)),
		TargetPID:           targetPID,
		EgressOrigIPNet:     uint64(IPv4ToNetOrderUint32(cfg.EgressOrigIP)),
	}
}

// buildConnect4Instructions 使用 cilium/ebpf/asm 构建内核态 cgroup/connect4 字节码。
func buildConnect4Instructions(stateMapFD, targetsMapFD int) asm.Instructions {
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
		offTargetPID        = 80
		offEgressOrigIP     = 88
	)

	return asm.Instructions{
		// R6 = R1 (保存 struct bpf_sock_addr *ctx)
		asm.Mov.Reg(asm.R6, asm.R1),

		// 在栈上准备 key = 0: *(uint32 *)(FP - 4) = 0
		asm.StoreImm(asm.RFP, -4, 0, asm.Word),
		asm.LoadMapPtr(asm.R1, stateMapFD),
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

		// 获取当前调用 connect() 的进程 TGID (用户态 PID) 保存至 R5
		asm.FnGetCurrentPidTgid.Call(),
		asm.RSh.Imm(asm.R0, 32),
		asm.Mov.Reg(asm.R5, asm.R0),

		// 若 current_tgid == MonitorPID，说明是监控代理自身发起的透传连接，豁免放行防回环
		asm.LoadMem(asm.R1, asm.R7, offMonitorPID, asm.DWord),
		asm.JNE.Reg(asm.R5, asm.R1, "check_ports"),
		asm.LoadMem(asm.R1, asm.R7, offStatBypassSelf, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatBypassSelf, asm.R1, asm.DWord),
		asm.Ja.Label("exit_allow"),

		// 读取 ctx->user_port 至 R8，读取 EnableFlags 至 R9
		asm.LoadMem(asm.R8, asm.R6, sockAddrOffsetUserPort, asm.Word).WithSymbol("check_ports"),
		asm.LoadMem(asm.R9, asm.R7, offEnableFlags, asm.DWord),

		// 1. 检查是否启用出站拦截
		asm.Mov.Reg(asm.R1, asm.R9),
		asm.And.Imm(asm.R1, int32(FlagInterceptEgress)),
		asm.JEq.Imm(asm.R1, 0, "check_ingress"),

		// 若设置了 TargetPID (> 0)，仅当 current_tgid == TargetPID 时才拦截 Egress
		asm.LoadMem(asm.R1, asm.R7, offTargetPID, asm.DWord),
		asm.JEq.Imm(asm.R1, 0, "lookup_targets_map"),
		asm.JNE.Reg(asm.R5, asm.R1, "check_ingress"),

		// 1.1 优先在 targetsMap (BPF_MAP_TYPE_HASH) 中检索 (ctx->user_ip4, ctx->user_port)
		asm.LoadMem(asm.R1, asm.R6, sockAddrOffsetUserIP4, asm.Word).WithSymbol("lookup_targets_map"),
		asm.StoreMem(asm.RFP, -16, asm.R1, asm.Word),
		asm.StoreMem(asm.RFP, -12, asm.R8, asm.Word),
		asm.LoadMapPtr(asm.R1, targetsMapFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -16),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "check_legacy_egress"),

		// 命中 targetsMap: R0 指向 EgressTargetVal (ProxyPort 为 4 字节 Word)
		asm.LoadMem(asm.R3, asm.R0, 0, asm.Word),
		asm.Mov.Imm(asm.R2, loopbackIPv4NetOrder),
		asm.StoreMem(asm.R6, sockAddrOffsetUserIP4, asm.R2, asm.Word),
		asm.StoreMem(asm.R6, sockAddrOffsetUserPort, asm.R3, asm.Word),
		asm.LoadMem(asm.R1, asm.R7, offStatEgressRedir, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatEgressRedir, asm.R1, asm.DWord),
		asm.Ja.Label("exit_allow"),

		// 1.2 兼容单目标 Legacy Egress 配置 (EgressOrigIPNet, EgressOrigPortNet)
		asm.LoadMem(asm.R2, asm.R7, offEgressOrig, asm.DWord).WithSymbol("check_legacy_egress"),
		asm.JEq.Imm(asm.R2, 0, "check_ingress"),
		asm.LoadMem(asm.R1, asm.R6, sockAddrOffsetUserIP4, asm.Word),
		asm.LoadMem(asm.R3, asm.R7, offEgressOrigIP, asm.DWord),
		asm.JNE.Reg(asm.R1, asm.R3, "check_ingress"),
		asm.JNE.Reg(asm.R8, asm.R2, "check_ingress"),

		// 命中 Legacy Egress：重定向至 127.0.0.1:EgressProxyPort
		asm.Mov.Imm(asm.R3, loopbackIPv4NetOrder),
		asm.StoreMem(asm.R6, sockAddrOffsetUserIP4, asm.R3, asm.Word),
		asm.LoadMem(asm.R3, asm.R7, offEgressProxy, asm.DWord),
		asm.StoreMem(asm.R6, sockAddrOffsetUserPort, asm.R3, asm.Word),
		asm.LoadMem(asm.R1, asm.R7, offStatEgressRedir, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R7, offStatEgressRedir, asm.R1, asm.DWord),
		asm.Ja.Label("exit_allow"),

		// 2. 检查是否启用并匹配 Ingress 端口 (127.0.0.1:IngressOrigPort)
		asm.Mov.Reg(asm.R1, asm.R9).WithSymbol("check_ingress"),
		asm.And.Imm(asm.R1, int32(FlagInterceptIngress)),
		asm.JEq.Imm(asm.R1, 0, "exit_allow"),

		// 若设置了 TargetPID 且当前进程正是被测进程自身，不作为入站客户端拦截
		asm.LoadMem(asm.R1, asm.R7, offTargetPID, asm.DWord),
		asm.JEq.Imm(asm.R1, 0, "check_ingress_addr"),
		asm.JEq.Reg(asm.R5, asm.R1, "exit_allow"),

		asm.LoadMem(asm.R2, asm.R6, sockAddrOffsetUserIP4, asm.Word).WithSymbol("check_ingress_addr"),
		asm.JNE.Imm(asm.R2, loopbackIPv4NetOrder, "exit_allow"),
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
	return ic.UpdateTargetProcess(ic.cfg.TargetPID, ic.cfg.IngressOrigPort, ic.cfg.EgressOrigIP, ic.cfg.EgressOrigPort, enableIngress, enableEgress, ingressProxyPort, egressProxyPort)
}

// UpdateTargetProcess 动态更新目标进程 TargetPID、入站监听端口及下游依赖地址规则（热更新 BPF Map）。
func (ic *Interceptor) UpdateTargetProcess(targetPID int, ingressOrigPort uint16, egressOrigIP string, egressOrigPort uint16, enableIngress, enableEgress bool, ingressProxyPort, egressProxyPort uint16) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	if targetPID >= 0 {
		ic.cfg.TargetPID = targetPID
	}
	if ingressOrigPort > 0 {
		ic.cfg.IngressOrigPort = ingressOrigPort
	}
	if strings.TrimSpace(egressOrigIP) != "" {
		ic.cfg.EgressOrigIP = strings.TrimSpace(egressOrigIP)
	}
	if egressOrigPort > 0 {
		ic.cfg.EgressOrigPort = egressOrigPort
	}
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
		if ic.cfg.TargetPID > 0 {
			cur.TargetPID = uint64(ic.cfg.TargetPID)
		} else {
			cur.TargetPID = 0
		}
		cur.EgressOrigIPNet = uint64(IPv4ToNetOrderUint32(ic.cfg.EgressOrigIP))
	}
	return ic.stateMap.Put(&keyZero, &cur)
}

// AddEgressTarget 动态添加一个出站拦截目标 (如 "127.0.0.1:19201" -> proxyPort)。
func (ic *Interceptor) AddEgressTarget(destIP string, destPort, proxyPort uint16) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	trimmedIP := strings.TrimSpace(destIP)
	if trimmedIP == "" {
		trimmedIP = "127.0.0.1"
	}
	keyStr := fmt.Sprintf("%s:%d", trimmedIP, destPort)
	if ic.targets == nil {
		ic.targets = make(map[string]EgressTargetInfo)
	}
	ic.targets[keyStr] = EgressTargetInfo{
		DestIP:    trimmedIP,
		DestPort:  destPort,
		ProxyPort: proxyPort,
	}

	if !ic.kernelActive || ic.targetsMap == nil {
		return nil
	}

	key := EgressTargetKey{
		DestIP:   IPv4ToNetOrderUint32(trimmedIP),
		DestPort: uint32(Htons(destPort)),
	}
	val := EgressTargetVal{
		ProxyPort: uint32(Htons(proxyPort)),
	}
	return ic.targetsMap.Put(&key, &val)
}

// RemoveEgressTarget 动态移除一个出站拦截目标。
func (ic *Interceptor) RemoveEgressTarget(destIP string, destPort uint16) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	trimmedIP := strings.TrimSpace(destIP)
	if trimmedIP == "" {
		trimmedIP = "127.0.0.1"
	}
	keyStr := fmt.Sprintf("%s:%d", trimmedIP, destPort)
	delete(ic.targets, keyStr)

	if !ic.kernelActive || ic.targetsMap == nil {
		return nil
	}

	key := EgressTargetKey{
		DestIP:   IPv4ToNetOrderUint32(trimmedIP),
		DestPort: uint32(Htons(destPort)),
	}
	err := ic.targetsMap.Delete(&key)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// ListEgressTargets 返回当前配置的所有出站拦截目标。
func (ic *Interceptor) ListEgressTargets() []EgressTargetInfo {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	res := make([]EgressTargetInfo, 0, len(ic.targets))
	for _, v := range ic.targets {
		res = append(res, v)
	}
	return res
}

// Status 读取当前 eBPF 探针状态及内核 BPF Map 实时统计指标。
func (ic *Interceptor) Status() RuntimeStatus {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	egressIP := ic.cfg.EgressOrigIP
	if egressIP == "" {
		egressIP = "127.0.0.1"
	}

	targets := make([]EgressTargetInfo, 0, len(ic.targets))
	for _, t := range ic.targets {
		targets = append(targets, t)
	}

	st := RuntimeStatus{
		KernelActive:       ic.kernelActive,
		Mode:               "USERSPACE_PROXY_FALLBACK",
		CgroupPath:         ic.cfg.CgroupPath,
		ProgramID:          ic.progID,
		MonitorPID:         uint64(ic.cfg.MonitorPID),
		TargetPID:          uint64(ic.cfg.TargetPID),
		IngressIntercepted: ic.cfg.EnableIngress,
		EgressIntercepted:  ic.cfg.EnableEgress,
		IngressOrigPort:    ic.cfg.IngressOrigPort,
		IngressProxyPort:   ic.cfg.IngressProxyPort,
		EgressOrigIP:       egressIP,
		EgressOrigPort:     ic.cfg.EgressOrigPort,
		EgressProxyPort:    ic.cfg.EgressProxyPort,
		Targets:            targets,
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
			st.TargetPID = bpfSt.TargetPID
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
	if ic.targetsMap != nil {
		if err := ic.targetsMap.Close(); err != nil {
			errs = append(errs, err)
		}
		ic.targetsMap = nil
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
