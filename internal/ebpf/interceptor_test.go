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

	// 1. 先测试当 MonitorPID 设置为虚拟 PID (999999) 时，当前测试进程连接不存在的 127.0.0.1:29299 会被内核透明重定向到 proxyPort
	ic, err := NewInterceptor(Config{
		MonitorPID:      999999, // 非当前进程 PID，因此当前进程会被拦截重定向
		EnableEgress:    true,
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

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", deadTargetPort), 2*time.Second)
	if err != nil {
		t.Fatalf("expected dial to dead port %d to be redirected by eBPF to %d, got err: %v", deadTargetPort, proxyPort, err)
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
	t.Logf("cilium/ebpf kernel status after interception: progID=%d, egressRedirected=%d, totalConnects=%d",
		st.ProgramID, st.StatEgressRedirected, st.StatTotalConnects)
}
