# flowbench 文档知识库 (Docs Hub)

本项目严格采用 **SDD (Spec-Driven Development，规范驱动开发)** 模式，所有提示词资产、需求边界、系统架构、自定义二进制协议契约、任务拆解与验证手册均在此目录中进行统一管理与版本控制。

---

## 目录分层架构

```text
docs/
├── 00-prompt/       # 提示词库：记录各阶段 SDD Prompt 资产、初始化指令与演进提示词
│   ├── 01-init.md   # 系统初始化与阶段一（生成 Feature Matrix）指令提示词
│   ├── 02-design-prompt.md # 阶段二系统架构设计 (SDD) 提示词指令
│   ├── 03-api-and-tasks-prompt.md # 阶段三协议契约与工程任务分解提示词指令
│   └── 04-implementation-prompt.md # 阶段四工程实施与全链路交付验证提示词指令
├── 01-features/     # 特性与需求边界：特性清单矩阵 (Feature Matrix)、P0~P2 优先级与 Non-Goals
│   ├── 01-feature-matrix.md # 阶段一需求范围与特性矩阵规范 (已定稿)
│   ├── 02-ebpf-native-monitor-and-console.md # 阶段一（演进篇）：原生 cilium/ebpf 协议感知监控与实时 Web 控制台特性规范
│   └── 03-universal-protobuf-platform.md     # 阶段一（平台化演进）：独立、通用、可扩展的私有 Protobuf 协议监听与自愈回放工具平台特性规范
├── 02-design/       # 系统技术架构 (SDD)：三端架构、自定义二进制协议栈、业务编排与原生 eBPF 治理
│   ├── 01-system-architecture.md     # 【总览篇】系统总体架构与模块分层设计
│   ├── 02-wire-protocol-and-codec.md # 【协议篇】自定义二进制帧协议与拆粘包状态机设计
│   ├── 03-service-orchestration.md   # 【业务篇】三端协同与 Server 业务加工编排设计
│   └── 05-ebpf-native-monitor-and-console.md # 【核心篇】原生 cilium/ebpf 协议感知监控与实时 Web 控制台架构设计
├── 03-api/          # 协议与接口契约：自定义二进制帧头规范 (Wire Format) 与 Protobuf 消息字典
│   ├── 01-binary-frame-spec.md       # 【帧协议契约】24B 二进制帧头字节级契约与 Go 接口签名
│   └── 02-protobuf-messages.md       # 【消息契约】Protobuf 消息 Schema 与 OpCode 路由映射
├── 04-tasks/        # 实施规划与验收：Milestone 拆解、任务作战卡与自动化验收测试标准 (AC)
│   ├── 01-roadmap.md                 # 【工程路线图】Milestone 1~5 依赖拓扑与质量门禁
│   ├── 02-m1-protocol-and-codec.md   # 【M1 作战卡】自定义二进制协议与编解码器
│   ├── 03-m2-backend-and-server.md   # 【M2 作战卡】TCP 传输层、Backend 与 Server 业务加工引擎
│   ├── 04-m3-client-and-e2e.md       # 【M3 作战卡】Client 多模式驱动与三端全链路 E2E 测试
│   └── 06-m5-ebpf-monitor-and-console.md # 【M5 作战卡】原生 cilium/ebpf 监控、智能回放与实时 Web 控制台
├── 05-adr/          # 架构决策记录 (ADR)：重大技术选型与权衡记录 (Architecture Decision Records)
│   └── 01-custom-binary-frame-and-determinism.md # ADR-01: 24B 二进制帧设计与确定性回放控制
└── 06-ops/          # 交付与验证：原生 eBPF 监控环境准备、流量录制/回放操作手册与观测报告
    └── 01-ebpf-monitor-ops-guide.md # 三端联调与原生 eBPF 监控回放实操指南
```

---

## 目录治理规则

1. **全链路双位编号递进**：
   - 一级子目录采用阶段双位数字前缀（`00-` ~ `06-`）；
   - 各阶段目录下的**所有 Markdown 文档必须带双位数字编号前缀**（如 `01-feature-matrix.md`、`01-system-architecture.md`、`01-binary-frame-spec.md`、`01-roadmap.md`），保证在文件树、IDE 及构建脚本中保持严格时序与逻辑有序性。
2. **规范先行**：任何业务代码实施前，必须确保 `01-features/`、`02-design/`、`03-api/` 与 `04-tasks/` 中有明确对应的设计契约与验收标准。
3. **决策可溯**：凡涉及关键技术选型（如自定义二进制帧结构设计、TCP 连接复用策略、Server 侧非确定性因子隔离、原生 eBPF 重定向设计），须在 `05-adr/` 中以标准 ADR 模板记录背景与决策理由。
