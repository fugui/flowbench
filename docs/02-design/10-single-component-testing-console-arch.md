# flowbench 业务开发视角单组件测试工作台与经典 UML 时序图系统架构设计

- **文档编号**：`10-single-component-testing-console-arch.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段二：系统架构设计文档
- **状态**：定稿待审 (Ready for Review)

---

## 1. 架构设计哲学与模型演进 (Design Philosophy)

在过去的监控控制台中，数据展示主要面向“协议解剖与网络流”；而在本架构中，系统完全转向**“以被测服务（SUT）为中心的业务时序交互模型”**：

```text
+--------------------------------------------------------------------------------------------------+
|                            flowbench Monitor Studio (业务开发单组件工作台)                       |
+--------------------------------------------------------------------------------------------------+
| 顶部极简业务拓扑:                                                                                |
|  [ 💻 业务调用端 (Caller) ]  ──▶  [ 🎯 被测服务 (SUT: 9100) ]  ──▶  [ 🗄️ 下游依赖组件 (9200/9201) ]   |
|                                                                     └─ [🟢 真实透传 / 🎭 离线Mock] |
+-------------------------------------+------------------------------------------------------------+
| 左栏: 业务请求用例列表 (Business)   | 右栏主工作区:                                              |
|                                     | +--------------------------------------------------------+ |
| #1005 OpOrderQuote (订单报价) 1.6ms | | 【区域 1: 经典 UML 交互式时序图 (Caller <-> SUT <-> Backends)】| |
|   ✅ StatusOK (2次下游调用)          | |  Caller      🎯 SUT (order-server)    🗄️ credit-svc:9200| |
|                                     | |    |                   |                     |         | |
| #1006 OpCreditEval (信用评估) 1.0ms | |    | ① ──(OpQuoteReq)─▶|                     |         | |
|   ⚠️ 业务拦截 (403)                  | |    |                   | ② ──(OpCreditReq)──▶|         | |
|                                     | |    |                   | ◀── ③ (OpCreditResp)| [🎭Mock]  | |
|                                     | |    | ◀── ④ (OpQuoteResp) (1.6ms)               |         | |
|                                     | +--------------------------------------------------------+ |
|                                     | | 【区域 2: 选中步骤 Protobuf 业务明文查看 / 改参 / 重放 / Diff】| |
|                                     | |  - 当前步骤: ③ credit-svc -> order-server               | |
|                                     | |  - dynamicpb 业务 JSON 查看与在线修改 (直接改Mock重放)  | |
|                                     | |  - [🚀 原地重放]  [Side-by-Side 字段级高亮比对]        | |
|                                     | +--------------------------------------------------------+ |
|                                     | | 【区域 3: 底层帧头与 Hex 排障抽屉 (默认折叠收起)】      | |
|                                     | |  [🔧 展开底层 24B 帧头字节与原始 Hex (仅协议异常/Debug用)]| |
|                                     | +--------------------------------------------------------+ |
+-------------------------------------+------------------------------------------------------------+
```

---

## 2. 后端数据模型：UML 时序因果关系图结构

为保证前后端时序生成的确定性与可测性，我们在 [`internal/monitor`](file:///home/fugui/flowbench/internal/monitor) 中定义结构化的 UML 数据抽象，并由 [`correlator.go`](file:///home/fugui/flowbench/internal/monitor/correlator.go) / [`session.go`](file:///home/fugui/flowbench/internal/monitor/session.go) 统一生成：

### 2.1 参与者生命线定义 (`UMLParticipant`)

```go
type ParticipantRole string

const (
    RoleCaller  ParticipantRole = "CALLER"  // 上游业务调用方
    RoleSUT     ParticipantRole = "SUT"     // 核心被测业务组件 (居中高亮)
    RoleBackend ParticipantRole = "BACKEND" // 下游依赖微服务
)

