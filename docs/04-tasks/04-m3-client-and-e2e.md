# Milestone 3 作战卡：Client 多模式流量驱动器与三端全链路 E2E 测试

- **文档编号**：`04-m3-client-and-e2e.md`
- **归属阶段**：阶段三/四：M3 任务拆解与验收标准
- **前置依赖**：Milestone 2（`internal/transport`, `internal/backend`, `internal/server`）

---

## 1. M3 实施目标

实现 `cmd/client` 多模式流量发生器与响应断言器，编写覆盖 `SC-01` ~ `SC-06` 全场景、粘包分片压测、逻辑突变检测与确定性比对的 `tests/e2e_test.go` 端到端集成测试，并交付 `Makefile` 与 `scripts/verify-e2e.sh` 一键联调脚本。

---

## 2. 详细任务拆解 (Tasks Breakdown)

### Task 3.1：实现 `Client` 多模式流量驱动器 (`cmd/client/main.go`)
1. 支持命令行参数：
   - `-server-addr`：默认 `127.0.0.1:9100`
   - `-mode`：`ping` | `quote` | `credit` | `suite` | `framing`（默认 `suite`）
   - `-conn-mode`：`short` | `keepalive`（默认 `short`）
   - `-debug-frame`：打印收发帧头与载荷十六进制摘要
2. 实现 `-mode=suite` 标准 6 大场景执行序列（固定 `RequestID = 1001 .. 1006`）：
   - 对每个场景的响应执行严格的业务状态码、金额加工字段、`risk_decision` 与 `processing_digest` 非空校验；
   - 终端打印结构化场景验证汇总表（PASS / FAIL），任一断言失败则以非零状态码退出。
3. 实现 `-mode=framing` 粘包与半包专项测试：
   - 建立原生 TCP 连接，先将 1 个 `OrderQuoteRequest` 帧拆成 3 段分片（5B + 19B + Payload）发送并验证响应；
   - 再将 3 个请求帧拼接在单次 `conn.Write` 中粘连发送，连续调用 3 次 `ReadFrame` 验证 `Server` 准确返回全部 3 个响应。

### Task 3.2：编写 Go 原生三端全链路 E2E 集成测试 (`tests/e2e_test.go`)
在测试进程内使用随机空闲环回端口（`127.0.0.1:0`）拉起真实 `Backend` 与 `Server` 实例，验证：
1. **E2E-01 全场景套件测试 (`TestEndToEndSuite_ShortAndKeepAlive`)**：在 `short` 与 `keepalive` 两种连接模式下完整跑通 `SC-01` ~ `SC-05`；
2. **E2E-02 前置校验短路零下游调用证明 (`TestPreValidationShortCircuit`)**：发送非法 `quantity=0`，断言 `Backend` 请求计数器增加 `0`；
3. **E2E-03 确定性字节级一致性测试 (`TestDeterministicByteIdentity`)**：同一请求连续调用两次，断言返回的完整 24B+Payload 二进制切片（含 `CRC32`）`bytes.Equal` 100% 相等；而在 `Deterministic=false` 下两次响应字节流不同；
4. **E2E-04 业务加工突变检出测试 (`TestFaultMutationDetection`)**：对比正常 `Server` 与开启 `FaultMutation="discount_bug"` 的 `Server`，断言响应 `discount_cents`、`final_amount_cents`、`processing_digest` 与帧头 `CRC32` 均发生可检出变化；
5. **E2E-05 TCP 粘包与半包压力测试 (`TestTCPFramingCoalescingAndFragmentation`)**。

### Task 3.3：编写 `Makefile` 与 `scripts/verify-e2e.sh`
1. `Makefile` 提供 `build`、`test`、`vet`、`fmt`、`verify-e2e`、`keploy-record`、`keploy-test`、`clean` 目标。
2. `scripts/verify-e2e.sh`：编译三端二进制 -> 后台启动 `Backend (127.0.0.1:9200)` 与 `Server (127.0.0.1:9100)` -> 运行 `Client -mode=suite` 与 `Client -mode=framing` -> `trap cleanup EXIT` 彻底回收后台进程。

---

## 3. 验收标准 (Acceptance Criteria)

- [ ] **AC-M3-01**：`go test -v -race ./...` 全量单元与 E2E 集成测试 100% 通过。
- [ ] **AC-M3-02**：执行 `./scripts/verify-e2e.sh` 输出全部场景 PASS，且脚本退出后无任何 `flowbench-server` 或 `flowbench-backend` 进程残留。
