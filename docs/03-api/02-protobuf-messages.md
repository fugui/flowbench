# flowbench Protobuf 消息结构与状态码契约 (Protobuf Messages Specification)

- **文档编号**：`02-protobuf-messages.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-25
- **当前阶段**：阶段三：协议契约冻结（消息契约）
- **状态**：已冻结 (Frozen)

---

## 1. 设计准则与约束 (IDL Design Guardrails)

1. **严禁引入 gRPC Service**：`proto/flowbench/v1/messages.proto` 中仅定义 `message` 与 `enum` 数据结构，**绝不声明 `service ... { rpc ... }`**，确保代码生成产物纯粹用于 `google.golang.org/protobuf/proto` 二进制封送。
2. **整型化金额与比率**：所有金额统一使用 `int64` 表示**分（Cents）**，所有折扣率/税率统一使用 `int32` 表示**基点（Basis Points, 1 bp = 0.01%）**，彻底消除 `float`/`double` 在不同 CPU 架构或序列化下的精度不一致。
3. **三端契约解耦**：
   - `Client <-> Server` 消息（`OrderQuoteRequest/Response`、`CreditEvalRequest/Response`）承载外部业务入参与 `Server` 加工后的富化结果；
   - `Server <-> Backend` 消息（`BackendOrderQueryRequest/Response`、`BackendCreditQueryRequest/Response`）承载底层商品、库存与征信原始档案查询，与入口消息结构物理区分。

---

## 2. 完整 `.proto` 契约定义 (`proto/flowbench/v1/messages.proto`)

```protobuf
syntax = "proto3";

package flowbench.v1;

option go_package = "github.com/fugui/flowbench/internal/protocol/pb;pb";

// ============================================================================
// 1. 通用状态码与公共枚举 (Common Enums)
// ============================================================================

enum BizCode {
  BIZ_CODE_UNSPECIFIED   = 0;
  BIZ_OK                 = 200; // 业务处理成功
  BIZ_INVALID_ARGUMENT   = 400; // Server 前置参数校验失败（短路拦截）
  BIZ_RISK_REJECTED      = 403; // 风控拦截或黑名单拒单
  BIZ_NOT_FOUND          = 404; // SKU 或用户不存在
  BIZ_OUT_OF_STOCK       = 409; // 库存不足拒单
  BIZ_INTERNAL_ERROR     = 500; // 内部处理错误
  BIZ_DOWNSTREAM_TIMEOUT = 504; // 下游 Backend 调用超时或异常
}

// ============================================================================
// 2. 链路探活消息 (OpCode: 0x01 PingReq / 0x02 PongResp)
// ============================================================================

message PingRequest {
  string client_id    = 1; // 发起方标识，如 "flowbench-client"
  string message      = 2; // 探活回显内容
  int64  sent_at_unix = 3; // 发送逻辑时间戳（秒）
}

message PongResponse {
  int32  biz_code         = 1; // 固定 200 (BIZ_OK)
  string server_id        = 2; // 响应方标识，如 "flowbench-server" 或 "flowbench-backend"
  string echo_message     = 3; // 回显内容，格式: "PONG:<message>"
  string status           = 4; // 服务状态，固定 "SERVING"
  int64  replied_at_unix  = 5; // 响应时间戳（秒）
}

// ============================================================================
// 3. 订单计价与风控聚合链路 —— 入口契约 (OpCode: 0x11 Req / 0x12 Resp)
// ============================================================================

message OrderQuoteRequest {
  string user_id          = 1; // 用户唯一标识，如 "USR-VIP-001"
  string sku_id           = 2; // 商品编码，如 "SKU-PRO-01"
  int32  quantity         = 3; // 购买数量，合法范围 [1, 1000]
  int32  vip_level        = 4; // 会员等级，合法范围 [0, 3]
  string region           = 5; // 配送与税务区域，如 "CN-EAST", "US-WEST", "EU-CENTRAL"
  int64  client_timestamp = 6; // 客户端逻辑时间戳（秒），用于确定性计算
}

message OrderQuoteResponse {
  int32  biz_code              = 1;  // 业务状态码 (BizCode)
  string biz_message           = 2;  // 业务结果描述
  string sku_name              = 3;  // 商品名称（来自 Backend，由 Server 富化返回）
  int64  unit_price_cents      = 4;  // 商品基准单价（分）
  int64  subtotal_cents        = 5;  // 原始小计金额（分）= unit_price_cents * quantity
  int64  discount_cents        = 6;  // Server 核算的 VIP + 数量阶梯总折扣金额（分）
  int64  discounted_cents      = 7;  // 折后商品净额（分）= subtotal_cents - discount_cents
  int64  tax_cents             = 8;  // Server 核算的区域税费（分）
  int64  shipping_cents        = 9;  // Server 核算的重量阶梯履约运费（分）
  int64  final_amount_cents    = 10; // 最终应付总额（分）= discounted_cents + tax_cents + shipping_cents
  string risk_decision         = 11; // 风控决策结论: "APPROVED" / "MANUAL_REVIEW" / "REJECTED_*"
  string fulfillment_warehouse = 12; // 分配履约仓库编码
  string processing_digest     = 13; // Server 核心加工字段 SHA-256 前 16 位十六进制摘要
  string trace_tag             = 14; // 链路追踪标签
  int64  processed_at_unix     = 15; // 处理完成时间戳（秒/纳秒）
}

