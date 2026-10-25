# Milestone 2 作战卡：TCP 传输层、Backend 数据服务与 Server 业务加工引擎

- **文档编号**：`03-m2-backend-and-server.md`
- **归属阶段**：阶段三/四：M2 任务拆解与验收标准
- **前置依赖**：Milestone 1（`internal/codec` & `internal/protocol`）

---

## 1. M2 实施目标

实现原生 TCP 客户端/服务端传输封装（支持短连接与长连接复用）、`Backend` 确定性内存数据底座与查询处理器、`Server` 前置参数校验/下游调用编排/四步定点算术业务加工引擎，以及 `cmd/backend` 和 `cmd/server` 可执行程序入口。

---

## 2. 详细任务拆解 (Tasks Breakdown)

### Task 2.1：实现原生 TCP 传输层 (`internal/transport/`)
1. `internal/transport/server.go`：
   - 定义 `FrameHandler func(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error)`；
   - 实现 `Server` 结构：绑定监听地址（如 `127.0.0.1:9100`），循环 `Accept` 连接并为每个连接启动独立 Goroutine；
   - 连接循环中调用 `codec.ReadFrame` 读取请求帧（设置读写超时 `SetDeadline`），交由 `FrameHandler` 处理后调用 `codec.WriteFrame` 回写响应帧；
   - 支持 `Shutdown(ctx)` 优雅关闭监听器并等待在途连接排空。
2. `internal/transport/client.go`：
   - 支持两种连接模式：`ConnModeShort ("short")` 与 `ConnModeKeepAlive ("keepalive")`；
   - 实现 `Call(ctx context.Context, reqFrame *codec.Frame) (*codec.Frame, error)`：
     - `short` 模式：每次调用 `net.Dialer{Timeout: timeout}.DialContext` 建联 -> `WriteFrame` -> `ReadFrame` -> 校验 `respFrame.Header.RequestID == reqFrame.Header.RequestID` -> 关闭连接；
     - `keepalive` 模式：复用持久连接（加互斥锁保证并发安全，断连时自动重连）。

### Task 2.2：实现 `Backend` 确定性内存数据底座与处理器 (`internal/backend/` & `cmd/backend/`)
1. `internal/backend/store.go`：
   - 内置预置商品字典（`SKU-PRO-01`、`SKU-EDGE-02`、`SKU-OUT-OF-STOCK`、`SKU-SLOW-500MS`）与用户信用字典（`USR-VIP-001`、`USR-NORMAL-002`、`USR-LOW-003`、`USR-BLACKLIST`）。
2. `internal/backend/handler.go`：
   - 路由处理 `OpPingReq (0x01)` -> 返回 `OpPongResp (0x02)`；
   - 路由处理 `OpBackendOrderQueryReq (0x31)` -> 查询商品与用户信用，若 `sku_id == "SKU-SLOW-500MS"` 则按需模拟延迟，返回 `OpBackendOrderQueryResp (0x32)`；
   - 路由处理 `OpBackendCreditQueryReq (0x41)` -> 查询客户征信档案，返回 `OpBackendCreditQueryResp (0x42)`；
   - 未知 `OpCode` 返回 `OpErrorResp (0xFF, StatusUnknownOpCode)`。
3. `cmd/backend/main.go`：支持 `-addr`（默认 `127.0.0.1:9200`）与 `-debug-frame` 参数，监听 `SIGINT/SIGTERM` 优雅退出。

### Task 2.3：实现 `Server` 业务加工引擎与下游编排 (`internal/server/` & `cmd/server/`)
1. `internal/server/config.go`：定义 `Config`（监听地址 `127.0.0.1:9100`、下游 `BackendAddr 127.0.0.1:9200`、连接模式 `ConnMode`、`Deterministic bool`、`FaultMutation string`、`DebugFrame bool`）。
2. `internal/server/processor.go`：
   - 实现 `ValidateOrderQuoteReq(req *pb.OrderQuoteRequest) error` 前置参数校验；
   - 实现 `ProcessOrderQuote(reqID uint64, req *pb.OrderQuoteRequest, bResp *pb.BackendOrderQueryResponse, cfg Config) *pb.OrderQuoteResponse`：严格执行文档 `03-service-orchestration.md` 定义的四大算子（VIP+阶梯折扣、区域税费+重量运费、风控三级决策机、SHA-256 16位十六进制摘要 `processing_digest`）及确定性/非确定性时间戳控制与 `-fault-mutation=discount_bug` 注入；
   - 实现 `ProcessCreditEval(reqID uint64, req *pb.CreditEvalRequest, bResp *pb.BackendCreditQueryResponse, cfg Config) *pb.CreditEvalResponse`。
3. `internal/server/handler.go`：
   - 处理 `OpPingReq`（纯入站，不调 `Backend`）；
   - 处理 `OpOrderQuoteReq`：前置校验失败时直接短路返回 `BIZ_INVALID_ARGUMENT`（不调 `Backend`）；校验通过时构造 `OpBackendOrderQueryReq` 帧调用 `Backend`，再调用 `ProcessOrderQuote` 返回 `OpOrderQuoteResp`；
   - 处理 `OpCreditEvalReq`：前置校验 -> 调用 `Backend` (`OpBackendCreditQueryReq`) -> `ProcessCreditEval` -> 返回 `OpCreditEvalResp`。
4. `cmd/server/main.go`：解析命令行参数并启动 `Server`，支持优雅停机。

---

## 3. 验收标准 (Acceptance Criteria)

- [ ] **AC-M2-01**：`go build -o bin/flowbench-backend ./cmd/backend` 与 `go build -o bin/flowbench-server ./cmd/server` 编译无报错。
- [ ] **AC-M2-02**：`go test -v -race ./internal/backend/... ./internal/server/...` 单元测试 100% 通过，验证 `SC-02` 精确数值（如 `SKU-PRO-01`, `qty=5`, `vip=2`, `CN-EAST` -> `subtotal=50000`, `discount=6500`, `tax=2610`, `shipping=3700`, `final=49810`, `decision="APPROVED"`）。
