# flowbench: 基于 SDD 模式的自定义 Protobuf 二进制协议开发指令 (历史归档)

> [!NOTE]
> 本文档为项目初期演进的历史提示词归档。随项目演进与实机测试验证，本项目已全面抛弃通用 Keploy 工具，转为基于原生 `cilium/ebpf` 的协议感知流量监控与自愈回放系统。

## 角色定位与工程原则
你是一名资深 Go 分布式系统架构师、网络协议专家与 eBPF 流量录制回放（Keploy）测试专家。我们将采用 **规范驱动开发 (Spec-Driven Development, SDD)** 的工程方法，共同开发一个用于验证 [Keploy](https://github.com/keploy/keploy) 流量检测、录制（Record）与回放（Test/Replay）能力的基准测试系统（代号：**flowbench**）。

开发过程**严禁直接跳过设计全量堆砌业务代码**，必须严格遵循：
**「文档规范先行 -> 架构设计定稿 -> 协议契约明晰 -> 任务拆解与验收清单 -> 分阶段实施与验证」** 的开发流水线。

---

## 1. 核心业务背景与技术约束

本项目的核心目标是构建一个真实可控、具有代表性的**非 gRPC、非 HTTP 自定义二进制协议**微服务调用链，用以检验和观测 Keploy 对自定义 TCP 二进制流量的拦截、录制、Mock 生成及回放比对能力：

1. **三级微服务/子系统架构（Go 语言实现）**：
   - **`Client`（客户端/流量发起端）**：向 `Server` 发起单次或批量的业务调用请求，校验响应结果并输出统计报告。
   - **`Server`（核心被测系统 SUT）**：接收 `Client` 的请求，执行前置参数校验与业务预处理，随后作为客户端继续调用下游 `Backend`；收到 `Backend` 响应后，在 `Server` 侧进行核心业务加工处理（如数据富化、分润/折扣重算、风险等级评估、摘要签名计算等），最后将加工后的结果返回给 `Client`。
   - **`Backend`（下游依赖服务端）**：接收 `Server` 的调用请求，提供底层基础数据查询或原子规则计算，并将结果返回给 `Server`。
2. **自定义 Protobuf 二进制协议（严禁使用 gRPC）**：
   - 全链路（`Client -> Server` 以及 `Server -> Backend`）**不使用 gRPC，也不使用 HTTP/JSON**。
   - 统一基于原生 **TCP Socket** + **自定义二进制帧协议（Custom Wire Framing：含 Magic Bytes、协议版本、消息类型 OpCode、标志位 Flags、请求追踪 RequestID、负载长度 Payload Length、CRC32 校验和）** + **Protobuf 二进制序列化载荷（`google.golang.org/protobuf/proto`）** 进行通信。
3. **Keploy 流量检测与监控验证**：
   - 将 `Server` 作为 Keploy 监控与录制的目标对象（System Under Test）：
     - **录制阶段（Record Mode）**：Keploy 通过 eBPF 捕获 `Client -> Server` 的入站（Ingress）TCP 二进制流量生成测试用例，同时捕获 `Server -> Backend` 的出站（Egress）TCP 二进制流量生成下游 Mock。
     - **测试回放阶段（Test Mode）**：在关闭真实 `Backend` 的情况下，由 Keploy 重放 `Client -> Server` 的入站二进制请求，并自动劫持 `Server -> Backend` 的出站调用返回录制的二进制 Mock，验证 `Server` 的业务加工逻辑与响应字节流是否与录制期一致。

---

## 2. 顶层目录与工程策略 (Top-Level Layout & Policies)

整个项目采用清晰的职责隔离，严格遵循以下一级目录与治理策略：

```text
flowbench/
├── docs/                # 规范文档库：统一管理 Prompt(00)、特性矩阵(01)、架构设计(02)、协议契约(03)、任务拆解(04)、ADR(05) 与运维手册(06)
├── proto/               # Protobuf 协议源定义：存放 .proto 文件（仅定义 Message，严禁定义 gRPC Service）
├── cmd/                 # 三大子系统可执行入口：cmd/client、cmd/server、cmd/backend
├── internal/            # 内部核心代码（具体子模块在阶段二设计文档中推导并确定）
├── keploy/              # Keploy 流量录制与回放资产库：配置、录制用例集与二进制 Mock 文件
├── scripts/             # 自动化编排脚本：编译、三端联动、Keploy 录制与回放一键验证脚本
└── tests/               # 集成与回归测试：二进制编解码测试、TCP 粘包/拆包测试、三端联调测试
```

- **规范先行策略**：在编写任何 `.go` 或 `.proto` 业务代码前，所有需求边界、帧格式设计与接口契约必须先落盘在 `docs/` 中。
- **双位数字编号策略**：`docs/` 下所有子目录（`00-prompt` ~ `06-ops`）及子目录内的所有 Markdown 文档，必须采用双位数字前缀编号（如 `01-feature-matrix.md`）。
- **按需细化策略**：`internal/`、`cmd/` 与 `scripts/` 内部的具体文件层级，在阶段二的技术架构设计中统一定义，不在阶段一做主观预设。

---

## 3. SDD 执行四大阶段

### 阶段一：特性清单与边界明确 (`docs/01-features/01-feature-matrix.md`)
- 输出详尽的需求功能矩阵，划分为以下五大核心领域：
  1. **自定义 Protobuf 二进制协议栈 (Custom Protobuf Wire Protocol & Codec)**：定长帧头设计、Magic/Version/OpCode/ReqID/CRC32 校验、TCP 粘包与半包流式拆帧、心跳与错误帧机制。
  2. **三端微服务与 Server 业务加工链路 (Three-Tier Services & Server Processing)**：`Client` 拨测与场景驱动、`Server` 承上启下与多维业务加工（确定性计算 vs 可控动态字段）、`Backend` 数据供給与故障注入模拟。
  3. **TCP 传输控制与高可靠通信 (TCP Transport & Reliability)**：连接管理、超时控制、请求-响应关联（Correlation ID）、优雅启停。
  4. **Keploy 流量录制、Mock 劫持与回放验证 (Keploy Traffic Detection & Replay)**：Record 模式入站/出站二进制捕获、Test 模式脱离 `Backend` 独立回放、确定性/非确定性（时间戳/随机 ID）对 Keploy 二进制回放影响的对比验证场景。
  5. **可观测性、自动化编排与诊断工具 (Observability & Automation)**：结构化日志（含帧头十六进制/字段摘要）、Makefile 与一键录制/回放验证脚本。
- 标明每个特性的优先级（`P0` 必须实现 / `P1` 重要增强 / `P2` 演进扩展）、核验方式，并明确定义 **Non-Goals（本项目当前阶段明确不做的功能，如不引入 gRPC、不引入外部数据库依赖、不做分布式服务注册中心等）**，严格控制系统复杂度与验证焦点。

### 阶段二：系统架构设计文档 (`docs/02-design/`)
- `01-system-architecture.md`：三端部署拓扑、进程模型、端到端请求生命周期与代码包分层结构。
- `02-wire-protocol-and-codec.md`：自定义二进制帧头位图（Bit/Byte Layout）、状态机拆包算法、Protobuf 负载映射与异常帧恢复机制。
- `03-service-orchestration.md`：`Client -> Server -> Backend` 详细处理流水线、`Server` 侧加工算法设计、确定性模式（Deterministic Mode）开关设计（用于对比 Keploy 在纯确定性与含动态字段下的二进制匹配行为）。
- `04-keploy-traffic-verification.md`：Keploy eBPF 拦截原理适配、录制/回放拓扑对比图、测试用例集验证矩阵。

### 阶段三：协议契约与任务分解 (`docs/03-api/`, `docs/04-tasks/`)
- 在 `docs/03-api/` 中冻结二进制帧字节序契约 (`01-binary-frame-spec.md`) 与 Protobuf 消息契约 (`02-protobuf-messages.md`)。
- 在 `docs/04-tasks/` 中将系统拆解为清晰的 Milestones（M1 协议与编解码器 -> M2 Backend 与 Server 核心链路 -> M3 Client 场景驱动与全链路联调 -> M4 Keploy 流量录制与回放验证闭环），每个 Milestone 配备明确的 **验收标准 (Acceptance Criteria)** 与自动化验证命令。

### 阶段四：工程实施与交付规范
- 严格按照设计契约落地 Go 代码与测试，确保 `go build ./...`、`go vet ./...`、`go test -race ./...` 全部零警告通过。
- 本地构建与测试通过后，自动提交并 push 至 GitHub 仓库。

---

## 🎯 初始执行指令（生成 Feature 阶段）
现在，请严格遵循上述规范，开始执行 **阶段一（第一步）**：
在 `docs/01-features/01-feature-matrix.md` 中输出详尽、结构化、可直接指导后续架构设计的**特性清单矩阵与范围界定文档 (Feature Matrix & Scope Definition)** 供我审阅。

**输出要求**：
1. 包含清晰的**项目愿景与验证目标**（聚焦自定义 Protobuf 二进制协议在三层微服务下的 Keploy 流量检测与录制回放验证）。
2. 包含按五大核心领域划分的**功能特性矩阵表格**（列包含：特性编号 `F-XX`、特性名称、详细技术描述、涉及子系统 `Client/Server/Backend/Keploy`、优先级 `P0/P1/P2`、验收验证方式）。
3. 专门设计 **Keploy 流量验证场景矩阵（Verification Scenarios）**，涵盖正常全链路录制回放、多消息类型（OpCode）、TCP 粘包/并发调用、以及 `Server` 侧确定性 vs 非确定性加工对二进制回放影响的对比场景。
4. 明确列出 **Non-Goals（非目标边界）** 与 **安全及环境约束（如仅绑定 `127.0.0.1`、零外部存储依赖等）**。
5. **在这一步，请不要创建任何 `.go` 或 `.proto` 业务代码，仅产出 `docs/01-features/01-feature-matrix.md`。**
