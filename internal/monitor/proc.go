package monitor

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ProcessSocketInfo 描述 OS 级活动用户进程及其关联的 TCP 监听端口与活跃出站连接
type ProcessSocketInfo struct {
	PID             int      `json:"pid"`
	Name            string   `json:"name"`
	Cmdline         string   `json:"cmdline"`
	CgroupPath      string   `json:"cgroup_path"`
	ListenPorts     []uint16 `json:"listen_ports"`
	ActiveEndpoints []string `json:"active_endpoints"`
	IsSelfMonitor   bool     `json:"is_self_monitor"`
	IsFlowbenchDemo bool     `json:"is_flowbench_demo"`
}

type tcpSocketEntry struct {
	localIP    string
	localPort  uint16
	remoteIP   string
	remotePort uint16
	stateHex   string // "0A" = LISTEN, "01" = ESTABLISHED
}

// ScanOSProcesses 纯 Go 读取 Linux /proc 文件系统，枚举系统活动进程及其关联的监听端口与出站连接（零外部命令依赖）。
func ScanOSProcesses(keyword string) ([]ProcessSocketInfo, error) {
	inodeMap := parseProcNetTCP("/proc/net/tcp")

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	selfPID := os.Getpid()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	var results []ProcessSocketInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid <= 0 {
			continue
		}

		procDir := "/proc/" + strconv.Itoa(pid)
		commBytes, err := os.ReadFile(procDir + "/comm")
		if err != nil {
			continue
		}
		procName := strings.TrimSpace(string(commBytes))
		if procName == "" {
			continue
		}
		// 跳过常见内核线程前缀
		if strings.HasPrefix(procName, "kworker/") || strings.HasPrefix(procName, "ksoftirqd/") || strings.HasPrefix(procName, "migration/") {
			continue
		}

		cmdline := readProcCmdline(procDir + "/cmdline")
		if cmdline == "" {
			cmdline = procName
		}
		cgroupPath := readProcCgroup(procDir + "/cgroup")
		listenPorts, activeEndpoints := matchProcessSockets(procDir+"/fd", inodeMap)

		info := ProcessSocketInfo{
			PID:             pid,
			Name:            procName,
			Cmdline:         cmdline,
			CgroupPath:      cgroupPath,
			ListenPorts:     listenPorts,
			ActiveEndpoints: activeEndpoints,
			IsSelfMonitor:   pid == selfPID,
			IsFlowbenchDemo: strings.Contains(strings.ToLower(procName), "flowbench") || strings.Contains(strings.ToLower(cmdline), "flowbench"),
		}

		if kw != "" {
			matchText := strings.ToLower(fmt.Sprintf("%d %s %s %v %v", info.PID, info.Name, info.Cmdline, info.ListenPorts, info.ActiveEndpoints))
			if !strings.Contains(matchText, kw) {
				continue
			}
		}

		results = append(results, info)
	}

	// 排序策略：优先展示 flowbench 沙箱/自身进程，其次展示拥有 TCP LISTEN 端口的网络服务进程，最后按 PID 升序
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.IsFlowbenchDemo != b.IsFlowbenchDemo {
			return a.IsFlowbenchDemo
		}
		aHasListen := len(a.ListenPorts) > 0
		bHasListen := len(b.ListenPorts) > 0
		if aHasListen != bHasListen {
			return aHasListen
		}
		aHasActive := len(a.ActiveEndpoints) > 0
		bHasActive := len(b.ActiveEndpoints) > 0
		if aHasActive != bHasActive {
			return aHasActive
		}
		return a.PID < b.PID
	})

	if len(results) > 60 {
		results = results[:60]
	}
	return results, nil
}

func parseProcNetTCP(path string) map[string]tcpSocketEntry {
	out := make(map[string]tcpSocketEntry)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	for scanner.Scan() {
		lineIdx++
		if lineIdx == 1 {
			continue // 跳过表头
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		stateHex := strings.ToUpper(fields[3])
		if stateHex != "0A" && stateHex != "01" {
			continue
		}
		inode := fields[9]
		if inode == "0" || inode == "" {
			continue
		}
		lip, lport, ok1 := parseHexIPPort(fields[1])
		rip, rport, ok2 := parseHexIPPort(fields[2])
		if !ok1 || !ok2 {
			continue
		}
		out[inode] = tcpSocketEntry{
			localIP:    lip,
			localPort:  lport,
			remoteIP:   rip,
			remotePort: rport,
			stateHex:   stateHex,
		}
	}
	return out
}

func parseHexIPPort(s string) (string, uint16, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return "", 0, false
	}
	ipBytes, err := hex.DecodeString(parts[0])
	if err != nil || len(ipBytes) != 4 {
		return "", 0, false
	}
	// /proc/net/tcp 中 IPv4 按小端 32 位整型十六进制输出
	ip := net.IPv4(ipBytes[3], ipBytes[2], ipBytes[1], ipBytes[0]).String()
	portVal, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return "", 0, false
	}
	return ip, uint16(portVal), true
}

func matchProcessSockets(fdDir string, inodeMap map[string]tcpSocketEntry) ([]uint16, []string) {
	if len(inodeMap) == 0 {
		return nil, nil
	}
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return nil, nil
	}

	listenSet := make(map[uint16]struct{})
	endpointSet := make(map[string]struct{})

	for _, fd := range fds {
		target, err := os.Readlink(fdDir + "/" + fd.Name())
		if err != nil {
			continue
		}
		if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
			continue
		}
		inode := target[8 : len(target)-1]
		entry, ok := inodeMap[inode]
		if !ok {
			continue
		}
		if entry.stateHex == "0A" {
			listenSet[entry.localPort] = struct{}{}
		} else if entry.stateHex == "01" && entry.remotePort > 0 {
			endpointSet[fmt.Sprintf("%s:%d", entry.remoteIP, entry.remotePort)] = struct{}{}
		}
	}

	listenPorts := make([]uint16, 0, len(listenSet))
	for p := range listenSet {
		listenPorts = append(listenPorts, p)
	}
	sort.Slice(listenPorts, func(i, j int) bool { return listenPorts[i] < listenPorts[j] })

	endpoints := make([]string, 0, len(endpointSet))
	for ep := range endpointSet {
		endpoints = append(endpoints, ep)
	}
	sort.Strings(endpoints)

	return listenPorts, endpoints
}

func readProcCmdline(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return ""
	}
	s := strings.ReplaceAll(string(raw), "\x00", " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func readProcCgroup(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return "/sys/fs/cgroup"
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[2] != "" {
			rel := strings.TrimSpace(parts[2])
			if rel == "/" {
				return "/sys/fs/cgroup"
			}
			return "/sys/fs/cgroup" + rel
		}
	}
	return "/sys/fs/cgroup"
}
