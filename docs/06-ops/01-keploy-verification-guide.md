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

## 3. Keploy eBPF 流量录制与离线回放 SOP

### 3.1 环境准备（安装 Keploy CLI）
在具备 Linux Kernel $\ge 5.10$（支持 BTF 与 eBPF）的环境中安装 Keploy：
```bash
curl --silent -O -L https://keploy.io/install.sh && source install.sh
```

### 3.2 步骤一：录制流量 (`Record Mode`)
执行封装好的录制脚本（自动启动 `Backend:9200` -> 通过 `keploy record` 启动 `Server:9100` -> 运行 `Client -mode=suite` 发起调用 -> 自动回收后台进程）：
```bash
make keploy-record
# 或直接调用脚本:
./scripts/keploy-record.sh
```
录制完成后，Keploy 会在 `./keploy/test-set-0/` 下生成：
- 入站测试用例（捕获 `Client -> Server:9100` 的 24B 帧头 + Protobuf 请求与响应）；
- `mocks.yaml`（捕获 `Server -> Backend:9200` 的出站 TCP 二进制查询帧与响应帧）。

### 3.3 步骤二：关闭 `Backend` 执行离线回放验证 (`Test Replay Mode`)
在**完全不启动 `flowbench-backend`** 的情况下，验证 Keploy eBPF 劫持 `Server -> 127.0.0.1:9200` 连接并返回二进制 Mock：
```bash
make keploy-test
# 或直接调用脚本:
./scripts/keploy-test.sh
```

### 3.4 步骤三：验证回归突变拦截与非确定性对照实验 (`SC-06`)
```bash
# 1. 注入 Server 折扣计算 Bug (-fault-mutation=discount_bug)，预期回放检测出二进制响应与 CRC32 偏差并判定 FAIL
./scripts/keploy-test.sh --mutation

# 2. 开启非确定性时间戳 (-deterministic=false)，观测动态字段对二进制帧字节比对的影响
./scripts/keploy-test.sh --non-deterministic
```
