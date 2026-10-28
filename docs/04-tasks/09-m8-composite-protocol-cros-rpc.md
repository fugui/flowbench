# Milestone 8 作战卡：工业级复合二进制协议支持 —— 内置 CROS RPC 编解码与契约自愈回放 (M8 Card)

- **文档编号**：`09-m8-composite-protocol-cros-rpc.md`
- **里程碑标识**：`Milestone 8 (M8)`
- **编制日期**：2026-10-28
- **责任模块**：`internal/codec/cros/`、`internal/codec/`、`internal/monitor/`、`tests/`
- **状态**：进行中 (In Progress)

---

## 1. 里程碑实施目标 (Objectives)

在遵循 SDD 规范与零外部重型依赖的前提下，为 `flowbench` 注入工业级复合多级二进制协议支持，完整内置 **DOPHI CROS RPC 协议**的拆粘包状态机、会话头解剖、基于微服务契约（`ServiceName.Operation`）的 Protobuf 动态反射绑定与极速序列号自愈重写。

---

## 2. 任务清单分解 (Task Breakdown)

- [ ] **Task 8.1：CROS 协议常量与结构体定义 (`internal/codec/cros/cros.go`)**
  - 定义魔数、消息类型、底座常量与大端字节序读写辅助函数；
  - 定义 `CommHead` (24B)、`ReqHead` (252B)、`RspHead` (200B)、`ExtendCommHead` (4B) 与核心聚合结构 `Frame`。
- [ ] **Task 8.2：流式拆粘包状态机与构建器实现 (`internal/codec/cros/codec.go`)**
  - 实现 `ReadFrame(r io.Reader) (*Frame, error)`，严格防御半包、粘包与超大载荷（32MB 防 OOM 限制）；
  - 实现魔数扫描同步器 `ScanMagic(r io.Reader)`，在数据流存在前置脏数据时滑动查找 `"CSE.TCP"` 特征并恢复对齐；
  - 实现 `BuildReqFrame(...)` 与 `BuildRspFrame(...)` 帧封送构建器；
  - 实现 `RebuildFrameSeqID(raw []byte, newSNO uint64)` 原地极速自愈更新。
- [ ] **Task 8.3：流式与鲁棒性单元测试矩阵 (`internal/codec/cros/codec_test.go`)**
  - 单包编解码回环校验；
  - 单字节切片极端半包拆解测试（`fragReader`）；
  - 10 帧高密度粘包连读测试；
  - 垃圾字节滑动窗口定位恢复测试；
  - 内存原地重写序列号准确性与并发竞态检测（`-race`）。
- [ ] **Task 8.4：平台预置模板与事件解剖集成 (`internal/codec/dynamic.go`, `internal/monitor/inspector.go`)**
  - 在 `internal/codec/dynamic.go` 注册 `PresetCROSv1()` (`cros-rpc-v1`)；
  - 扩展 `DecodedHeaderInfo`，支持透传 CROS RPC 的 `ServiceName`、`Operation`、`StatusCode`、`MsgType`、`TraceID`；
  - 在平台动态解剖器中增加对 CROS RPC 复合报文的原生格式化。
- [ ] **Task 8.5：端到端集成测试与回放验证 (`tests/cros_protocol_test.go`)**
  - 模拟真实 Client 发送 CROS RPC Protobuf 请求，经由平台协议感知引擎解析为可读 JSON 并断言字段一致性；
  - 验证 Mock 响应报文序列号原地自愈与客户端成功认领。

---

## 3. 验收门禁 (Quality Gate & Acceptance Criteria)

1. **测试全绿**：`go test -v -race ./internal/codec/cros/... ./tests/...` 100% 通过；
2. **静态检查**：`go vet ./...` 零警告，代码严格符合 `AGENTS.md` 规范；
3. **性能保证**：序列号原地自愈函数内存分配必须为 `0 B/op`；
4. **交付规范**：代码检视无误，直接提交并推送至 `origin/main`。
