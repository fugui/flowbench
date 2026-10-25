# flowbench: 阶段三协议契约与工程任务分解 (SDD) 提示词指令

## 角色定位与工程原则
你是一名资深 Go 分布式系统架构师、网络协议工程师与自动化测试专家。我们正在采用 **规范驱动开发 (Spec-Driven Development, SDD)** 模式开发 **flowbench（基于自定义 Protobuf 二进制协议的三级微服务与 Keploy 流量录制回放验证环境）**。

在阶段一特性矩阵（[`docs/01-features/01-feature-matrix.md`](file:///home/fugui/flowbench/docs/01-features/01-feature-matrix.md)）与阶段二系统架构设计规范（[`docs/02-design/`](file:///home/fugui/flowbench/docs/02-design/01-system-architecture.md) 01~04 四篇文档）全部定稿的基础上，现在正式进入：
**「阶段三：协议契约冻结与工程实施任务分解」**。

---

## 任务目标与交付文档矩阵 (Contract & Tasks Docs Matrix)

本阶段需要产出 **2 篇字节级/代码级协议契约文档**（位于 `docs/03-api/`）与 **5 篇工程实施路线图及 Milestone 作战卡**（位于 `docs/04-tasks/`）：

| 文档路径 | 规范类型 | 核心职责与交付要求 |
| :--- | :--- | :--- |
| `docs/03-api/01-binary-frame-spec.md` | **【帧协议契约】** | 冻结 24 字节二进制帧头的完整字节偏移字典、大端编码示例（Hex 字节对照表）、错误码映射表及编解码函数接口签名契约 |
| `docs/03-api/02-protobuf-messages.md` | **【消息契约】** | 冻结 `proto/flowbench/v1/messages.proto` 的完整 `.proto` 源文件定义（涵盖 Ping/Pong、OrderQuote、CreditEval、BackendOrderQuery、BackendCreditQuery、ErrorResponse 所有字段与编号），明确严禁引入 gRPC Service |
| `docs/04-tasks/01-roadmap.md` | **【工程路线图】** | 工程实施总览、Milestone 1 ~ 4 依赖拓扑图（DAG）、质量门禁红线与验收总则 |
| `docs/04-tasks/02-m1-protocol-and-codec.md` | **【M1 作战卡】** | Milestone 1：Go Module 初始化、Protobuf 消息编码封装、24B 二进制帧编解码器 (`internal/codec`) 与流式粘包/半包/CRC32 单元测试 |
| `docs/04-tasks/03-m2-backend-and-server.md` | **【M2 作战卡】** | Milestone 2：原生 TCP 传输层 (`internal/transport`)、`Backend` 确定性数据底座与服务实现、`Server` 前置校验/下游编排/四大业务加工引擎实现 |
| `docs/04-tasks/04-m3-client-and-e2e.md` | **【M3 作战卡】** | Milestone 3：`Client` 多模式驱动器 (`cmd/client`)、三端全链路自动化集成测试 (`tests/e2e_test.go`)、`Makefile` 与 `scripts/verify-e2e.sh` 联调脚本 |
| `docs/04-tasks/05-m4-keploy-verification.md` | **【M4 作战卡】** | Milestone 4：Keploy 环境配置 (`keploy/keploy.yml`)、`scripts/keploy-record.sh` 录制脚本、`scripts/keploy-test.sh` 离线回放脚本、突变检出与确定性对照实测报告 (`docs/06-ops/01-keploy-verification-guide.md`) |

> **红线提示**：本阶段聚焦于冻结二进制字节契约、Protobuf IDL 规范以及可执行的 Milestone 任务拆解，**暂不编写 `.go` 业务实现代码**。

---

## 🎯 执行指令
请严格按照上述规划，依次生成 `docs/03-api/` 下的 2 篇契约文档与 `docs/04-tasks/` 下的 5 篇路线图及 Milestone 作战卡，并同步更新 `README.md` 与 `docs/README.md` 的阶段进度索引。