type UMLParticipant struct {
    ID          string          `json:"id"`           // 如 "caller", "sut", "backend-127.0.0.1:9200"
    DisplayName string          `json:"display_name"` // 如 "业务调用端 (Caller)", "order-server (SUT)", "风控中心 (credit-svc:9200)"
    Role        ParticipantRole `json:"role"`
    Endpoint    string          `json:"endpoint"`     // 如 "127.0.0.1:9100", "127.0.0.1:9200"
}
```

### 2.2 消息调用步骤定义 (`UMLSequenceStep`)

```go
type UMLSequenceStep struct {
    StepIndex    int                `json:"step_index"`    // 1-based: 1, 2, 3, 4...
    FromID       string             `json:"from_id"`       // 发送方 Participant ID
    ToID         string             `json:"to_id"`         // 接收方 Participant ID
    FromName     string             `json:"from_name"`     // 发送方友好名称
    ToName       string             `json:"to_name"`       // 接收方友好名称
    IsReturn     bool               `json:"is_return"`     // false: 同步请求实线 (──▶); true: 响应返回虚线 (◀──)
    OpCode       uint32             `json:"op_code"`
    OpName       string             `json:"op_name"`
    StatusCode   uint32             `json:"status_code"`
    StatusName   string             `json:"status_name"`
    DurationMs   float64            `json:"duration_ms"`   // 该步耗时 (ms)
    Timestamp    string             `json:"timestamp"`
    IsMockable   bool               `json:"is_mockable"`   // 下游响应是否支持被 Mock 注入
    MockAssetID  string             `json:"mock_asset_id,omitempty"`
    FrameEvent   CapturedFrameEvent `json:"frame_event"`   // 关联的底层捕获帧 (含 ProtobufJSON, Header)
}
```

### 2.3 完整的 UML 时序图模型 (`UMLDiagramData`)

```go
type UMLDiagramData struct {
    TraceID         string            `json:"trace_id"`
    RequestID       uint64            `json:"request_id"`
    TotalDurationMs float64           `json:"total_duration_ms"`
    Participants    []UMLParticipant  `json:"participants"`
    Steps           []UMLSequenceStep `json:"steps"`
}
```

---

## 3. 下游组件命名识别与生命线构建算法

### 3.1 识别解析规则 (Resolution Rule)
在解析出站请求的目标地址 `targetBackend`（格式 `host:port`）时：
1. 查找被测服务挂载配置中的 `Backends []BackendEndpoint`；
2. **规则一（已配别名）**：若 `b.Host:b.Port == targetBackend` 且 `b.Name != ""`，显示格式为：
   `fmt.Sprintf("%s (%s:%d)", b.Name, b.Host, b.Port)`（例如：`风控中心 (credit-svc:9200)`）；
3. **规则二（未配别名但注册了后端）**：显示 `fmt.Sprintf("下游组件 (%s:%d)", b.Host, b.Port)`；
4. **规则三（未注册的动态连接）**：直接以目标地址兜底 `fmt.Sprintf("下游组件 (%s)", targetBackend)`。

### 3.2 步骤编排生成逻辑 (Step Construction Algorithm)
针对每个 `TransactionTrace`：
1. **步骤 1 (入站请求)**：
   - `From`: `caller` (`业务调用端 (Caller)`)
   - `To`: `sut` (`<ProcessName> (SUT)`)
   - `IsReturn`: `false`
   - `Frame`: `tr.IngressReq`
2. **步骤 2 ~ 2N+1 (按时间顺序遍历 `tr.SubCalls`)**：
   - 对于每个子调用 `sc`：
     - **子步骤 A (发向下游请求)**：`From: sut` ➔ `To: backendID`，`IsReturn: false`，`Frame: sc.EgressReq`
     - **子步骤 B (下游响应返回)**：若 `sc.EgressResp != nil`，`From: backendID` ➔ `To: sut`，`IsReturn: true`，`DurationMs: sc.DurationMs`，`IsMockable: true`，`Frame: *sc.EgressResp`
3. **最终步骤 (入站响应闭环)**：
   - 若 `tr.IngressResp != nil`：
     - `From: sut` ➔ `To: caller`，`IsReturn: true`，`DurationMs: tr.DurationMs`，`Frame: *tr.IngressResp`

---

## 4. 前端渲染与业务操作流 (UI & Interaction Flow)

### 4.1 经典 UML 交互式时序图画布 (Pure CSS + SVG)
- **生命线头 (Lifeline Box)**：顶部并排显示各个组件卡片，被测组件（SUT）使用醒目的金色或青色高亮边框；
- **生命线 (Lifeline)**：从每个卡片底部垂直向下延伸虚线；
- **消息箭头 (Message Arrow)**：
  - 同步请求：深蓝色/青色实线，末端带实心箭头 `▶`；
  - 响应返回：灰色/翠绿色虚线，末端带开式箭头 `▶` 或向左指向；
  - 连线上方悬浮：`① OpOrderQuoteReq` 与 `耗时胶囊`；
  - 下游返回右侧附带：`[🎭 原地Mock]` 快捷按钮；
- **点击选中态**：点击任意消息连线或步骤，连线产生高亮发光特效，并在下方工作台中无缝载入其 Protobuf 业务明文。

### 4.2 业务开发单组件自测 4 步闭环工作台

1. **第 1 步：看业务明文**：
   - 界面不再呈现晦涩的 Base64 和 24B 拆包色块，直接呈现格式化的高亮 JSON（如 `annualIncomeCents: 20000000`, `userId: "USR-VIP-001"`）。
2. **第 2 步：原地改参**：
   - 选中步骤 1（入站请求），右侧编辑框直接允许修改任意字段值，点击右上角【🚀 发起重放】。
3. **第 3 步：原地下游 Mock 注入**：
   - 选中任意下游响应步骤（如步骤 3），直接在响应 JSON 中把 `creditScore: 750` 改为 `300`，点击【保存为挡板并重放】，系统自动以该 Mock 数据回放给 SUT，验证被测服务的异常分支！
4. **第 4 步：结果 Diff 比对**：
   - 重放结果即时刷新，左右并排对比【基准业务响应 vs 本次实际响应】，字段变更标红高亮。
5. **按需排障抽屉**：
   - 底层 24B 帧头、Magic、CRC32 校验和原始 Hex dump 统一折叠在下方微型面板【🔧 底层协议头与二进制 Hex (校验正常)】，只有在排查协议解析故障时才点击展开。
