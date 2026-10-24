# AGENTS.md - flowbench 开发与协作准则

本项目为 **flowbench（基于自定义 Protobuf 二进制协议的三级微服务与 Keploy 流量录制回放验证基准环境）**。
所有进入本项目的 AI 代理（Agents）必须严格遵循本准则。

---

## 1. 核心工程哲学：规范驱动开发 (SDD)

本项目严禁直接跳过设计堆砌业务代码。所有开发工作必须遵循严格的流水线：
> **「文档规范先行 -> 架构设计定稿 -> 协议与接口契约明晰 -> 任务拆解与验收清单 -> 分阶段实施与验证」**

### 研发阶段递进原则
1. **阶段一：特性清单与边界明确 (`docs/01-features/01-feature-matrix.md`)**：明确三级微服务（`Client` / `Server` / `Backend`）、自定义 Protobuf 二进制协议栈、Server 侧业务加工编排、Keploy 流量监控/录制/回放验证四大领域的需求矩阵与 Non-Goals。
2. **阶段二：系统架构设计文档 (`docs/02-design/`)**：
   - `01-system-architecture.md`：端到端三层拓扑（`Client -> Server -> Backend`）、网络通信模型与模块分层设计；
   - `02-wire-protocol-and-codec.md`：自定义 TCP 二进制帧格式（Magic、Version、OpCode、ReqID、Length、CRC32、Protobuf Payload）、粘包/半包拆帧状态机与错误处理；
   - `03-service-orchestration.md`：`Server` 侧前置校验、下游 `Backend` 调用、数据加工聚合与确定性/非确定性字段控制策略；
   - `04-keploy-traffic-verification.md`：Keploy eBPF 探针挂载机制、Ingress 流量录制、Egress (`Server -> Backend`) TCP Mock 劫持与回放断言方案。
3. **阶段三：协议契约与任务分解 (`docs/03-api/`, `docs/04-tasks/`)**：
   - `docs/03-api/01-binary-frame-spec.md`：自定义二进制协议帧头字节级契约；
   - `docs/03-api/02-protobuf-messages.md`：`.proto` 消息结构定义、操作码映射与状态码字典；
   - `docs/04-tasks/`：工程实施路线图 (`01-roadmap.md`) 与各 Milestone 作战卡及自动化验收标准 (AC)。
4. **阶段四：工程实施与交付 (`proto/`, `cmd/`, `internal/`, `keploy/`, `scripts/`, `tests/`)**：在上述设计定稿并确认后，方可开展业务代码、联调脚本与测试编写。

---

## 2. 行为准则与自动化交付流水线 (Mandatory Workflow)

每次代码修改与任务实施必须遵循以下交付闭环：
1. **沟通与注释语言**：所有交流、设计文档、代码注释及提交日志统一使用**简体中文**。Git 提交日志严格遵循 **Conventional Commits**（如 `feat: ...`、`docs: ...`、`fix: ...`、`test: ...`）。
2. **代码检视 (Self-Review)**：每次代码修改完成后，必须对修改内容进行自检（包括字节序一致性、并发安全、TCP 连接生命周期、错误显式处理与文档一致性）。
3. **本地校验 (Verification)**：必须确保本地构建无报错（`go build ./...`）、静态检查零警告（`go vet ./...`）、所有单元与集成测试全部通过（`go test -race ./...`）。
4. **后台进程清理**：完成本地联调、服务验证或 Keploy 录制/测试后，必须在单次回复前主动关闭所有拉起的后台进程（`Client`、`Server`、`Backend`、`keploy`），严禁留存僵尸进程占用本地端口。
5. **自动交付 (Auto Push)**：**确认无误且本地构建与测试全部通过后，直接提交并 push 到 GitHub 仓库（`origin/main`），无需在汇报后等待用户口头指示。**

---

## 3. 目录边界与治理规范 (Layout & Policies)

项目一级目录具有强边界隔离，严禁随意跨目录散落代码或新建无约定顶层目录：

```text
flowbench/
├── docs/                # 规范文档库：00-prompt ~ 06-ops，版本化管理所有设计与契约
├── proto/               # Protobuf 协议源定义：仅定义数据结构 (.proto)，严禁引入 gRPC Service
├── cmd/                 # 三大子系统入口：cmd/client、cmd/server、cmd/backend
├── internal/            # 内部核心实现：codec(二进制帧编解码)、protocol(pb生成代码)、transport(TCP传输)、server、backend
├── keploy/              # Keploy 流量资产：keploy.yml 配置、录制的测试用例 (test-set) 与二进制 Mock 资产
├── scripts/             # 自动化脚本：编译生成、三端启停编排、Keploy 录制与回放验证脚本
├── tests/               # 集成与回归测试：拆粘包压测、三端全链路 E2E 测试与 Keploy 回放校验
├── AGENTS.md            # Agent 行为准则与架构宪法
└── README.md            # 项目主页与 SDD 研发路线图
```

---

## 4. Go 编码规范与技术红线 (Guardrails)

1. **纯净自定义二进制协议（严禁使用 gRPC / HTTP）**：
   - `Client <-> Server` 与 `Server <-> Backend` 之间必须使用基于原生 `net.Conn` (TCP) 的自定义二进制帧协议承载 Protobuf 序列化负载（`google.golang.org/protobuf/proto`）。
   - **严禁引入 `google.golang.org/grpc` 或使用 HTTP/REST 封装 Protobuf**，确保对 Keploy 的底层 TCP/Binary 流量捕获能力构成真实检验。
2. **严谨的二进制封包与拆包**：
   - 所有多字节整型帧头字段统一采用 **大端字节序 (`binary.BigEndian`)**。
   - 必须使用 `io.ReadFull` 及帧长上限校验（防止恶意或损坏帧导致 OOM）处理 TCP 流式粘包与半包，严禁假定单次 `conn.Read` 等于完整业务报文。
3. **严格的错误处理與资源回收**：
   - 必须显式处理所有 `error`，**严禁使用 `_ = err` 忽略错误**。
   - 所有 TCP 连接、Goroutine 必须具备超时控制（`context.Context` / `SetDeadline`）与优雅退出机制（Graceful Shutdown）。
4. **测试安全与端口绑定规范**：
   - 本地开发与自动化测试时，服务端默认仅绑定 `127.0.0.1`（如 `Server` 监听 `127.0.0.1:9100`，`Backend` 监听 `127.0.0.1:9200`），严禁在未授权测试中暴露 `0.0.0.0`。

---

## 5. 常用开发与验证指令速查

```bash
# 代码格式化与静态检查
go fmt ./...
go vet ./...

# 编译三大微服务子系统
go build -o bin/flowbench-backend ./cmd/backend
go build -o bin/flowbench-server  ./cmd/server
go build -o bin/flowbench-client  ./cmd/client

# 运行全量单元与竞态检测测试
go test -v -race ./...

# 查看当前分支与远端状态
git status && git branch -vv

# 标准提交与推送
git add .
git commit -m "feat/docs/fix: <中文描述>"
git push origin main
```
