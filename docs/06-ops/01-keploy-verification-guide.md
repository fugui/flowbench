# flowbench 运维、联调与 Keploy 流量录制回放验证指南 (Operations & Verification Guide)

- **文档编号**：`01-keploy-verification-guide.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-25
- **归属阶段**：阶段四：交付验证与运维手册

---

## 1. 快速构建与全量自动化测试

```bash
# 1. 编译三大微服务子系统 (输出至 bin/)
make build

# 2. 运行全量单元测试、三端全链路 E2E 测试与内置二进制录制/离线回放测试 (开启竞态检测)
make test

# 3. 一键执行三端真实进程联调验证 (Short-Conn + Keep-Alive + TCP 粘包/分片压测)
make verify-e2e
```

---

## 2. 三大微服务独立命令行参数速查

### 2.1 下游服务 `flowbench-backend`
```bash
./bin/flowbench-backend \
  -addr="127.0.0.1:9200" \
  -io-timeout=5s \
  -debug-frame=false
```

### 2.2 核心被测服务 `flowbench-server` (SUT)
```bash
./bin/flowbench-server \
  -addr="127.0.0.1:9100" \
  -backend-addr="127.0.0.1:9200" \
  -conn-mode="short" \
  -deterministic=true \
  -fault-mutation="" \
  -debug-frame=false
```
- `-deterministic=true|false`：控制响应时间戳与 `trace_tag` 是否确定性生成；
- `-fault-mutation="discount_bug"`：人为注入折扣多减 100 分的回归 Bug，验证回放测试能否精准报错拦截。

### 2.3 流量发起与断言端 `flowbench-client`
```bash
# 执行全套 6 大标准场景 (SC-01 ~ SC-05)
./bin/flowbench-client -server-addr="127.0.0.1:9100" -mode=suite -conn-mode=short

# 执行 TCP 半包分片 (3段拆分) 与多帧粘包 (3帧合并单次Write) 专项测试
./bin/flowbench-client -server-addr="127.0.0.1:9100" -mode=framing

# 开启 24B 二进制帧头与 Protobuf 载荷 Hex Dump 调试打印
./bin/flowbench-client -server-addr="127.0.0.1:9100" -mode=quote -debug-frame=true
```

---

## 3. Keploy eBPF 流量录制与离线回放 SOP (基于 Keploy v3.6.163 实机验证)

### 3.1 环境准备（安装 Keploy CLI）
在具备 Linux Kernel $\ge 5.10$（当前验证环境：`Linux 6.8.0-147-generic x86_64`，开启 `/sys/kernel/btf/vmlinux`）的环境中安装 Keploy OSS CLI：
```bash
curl --silent -O -L https://keploy.io/install.sh && source install.sh --oss
```

### 3.2 核心实证发现：Keploy 对自定义二进制协议的 Ingress / Egress 支持边界
在 `Keploy 3.6.163` 上的实机测试验证了以下关键结论：
1. **Ingress Proxy (`keploy record / test`) 的 HTTP/1.1 限制**：
   - `keploy record` 会通过 eBPF `bind` 钩子将 `flowbench-server` 的监听端口（`9100`）重映射至随机端口，并启动 `Using request-framed HTTP/1.1 proxy with MSG_PEEK pool liveness` 接管 `9100`。
   - 当 `flowbench-client` 发送 `0xFB01` 魔数开头的自定义二进制帧时，Keploy Ingress Proxy 因强依赖 HTTP/1.1 解析而报错：`Failed to parse downstream HTTP request: malformed HTTP request "\xfb\x01..."`，并直接向客户端返回 `HTTP/1.1 400 Bad Request`（首两字节为 `0x4854` 即 `"HT"`）。
2. **Egress Proxy (`keploy mock record / replay`) 的 Generic TCP Binary 完美支持**：
   - Keploy 的 Egress Proxy（`:16789`）内置了 `V2 generic parser`。当检测到 `Server -> Backend (127.0.0.1:9200)` 为非标准协议时，自动回退至 `generic` 解析器（`The external dependency is not supported. Hence using generic parser`），将 24B 帧头 + Protobuf 载荷完整录制为 Base64 二进制块（以 `+wEB` 开头，解码即为 `0xFB 0x01 0x01`）。
   - 使用 `keploy mock record` 与 `keploy mock replay` 时，Keploy 自动识别同一测试作业内的 `Client -> Server (127.0.0.1:9100)` 为 `self-call` 原生透传（绕过 HTTP-only Ingress Proxy），同时通过 eBPF 100% 劫持并回放 `Server -> Backend (127.0.0.1:9200)` 的自定义二进制流量。

### 3.3 步骤一：录制流量 (`Record Mode`)
执行封装好的录制脚本（自动启动真实 `Backend:9200` -> 通过 `keploy mock record` 启动 `Server:9100` 与 `Client -mode=suite` -> 自动清理后台进程）：
```bash
make keploy-record
# 或直接调用脚本:
./scripts/keploy-record.sh
```
录制完成后，Keploy 会在 [`keploy/keploy/flowbench-e2e/mocks.yaml`](file:///home/fugui/flowbench/keploy/keploy/flowbench-e2e/mocks.yaml) 下生成 4 条 `kind: Generic` 二进制 Mock（对应 `SC-02`、`SC-04`、`SC-04B`、`SC-05`；而 `SC-01` 纯入站与 `SC-03` 前置校验短路均为 0 次下游调用，不产生冗余 Mock）。

### 3.4 步骤二：关闭 `Backend` 执行离线回放验证 (`Offline Replay Mode`)
在**完全不启动 `flowbench-backend`** 的情况下，验证 Keploy eBPF 劫持 `Server -> 127.0.0.1:9200` 连接并返回二进制 Mock（`loaded: 4, consumed: 4, missed: 0`）：
```bash
make keploy-test
# 或直接调用脚本:
./scripts/keploy-test.sh
```

### 3.5 步骤三：验证回归突变拦截与非确定性对照实验 (`SC-06`)
```bash
# 1. 注入 Server 折扣计算 Bug (-fault-mutation=discount_bug)，预期回放检测出响应金额与 processing_digest 偏差并以 Exit Code 1 拦截失败
./scripts/keploy-test.sh --mutation

# 2. 开启非确定性时间戳 (-deterministic=false)，观测动态字段对确定性摘要与回放断言的影响
./scripts/keploy-test.sh --non-deterministic
```
