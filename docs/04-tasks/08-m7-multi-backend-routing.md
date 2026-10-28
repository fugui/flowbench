# Milestone 7 作战卡：多下游 Backend 路由劫持与 Mock 隔离

- **文档编号**：`08-m7-multi-backend-routing.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-27
- **归属阶段**：阶段三（任务拆解与验收标准）
- **状态**：已定稿并全部通过验证 (Finalized & Verified)

---

## 1. 里程碑目标 (Milestone Goal)

基于 [`docs/01-features/04-multi-backend-routing.md`](file:///home/fugui/flowbench/docs/01-features/04-multi-backend-routing.md) 与 [`docs/02-design/07-multi-backend-routing-arch.md`](file:///home/fugui/flowbench/docs/02-design/07-multi-backend-routing-arch.md)，全面落地被测服务（SUT）“单入口监听、多下游依赖”（同一主机多个不同端口 + 少量跨机远端 IP）的流量治理能力：
1. **eBPF 多目标 HASH Map 与精准分流探针**：在内核层实现对任意多个下游 `(DestIP, DestPort)` 的无锁哈希查找与重定向，彻底解除单一目标限制；
2. **多路 Egress 代理池与生命周期管理**：按需动态分配本地回环代理端口，透明穿透或本地隔离；
3. **`(BackendAddr, OpCode)` 联合索引 Mock 引擎**：实现微服务群多 Backend 间的 Mock 资产隔离与动态 RequestID / 校验和自愈；
4. **Web 多下游管理控制台与全链路 E2E 验证**：前端支持多后端动态增删改查与分组可视化，测试套件端到端验证。

---

## 2. 任务分解清单 (Task Breakdown)

| 任务 ID | 任务名称 | 交付产物路径 | 核心实施要求 |
| :--- | :--- | :--- | :--- |
| **M7-T1** | 内核态 eBPF 多目标 HASH 规则表升级 | `internal/ebpf/interceptor.go`<br>`internal/ebpf/interceptor_test.go` | 1. 引入 `BPF_MAP_TYPE_HASH` 命名的 `egress_targets`，Key 为 8 字节 `(DestIP, DestPort)`，Val 为 4 字节 `ProxyPort`；<br>2. 升级 `buildConnect4Instructions` 在 `TargetPID` 过滤后查表匹配，命中时改写目标为 `127.0.0.1:ProxyPort`；<br>3. 提供 `AddEgressTarget`、`RemoveEgressTarget`、`ListEgressTargets` 热更新接口。 |
| **M7-T2** | 多下游 Egress 代理池与动态路由引擎 | `internal/monitor/platform.go`<br>`internal/monitor/engine.go` | 1. 实现 `BackendRouter`，维护多个独立的 TCP 代理监听器（`ProxyPort_i -> Host_i:Port_i`）；<br>2. 支持在线动态 `AddBackend` / `RemoveBackend`，自动完成端口分配与内核 BPF Map 同步；<br>3. 兼容用户态 Fallback 模式平滑降级。 |
| **M7-T3** | 多 Backend 联合键 Mock 隔离与自愈回放 | `internal/monitor/inspector.go`<br>`internal/monitor/engine.go`<br>`data/native-ebpf-assets.json` | 1. 流量帧解剖与 Mock 资产数据结构新增 `TargetBackend`（格式 `host:port`）与 `BackendName`；<br>2. 离线回放匹配器升级为 `(TargetBackend, OpCode)` 联合键检索；<br>3. 保持动态 RequestID 与 CRC32/Adler32 校验和全自动重算自愈。 |
| **M7-T4** | Web 工作台多后端管理与端到端集成测试 | `internal/monitor/web/`<br>`internal/monitor/server.go`<br>`tests/multi_backend_test.go` | 1. 扩展 Web 附着面板与 API（`/api/backends`、`/api/attach`），支持前端动态增删多个下游 Backend；<br>2. 流量列表增加 Backend 标签与分组折叠；<br>3. 编写 `TestMultiBackend_SameHostMultiPortsAndRemoteIP` 全链路集成测试，验证同机多端口（如 19201, 19202）及跨机 IP 回放隔离。 |

---

## 3. 自动化验收标准 (Acceptance Criteria, AC)

- **AC-M7-01 (eBPF 多目标 HASH 查找与精准重定向)**：`go test -v -race ./internal/ebpf/...` 100% 通过；在内核模式下注册 3 个不同下游（`127.0.0.1:19201`、`127.0.0.1:19202`、`192.168.1.50:19204`），验证向其发起的 `connect()` 全部精准重定向至各自对应的代理端口，未注册的端口 100% 豁免。
- **AC-M7-02 (动态代理池与生命周期)**：动态添加/删除 Backend 规则，代理端口与 BPF Map 实时生效，无 Goroutine 或 Socket 泄漏。
- **AC-M7-03 (同 OpCode 跨 Backend Mock 隔离与自愈)**：当 Backend 1 与 Backend 2 具有相同 `OpCode=0x0101` 但业务明文不同的请求时，离线回放命中各自正确的 Mock 数据，校验和自愈无误，断言 100% 准确。
- **AC-M7-04 (零回归与全量套件全绿)**：`go test -v -race ./...` 与 `./scripts/verify-e2e.sh` 100% 通过，Web 控制台安全检查零 `innerHTML`/`alert`/`console.log`。
