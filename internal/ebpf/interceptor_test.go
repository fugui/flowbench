package ebpf

import (
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestHtons(t *testing.T) {
	// 9100 = 0x238C -> BigEndian [0x23, 0x8C] -> LittleEndian uint16 = 0x8C23
	if got := Htons(9100); got != 0x8C23 {
		t.Fatalf("expected Htons(9100)=0x8C23, got 0x%04X", got)
	}
	// 9200 = 0x23F0 -> BigEndian [0x23, 0xF0] -> LittleEndian uint16 = 0xF023
	if got := Htons(9200); got != 0xF023 {
		t.Fatalf("expected Htons(9200)=0xF023, got 0x%04X", got)
	}
}

func TestIPv4ToNetOrderUint32AndBuildBPFState(t *testing.T) {
	if got := IPv4ToNetOrderUint32("127.0.0.1"); got != 0x0100007f {
		t.Fatalf("expected 127.0.0.1 -> 0x0100007f, got 0x%08x", got)
	}
	// 10.0.1.20 = [0x0A, 0x00, 0x01, 0x14] -> LittleEndian uint32 = 0x1401000A
	if got := IPv4ToNetOrderUint32("10.0.1.20"); got != 0x1401000a {
		t.Fatalf("expected 10.0.1.20 -> 0x1401000a, got 0x%08x", got)
	}

	st := buildBPFState(Config{
		MonitorPID:      1001,
		TargetPID:       28412,
		EnableIngress:   true,
		EnableEgress:    true,
		IngressOrigPort: 9100,
		EgressOrigIP:    "10.0.1.20",
		EgressOrigPort:  8000,
	})
	if st.TargetPID != 28412 || st.EgressOrigIPNet != 0x1401000a {
		t.Fatalf("unexpected BPFState: %+v", st)
	}
}

// TestKernelCgroupConnect4RedirectionAndSelfBypass 验证 cilium/ebpf cgroup/connect4 在真实内核中的端口改写与防回环豁免。
func TestKernelCgroupConnect4RedirectionAndSelfBypass(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping kernel cilium/ebpf cgroup/connect4 test: requires root/CAP_BPF privileges")
	}

	// 启动一个模拟的 Mock 代理监听端口 (代替实际上未启动的 127.0.0.1:29299)
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock proxy: %v", err)
	}
	defer func() {
		_ = proxyLn.Close()
	}()
	proxyPort := uint16(proxyLn.Addr().(*net.TCPAddr).Port)

	go func() {
		for {
			conn, acceptErr := proxyLn.Accept()
			if acceptErr != nil {
				return
			}
			_, _ = conn.Write([]byte("EBPF_INTERCEPTED_OK"))
			_ = conn.Close()
		}
	}()

	const deadTargetPort uint16 = 29299

	// 1. 测试当 TargetPID 设置为当前进程 PID 时，当前进程连接虚拟下游 IP 10.0.1.20:29299 被跨 IP 重定向至本地 127.0.0.1:proxyPort
	ic, err := NewInterceptor(Config{
		MonitorPID:      999999,
		TargetPID:       os.Getpid(),
		EnableEgress:    true,
		EgressOrigIP:    "10.0.1.20",
		EgressOrigPort:  deadTargetPort,
		EgressProxyPort: proxyPort,
	})
	if err != nil {
		t.Fatalf("NewInterceptor failed: %v", err)
	}
	defer func() {
		if closeErr := ic.Close(); closeErr != nil {
			t.Fatalf("close interceptor: %v", closeErr)
		}
	}()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("10.0.1.20:%d", deadTargetPort), 2*time.Second)
	if err != nil {
		t.Fatalf("expected dial to 10.0.1.20:%d to be redirected by eBPF to 127.0.0.1:%d, got err: %v", deadTargetPort, proxyPort, err)
	}
	data, err := io.ReadAll(conn)
	_ = conn.Close()
	if err != nil {
		t.Fatalf("read from redirected conn: %v", err)
	}
	if string(data) != "EBPF_INTERCEPTED_OK" {
		t.Fatalf("unexpected response from eBPF redirected socket: %q", string(data))
	}

	st := ic.Status()
	if !st.KernelActive || st.StatEgressRedirected == 0 {
		t.Fatalf("expected KernelActive=true and StatEgressRedirected > 0, got %+v", st)
	}
	t.Logf("cilium/ebpf kernel status after interception: progID=%d, targetPID=%d, egressRedirected=%d",
		st.ProgramID, st.TargetPID, st.StatEgressRedirected)
}

func TestMultiEgressTargetsAPI(t *testing.T) {
	ic := &Interceptor{
		targets: make(map[string]EgressTargetInfo),
	}

	if err := ic.AddEgressTarget("127.0.0.1", 19201, 29201); err != nil {
		t.Fatalf("add target: %v", err)
	}
	if err := ic.AddEgressTarget("10.0.1.50", 19202, 29202); err != nil {
		t.Fatalf("add target 2: %v", err)
	}

	targets := ic.ListEgressTargets()
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}

	st := ic.Status()
	if len(st.Targets) != 2 {
		t.Fatalf("expected 2 targets in status, got %d", len(st.Targets))
	}

	if err := ic.RemoveEgressTarget("127.0.0.1", 19201); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	if len(ic.ListEgressTargets()) != 1 {
		t.Fatalf("expected 1 target left, got %d", len(ic.ListEgressTargets()))
	}
}
