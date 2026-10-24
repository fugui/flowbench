# flowbench: 阶段二系统架构设计 (SDD) 提示词指令

## 角色定位与工程原则
你是一名资深 Go 分布式系统架构师、自定义二进制网络协议专家与 eBPF 流量录制回放（Keploy）测试专家。我们正在采用 **规范驱动开发 (Spec-Driven Development, SDD)** 模式开发 **flowbench（基于自定义 Protobuf 二进制协议的三级微服务与 Keploy 流量录制回放验证基准环境）**。

在阶段一特性清单与边界界定规范（[`docs/01-features/01-feature-matrix.md`](file:///home/fugui/flowbench/docs/01-features/01-feature-matrix.md)）已定稿的基础上，现在正式进入：
**「阶段二：系统架构设计文档编制」**。

---

## 任务目标与设计文档矩阵 (Design Docs Matrix)

为避免单一文档体积膨胀导致职责混乱，阶段二系统架构设计采用 **「1 篇总览核心架构 + 3 篇深度子系统规范」** 的总分系列矩阵组织：

| 文档路径 | 规范类型 | 核心职责与设计边界 |
| :--- | :--- | :--- |
| `docs/02-design/01-system-architecture.md` | **【总览篇】系统总体架构** | 三级微服务（`Client -> Server -> Backend`）部署拓扑、端到端请求生命周期序列图、代码目录与 Go Package 分层设计、连接模型（短连接 vs 长连接复用）、优雅停机与配置体系 |
| `docs/02-design/02-wire-protocol-and-codec.md` | **【协议篇】二进制帧协议与编解码** | 24 字节定长大端二进制帧头（Bit/Byte Layout）、`Magic/Version/OpCode/Flags/Status/RequestID/PayloadLen/CRC32` 字段语义、基于 `io.ReadFull` 的流式拆粘包状态机、确定性 Protobuf 封送与帧级异常恢复 |
| `docs/02-design/03-service-orchestration.md` | **【业务篇】三端协同与业务加工** | `Server` 前置参数校验、`Server -> Backend` 二进制调用编排、`Server` 四大核心业务加工算子（阶梯折扣、区域税费、综合风控决策、SHA-256 摘要计算）、确定性模式开关（Deterministic Mode）与 `Backend` 内存数据集设计 |
| `docs/02-design/04-keploy-traffic-verification.md` | **【验证篇】Keploy 流量录制与回放架构** | Keploy eBPF 内核拦截机制在自定义 TCP 二进制协议下的工作流、Ingress 用例捕获与 Egress (`Server -> Backend`) TCP 二进制 Mock 匹配机制、6 大验证场景（`SC-01` ~ `SC-06`）执行拓扑与断言判定方案 |

> **红线提示**：本阶段**严禁编写任何 `.go` 或 `.proto` 业务源码**，只产出上述系统架构设计规范文档。

---

## 核心设计要求与技术约束 (Technical Guardrails)

1. **纯净自定义二进制协议栈**：严禁使用 gRPC 或 HTTP/2；明确 `net.Conn` 上的 24 字节定长二进制帧头布局与 `google.golang.org/protobuf/proto` 载荷映射关系。
2. **Server 侧可验证的深度业务加工**：详细定义 `Server` 拿到 `Backend` 数据后的数学计算公式（整数分 `int64` 精确核算，杜绝浮点数精度漂移）与 SHA-256 摘要拼接规则，确保任何逻辑突变必然反映在输出的 Protobuf 二进制字节流上。
3. **二进制回放确定性保障**：明确设计 `proto.MarshalOptions{Deterministic: true}` 与逻辑时钟控制机制，消除非预期随机因子对 Keploy 二进制帧比对的干扰，同时保留 `-deterministic=false` 开关用于对照实验。
4. **本地环回安全与超时防线**：所有服务端默认绑定 `127.0.0.1`（`Server: 127.0.0.1:9100`，`Backend: 127.0.0.1:9200`），明确帧最大长度限制（`MaxPayloadSize = 4MB`）与全链路读写 `Deadline` 传递策略。

---

## 🎯 执行指令
请遵循上述设计规范，在 `docs/02-design/` 下输出完整的阶段二系统架构设计文档矩阵（`01-system-architecture.md` ~ `04-keploy-traffic-verification.md`），并同步更新 `README.md` 与 `docs/README.md` 进度索引。