// ============================================================================
// 4. 客户授信额度评估链路 —— 入口契约 (OpCode: 0x21 Req / 0x22 Resp)
// ============================================================================

message CreditEvalRequest {
  string user_id               = 1; // 用户唯一标识
  int64  requested_limit_cents = 2; // 申请授信额度（分）
  int64  annual_income_cents   = 3; // 申报年收入（分）
  int64  existing_debt_cents   = 4; // 申报存量负债（分）
  int64  client_timestamp      = 5; // 客户端逻辑时间戳（秒）
}

message CreditEvalResponse {
  int32  biz_code             = 1; // 业务状态码 (BizCode)
  string biz_message          = 2; // 业务结果描述
  string credit_grade         = 3; // Server 评定的信用等级: "A" / "B" / "C" / "D"
  int32  dti_bps              = 4; // Server 核算的负债收入比基点 (Debt-to-Income in bps)
  int64  max_allowable_cents  = 5; // Server 模型测算的最大可授信上限（分）
  int64  approved_limit_cents = 6; // 最终获批授信额度（分）
  string decision             = 7; // 审批结论: "APPROVED" / "REJECTED" / "REJECTED_PRECHECK"
  string processing_digest    = 8; // Server 授信加工结果 SHA-256 前 16 位十六进制摘要
  string trace_tag            = 9; // 链路追踪标签
  int64  processed_at_unix    = 10; // 处理完成时间戳
}

// ============================================================================
// 5. Server <-> Backend 下游内部查询契约 (OpCode: 0x31/0x32 & 0x41/0x42)
// ============================================================================

// OpCode: 0x31 (OpBackendOrderQueryReq)
message BackendOrderQueryRequest {
  string sku_id  = 1; // 待查询商品编码
  string user_id = 2; // 待查询用户编码
  string region  = 3; // 配送区域
}

// OpCode: 0x32 (OpBackendOrderQueryResp)
message BackendOrderQueryResponse {
  bool   found             = 1; // 商品是否存在
  string sku_id            = 2; // 商品编码
  string sku_name          = 3; // 商品名称
  int64  base_price_cents  = 4; // 底层商品基准单价（分）
  int32  available_stock   = 5; // 当前可用库存数
  int32  unit_weight_grams = 6; // 单件商品重量（克）
  string warehouse_code    = 7; // 所属仓库编码
  int32  user_credit_score = 8; // 用户底层基础信用分 (300..850)
  bool   risk_blacklisted  = 9; // 是否命中底层黑名单
}

// OpCode: 0x41 (OpBackendCreditQueryReq)
message BackendCreditQueryRequest {
  string user_id = 1; // 待查询用户编码
}

// OpCode: 0x42 (OpBackendCreditQueryResp)
message BackendCreditQueryResponse {
  bool   found                    = 1; // 用户档案是否存在
  string user_id                  = 2; // 用户编码
  int32  credit_score             = 3; // 征信中心基础信用分 (300..850)
  int32  historical_default_count = 4; // 历史违约逾期次数
  int32  account_age_months       = 5; // 账户账龄（月）
  bool   risk_blacklisted         = 6; // 是否命中高危黑名单
}

// ============================================================================
// 6. 通用协议级异常响应 (OpCode: 0xFF OpErrorResp)
// ============================================================================

message ErrorResponse {
  int32  error_code    = 1; // 对应帧级 Status 或系统错误码
  string error_message = 2; // 详细错误原因说明
  uint64 request_id    = 3; // 关联的请求 ID
}
```

---

## 3. 消息与 `OpCode` 路由映射契约表

| 链路层级 | 请求 OpCode | 请求 Message 类型 | 响应 OpCode | 响应 Message 类型 |
| :--- | :---: | :--- | :---: | :--- |
| 通用探活 (`Client->Server` / `Server->Backend`) | `0x01` | `pb.PingRequest` | `0x02` | `pb.PongResponse` |
| 入口订单计价 (`Client <-> Server`) | `0x11` | `pb.OrderQuoteRequest` | `0x12` | `pb.OrderQuoteResponse` |
| 入口授信评估 (`Client <-> Server`) | `0x21` | `pb.CreditEvalRequest` | `0x22` | `pb.CreditEvalResponse` |
| 下游订单查询 (`Server <-> Backend`) | `0x31` | `pb.BackendOrderQueryRequest` | `0x32` | `pb.BackendOrderQueryResponse` |
| 下游征信查询 (`Server <-> Backend`) | `0x41` | `pb.BackendCreditQueryRequest` | `0x42` | `pb.BackendCreditQueryResponse` |
| 协议异常返回 (任意方向) | - | - | `0xFF` | `pb.ErrorResponse` |
