# flowbench 手工录制会话与单调用自愈重放 API 契约规范

- **文档编号**：`04-interactive-session-and-replay-api.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段三：API 与协议契约
- **状态**：定稿待审 (Ready for Review)

---

## 1. 会话控制 RESTful 接口

### 1.1 开启手工录制会话 (`POST /api/session/start`)

- **请求方式**：`POST /api/session/start`
- **Content-Type**：`application/json`
- **请求体格式**：
```json
{
  "title": "手工联调录制-订单与风控流",
  "target_pid": 8920,
  "process_name": "flowbench-server",
  "ingress_port": 9100,
  "backends": [
    { "name": "order-stock", "host": "127.0.0.1", "port": 9200, "active": true },
    { "name": "user-credit", "host": "127.0.0.1", "port": 9201, "active": true }
  ]
}
```
- **成功响应** (`200 OK`)：
```json
{
  "session": {
    "session_id": "REC-20261028-094520-8920",
    "title": "手工联调录制-订单与风控流",
    "status": "RECORDING",
    "started_at": "09:45:20",
    "target_pid": 8920,
    "process_name": "flowbench-server",
    "ingress_port": 9100,
    "backends": [ ... ],
    "total_frames": 0,
    "total_traces": 0
  },
  "ebpf_status": {
    "mode": "KERNEL_EBPF_CONNECT4_ACTIVE",
    "kernel_active": true
  }
}
```

---

### 1.2 结束当前录制会话 (`POST /api/session/stop`)

- **请求方式**：`POST /api/session/stop`
- **成功响应** (`200 OK`)：
```json
{
  "session": {
    "session_id": "REC-20261028-094520-8920",
    "title": "手工联调录制-订单与风控流",
    "status": "STOPPED",
    "started_at": "09:45:20",
    "stopped_at": "09:46:15",
    "duration_sec": 55.2,
    "total_frames": 16,
    "total_traces": 4,
    "mocks_created": [ ... ]
  }
}
```

---

### 1.3 获取当前会话状态及汇总 (`GET /api/session/current`)

- **请求方式**：`GET /api/session/current`
- **成功响应** (`200 OK`)：
```json
{
  "session": { ... },
  "telemetry": {
    "ingress_qps": 2.5,
    "ingress_kbps": 3.8,
    "egress_qps": 5.0,
    "egress_kbps": 6.2,
    "active_edges": ["ingress", "egress_9200"]
  }
}
```

---

### 1.4 获取端到端聚合事务列表 (`GET /api/session/traces`)

- **请求方式**：`GET /api/session/traces`
- **查询参数**：`q`（按 OpCode / RequestID 过滤，可选）
- **成功响应** (`200 OK`)：
```json
{
  "traces": [
    {
      "trace_id": "trace-1002",
      "request_id": 1002,
      "op_name": "OpOrderQuote",
      "status_code": 200,
      "status_name": "StatusOK",
      "duration_ms": 2.45,
      "started_at": "09:45:30.120",
      "ingress_req": { "seq_id": 1, "header": { ... }, "protobuf_json": { "skuId": "SKU-PRO-01", "quantity": 3 } },
      "sub_calls": [
        {
          "sub_call_id": "sub-1002-1",
          "target_backend": "127.0.0.1:9200",
          "backend_name": "order-stock",
          "op_name": "OpBackendOrderQueryReq",
          "duration_ms": 0.65,
          "egress_req": { ... },
          "egress_resp": { ... }
        }
      ],
      "ingress_resp": { "seq_id": 4, "header": { "crc32_hex": "0x4293DC4D" }, "protobuf_json": { "finalAmountCents": 50310 } }
    }
  ]
}
```

---

### 1.5 发起单调用原地自愈重放 (`POST /api/replay/single`)

- **请求方式**：`POST /api/replay/single`
- **Content-Type**：`application/json`
- **请求体格式**：
```json
{
  "trace_id": "trace-1002",
  "override_payload_json": {
    "userId": "USR-VIP-001",
    "skuId": "SKU-PRO-01",
    "quantity": 5
  },
  "mock_egress": true
}
```
- **成功响应** (`200 OK`)：
```json
{
  "trace_id": "trace-1002",
  "passed": false,
  "byte_exact_match": false,
  "expected_crc32": "0x4293DC4D",
  "actual_crc32": "0x89DFB102",
  "latency_ms": 1.82,
  "field_diffs": [
    {
      "field": "quantity",
      "expected": "3",
      "actual": "5"
    },
    {
      "field": "finalAmountCents",
      "expected": "50310",
      "actual": "83850"
    }
  ],
  "actual_response": {
    "seq_id": 9901,
    "header": { "op_name": "OpOrderQuoteResp", "crc32_hex": "0x89DFB102" },
    "protobuf_json": { "finalAmountCents": 83850, "status": "APPROVED" }
  }
}
```

---

## 2. SSE 实时事件流扩展契约 (`/api/events`)

在原有的实时帧事件基础上，扩展事件类型字段 `event_type`：
1. `event_type: "frame"`：增量捕获到的单个二进制帧；
2. `event_type: "trace_update"`：完成或更新的端到端调用事务；
3. `event_type: "telemetry"`：各条链路的滑动窗口 QPS、字节速率与活跃状态，格式如下：
```json
{
  "event_type": "telemetry",
  "timestamp": "09:45:32",
  "links": {
    "client->sut": { "qps": 1.2, "kbps": 2.4, "avg_lat_ms": 1.8, "active": true },
    "sut->backend_9200": { "qps": 1.2, "kbps": 1.1, "avg_lat_ms": 0.6, "active": true }
  }
}
```
