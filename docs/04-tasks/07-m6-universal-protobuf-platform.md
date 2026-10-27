# Milestone 6 作战卡：通用私有 Protobuf 协议监听与自愈回放工具平台

- **文档编号**：`07-m6-universal-protobuf-platform.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-27
- **归属阶段**：阶段三 & 四（平台化演进）：任务拆解与实施验收标准

---

## 1. 里程碑目标 (Milestone Goal)

基于 [`docs/01-features/03-universal-protobuf-platform.md`](file:///home/fugui/flowbench/docs/01-features/03-universal-protobuf-platform.md) (v1.1.0) 与 [`docs/02-design/06-universal-protobuf-platform-arch.md`](file:///home/fugui/flowbench/docs/02-design/06-universal-protobuf-platform-arch.md)，将 `flowbench-monitor` 升级为通用、独立、可扩展的私有 Protobuf 协议监听与自愈回放工具平台，交付：
1. **声明式帧头 DSL 引擎 (`internal/codec/dynamic.go`)**：支持任意字节序、魔数、长度公式、序列号与多算法 Checksum 的动态拆粘包与自愈重算，内置 3 套主流协议模板；
2. **零外部依赖动态 Schema 注册表 (`internal/protocol/schema/`)**：内置纯 Go Proto3 词法/语法解析器，配合官方 `protodesc` + `dynamicpb` 实现 `.proto` 在线导入、`(OpCode, Direction)` 联合路由、JSON <-> Binary 双向反射及无 Schema `protowire` Tag 树优雅降级与历史帧原地重激活；
3. **OS 级 `/proc` 进程探测器与 `TargetPID` 精准 eBPF 探针 (`internal/monitor/proc.go` & `internal/ebpf/interceptor.go`)**：零外部命令扫描系统进程与监听端口，内核态支持 `TargetPID` 精准过滤与远端 IPv4 下游地址跨 IP 重写；
4. **通用平台 Web 工作台 (`internal/monitor/server.go` & `internal/monitor/web/`)**：提供进程中心、协议与 `.proto` 导入工作台、实时多流解剖器、Mock JSON 在线编辑重编码与内置沙箱演练场。

---

## 2. 任务分解清单 (Task Breakdown)

| 任务 ID | 任务名称 | 交付产物路径 | 核心实施要求 |
| :--- | :--- | :--- | :--- |
| **M6-T1** | 声明式帧头 DSL 与动态流式拆粘包状态机 | `internal/codec/dynamic.go`<br>`internal/codec/dynamic_test.go` | 1. 实现 `FramingSpec`、`DynamicFrame`、`ReadDynamicFrame`、`WriteDynamicFrame`、`RebuildDynamicFrame`；<br>2. 实现标准长度换算公式（`payload_only` / `total_frame` + `bias`）与越界保护；<br>3. 支持 `crc32_ieee`、`crc32_castagnoli`、`adler32` 及 `zero_fill` / `skip_bytes` 全帧校验和自愈；<br>4. 内置 `flowbench-v1` (24B)、`generic-financial-v2` (20B)、`gaming-sync-v1` (12B) 三套预置协议模板。 |
| **M6-T2** | 纯 Go `.proto` 动态解析器与 `dynamicpb` 反射注册中心 | `internal/protocol/schema/parser.go`<br>`internal/protocol/schema/registry.go`<br>`internal/protocol/schema/schema_test.go` | 1. 实现零 `protoc` 依赖的纯 Go Proto3 源码解析器，生成 `descriptorpb.FileDescriptorProto` 并构建 `protodesc` 描述符池；<br>2. 实现 `(OpCode, Direction: REQ\|RESP)` 二元组路由契约与 `dynamicpb` JSON <-> Wire 双向转换；<br>3. 实现无 Schema 场景下基于 `protowire` 的 Tag 树降级解析，以及补传 `.proto` 后的历史帧原地重解析激活。 |
| **M6-T3** | OS 级 `/proc` 进程发现引擎与 `TargetPID` / 跨 IP eBPF 探针升级 | `internal/monitor/proc.go`<br>`internal/ebpf/interceptor.go`<br>`internal/ebpf/interceptor_test.go` | 1. 纯 Go 读取 `/proc/[pid]/comm`, `cmdline`, `cgroup`, `fd`, `/proc/net/tcp` 枚举系统进程与监听/出站端口；<br>2. 扩展 `BPFState` 至 96 字节，新增 `TargetPID` 与 `EgressOrigIPNet`；<br>3. 升级 `buildConnect4Instructions` 支持按 `TargetPID` 精准过滤出站连接并将远端下游 IPv4 重写至 `127.0.0.1:EgressProxyPort`。 |
| **M6-T4** | 通用平台引擎整合与安全 Web 工作台升级 | `internal/monitor/engine.go`<br>`internal/monitor/server.go`<br>`internal/monitor/web/index.html`<br>`internal/monitor/web/app.js`<br>`internal/monitor/web/styles.css` | 1. 引擎集成 `FramingSpec`、`SchemaRegistry`、动态进程 Attach/Detach、历史帧重激活与 Mock JSON 在线编辑重编码；<br>2. 新增 `/api/processes`、`/api/attach`、`/api/detach`、`/api/protocol/spec`、`/api/protocol/schema`、`/api/mocks/update` 接口；<br>3. 前端严格采用 `createElement`/`textContent` 安全 DOM 渲染（零 `innerHTML`、零 `alert`、零 `console.log`）。 |

---

## 3. 自动化验收标准 (Acceptance Criteria)

- **AC-M6-01 (多协议 DSL 拆粘包与 Checksum 自愈)**：`go test -v -race ./internal/codec/...` 100% 通过，验证 `flowbench-v1`、`generic-financial-v2`（20B 大端 `total_frame` + 全帧 Castagnoli CRC32 `zero_fill`）、`gaming-sync-v1`（12B 小端 + Adler32）在单字节半包、多帧粘包及序列号/载荷改写后的自愈重算全部精确无误。
- **AC-M6-02 (零依赖 `.proto` 导入、`dynamicpb` 反射与无 Schema 降级重激活)**：`go test -v -race ./internal/protocol/...` 100% 通过，验证不依赖外部 `protoc` 即可在线解析自定义 `.proto` 文本，完成 `(OpCode, Direction)` 路由与 JSON <-> Binary 互转；验证先在无 Schema 下抓包输出 Tag 树，随后导入 `.proto` 即刻将历史报文原地刷新为业务明文 JSON。
- **AC-M6-03 (`/proc` 进程探测与 `TargetPID` 内核精准过滤)**：验证 `/proc` 探测器准确识别正在运行的监听进程及端口；在内核 eBPF 模式下设置 `TargetPID` 与自定义下游 IP:Port，验证仅 `TargetPID` 发起的连接被精准劫持，非目标进程 100% 豁免。
- **AC-M6-04 (Web 工作台 API 闭环与零回归)**：全量测试 `go test -v -race ./...` 与 `./scripts/verify-e2e.sh` 100% 通过；Web 前端静态安全检查零 `innerHTML` / `alert` / `console.log`。
