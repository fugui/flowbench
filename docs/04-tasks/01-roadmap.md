# flowbench 工程实施路线图与验收总则 (Implementation Roadmap)

- **文档编号**：`01-roadmap.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-25
- **当前阶段**：阶段三：工程实施规划与验收总则
- **状态**：已定稿 (Finalized)

---

## 1. 工程实施总则与递进策略 (Execution Strategy)

基于已冻结的二进制帧协议契约（[`docs/03-api/01-binary-frame-spec.md`](file:///home/fugui/flowbench/docs/03-api/01-binary-frame-spec.md)）与 Protobuf 消息契约（[`docs/03-api/02-protobuf-messages.md`](file:///home/fugui/flowbench/docs/03-api/02-protobuf-messages.md)），工程实施阶段（阶段四）拆分为 **4 个正交递进的里程碑（Milestone 1 ~ Milestone 4）**。每个里程碑均遵循 **TDD（测试驱动与即时验证）** 原则，前一个 Milestone 的单元测试与静态检查 100% 通过后，方可推进至下一 Milestone。

---

## 2. Milestone 依赖拓扑图 (Milestone DAG)

```mermaid
flowchart LR
    M1["Milestone 1: 协议与编解码底座<br/>(proto / internal/protocol / internal/codec)<br/>24B 帧头 + Protobuf + 粘包拆帧单测"]
    M2["Milestone 2: 传输层与双服务端实现<br/>(internal/transport / backend / server)<br/>Backend 数据底座 + Server 四步加工引擎"]
    M3["Milestone 3: Client 驱动与全链路 E2E<br/>(cmd/client / tests/e2e_test.go / scripts)<br/>6 大场景拨测 + 突变检测 + 确定性验证"]
    M4["Milestone 4: 离线回放与自愈断言验证<br/>(tests/replay_verify_test.go / data)<br/>进程内透明代理录制 + 无 Backend 离线 Mock 回放"]
    M5["Milestone 5: 原生 cilium/ebpf 监控与 Web 控制台<br/>(internal/ebpf / internal/monitor / cmd/monitor)<br/>内核透明劫持 + 24B/Protobuf解剖 + 实时控制台"]
    M6["Milestone 6: 通用私有 Protobuf 协议平台演进<br/>(FramingSpec DSL / 纯 Go .proto 反射 / /proc 探测 / TargetPID 过滤)"]
    M7["Milestone 7: 多下游 Backend 路由劫持与 Mock 隔离<br/>(eBPF 多目标 HASH / 动态代理池 / (Backend, OpCode) 联合 Mock / Web 多后端)"]
    M8["Milestone 8: 工业级复合二进制协议支持<br/>(内置 CROS RPC 编解码 / 24B+252B复合拆包 / 微服务契约反射 / ullSNO 原地自愈)"]
    M9["Milestone 9: 手工录制监控、动态拓扑与单调用自愈重放<br/>(交互式会话启停 / 端到端事务树 / SVG流动拓扑 / 单调用原地重放与Diff)"]

    M1 ==> M2 ==> M3 ==> M4 ==> M5 ==> M6 ==> M7 ==> M8 ==> M9
```

---

## 3. 里程碑全景矩阵与交付物清单

| 里程碑 | 作战卡文档 | 核心交付代码与资产 | 自动化验收门禁 (Quality Gate) |
| :--- | :--- | :--- | :--- |
| **M1：协议与编解码底座** | [`02-m1-protocol-and-codec.md`](file:///home/fugui/flowbench/docs/04-tasks/02-m1-protocol-and-codec.md) | `go.mod`、`proto/flowbench/v1/messages.proto`、`internal/protocol/pb/messages.pb.go`、`internal/protocol/marshal.go`、`internal/codec/frame.go`、`internal/codec/stream.go`、`internal/codec/frame_test.go` | `go test -v -race ./internal/codec/... ./internal/protocol/...` 100% 通过（覆盖单字节半包、10帧粘包、CRC32篡改、超大帧拦截、确定性 Marshal） |
| **M2：传输层与双服务端** | [`03-m2-backend-and-server.md`](file:///home/fugui/flowbench/docs/04-tasks/03-m2-backend-and-server.md) | `internal/transport/{server,client}.go`、`internal/backend/{store,handler,handler_test}.go`、`internal/server/{config,processor,handler,processor_test}.go`、`cmd/backend/main.go`、`cmd/server/main.go` | `go build ./cmd/backend ./cmd/server` 成功；`go test -v -race ./internal/...` 100% 通过 |
| **M3：Client 驱动与全链路 E2E** | [`04-m3-client-and-e2e.md`](file:///home/fugui/flowbench/docs/04-tasks/04-m3-client-and-e2e.md) | `cmd/client/main.go`、`tests/e2e_test.go`、`Makefile`、`scripts/verify-e2e.sh` | `go test -v -race ./...` 全绿；`./scripts/verify-e2e.sh` 跑通 `SC-01`~`SC-06` 全部场景且自动清理后台进程 |
| **M4：离线回放与自愈断言验证** | `tests/replay_verify_test.go` | `tests/replay_verify_test.go`、`data/native-ebpf-assets.json` | 进程内透明录制、出站 Mock 劫持、无 Backend 离线回放与回归突变精确拦截 |
| **M5：原生 eBPF 协议感知监控** | [`06-m5-ebpf-monitor-and-console.md`](file:///home/fugui/flowbench/docs/04-tasks/06-m5-ebpf-monitor-and-console.md) | `internal/ebpf/`、`internal/monitor/`、`cmd/monitor/`、`scripts/start-monitor.sh` | 内核态 `cgroup/connect4` 透明劫持、动态 RequestID 自愈、字段级 Diff 诊断与安全 Web 控制台 |
| **M6：通用私有 Protobuf 协议平台** | [`07-m6-universal-protobuf-platform.md`](file:///home/fugui/flowbench/docs/04-tasks/07-m6-universal-protobuf-platform.md) | `internal/codec/dynamic.go`、`internal/protocol/schema/`、`internal/monitor/proc.go`、`internal/ebpf/`、`internal/monitor/` | 声明式 `FramingSpec` DSL 拆帧、零依赖 `.proto` 动态反射、`/proc` 进程扫描与 `TargetPID` 精准拦截 |
| **M7：多下游 Backend 路由劫持与 Mock 隔离** | [`08-m7-multi-backend-routing.md`](file:///home/fugui/flowbench/docs/04-tasks/08-m7-multi-backend-routing.md) | `internal/ebpf/`、`internal/monitor/platform.go`、`internal/monitor/engine.go`、`internal/monitor/web/`、`tests/multi_backend_test.go` | eBPF 多目标 HASH 重定向、动态代理池、`(TargetBackend, OpCode)` 联合索引隔离与同机多端口/跨机多后端集成测试 |
| **M8：工业级复合二进制协议支持** | [`09-m8-composite-protocol-cros-rpc.md`](file:///home/fugui/flowbench/docs/04-tasks/09-m8-composite-protocol-cros-rpc.md) | `internal/codec/cros/`、`internal/codec/dynamic.go`、`internal/monitor/inspector.go`、`tests/cros_protocol_test.go` | 内置 CROS RPC 编解码器、24B+252B 复合拆包、基于契约的 Protobuf 动态反射与零开销序列号自愈 |
| **M9：手工录制、动态拓扑与单调用自愈重放** | [`10-m9-interactive-session-and-replay.md`](file:///home/fugui/flowbench/docs/04-tasks/10-m9-interactive-session-and-replay.md) | `internal/monitor/session.go`、`internal/monitor/correlator.go`、`internal/monitor/replayer.go`、`internal/monitor/web/` | 交互式会话启停、端到端事务关联、SVG 动态流动拓扑画布、单调用原地自愈重放与字段级 Diff |

---

## 4. 全局质量门禁红线 (Mandatory Quality Gates)

1. **零 gRPC 依赖验证**：`grep -i "grpc" go.mod` 必须为空，仅依赖 `google.golang.org/protobuf`。
2. **静态检查与竞态零容忍**：每次提交前必须通过 `go fmt ./...`、`go vet ./...` 与 `go test -race ./...`。
3. **端口与进程卫生**：所有服务端默认绑定 `127.0.0.1`；任何自动化测试或脚本结束后，`ps aux | grep flowbench-` 必须无残留进程。
