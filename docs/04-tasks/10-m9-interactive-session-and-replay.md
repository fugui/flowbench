# Milestone 9: 手工录制监控、动态拓扑与单调用自愈重放作战卡

- **文档编号**：`10-m9-interactive-session-and-replay.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段三：作战卡与验收标准
- **状态**：定稿待审 (Ready for Review)

---

## 1. 作战目标与核心交付物

实现用户主导的“目标选择与配置 -> 开始监控 -> 外部发包透明录制 -> 结束监控 -> 动态拓扑感知 -> 事务调用树 -> 单调用原地自愈重放”完整研发联调闭环。

核心交付代码清单：
1. **后端会话与事务引擎**：
   - `internal/monitor/session.go`：会话状态机、录制会话管理、数据模型与持久化；
   - `internal/monitor/correlator.go`：端到端事务关联器，按 `RequestID`/`ullSNO` 将 Ingress 与 Egress 归并为调用树；
   - `internal/monitor/replayer.go`：单调用原地自愈重放驱动器，支持 Payload 微调、自愈 Mock 拦截与 Side-by-Side 字段 Diff；
   - `internal/monitor/server.go`：注册 `/api/session/start`、`/api/session/stop`、`/api/session/current`、`/api/session/traces`、`/api/replay/single` 并增强 SSE 遥测推流；
2. **后端单元与集成测试 (TDD)**：
   - `internal/monitor/session_test.go`：会话启停、多调用事务关联、单调用原地重放与字段 Diff 自动化单元测试；
3. **前端 Web 控制台重构**：
   - `internal/monitor/web/index.html`：置顶 Master Bar、SVG 动态流量拓扑画布、双分栏（左栏配置/事务树，右栏解剖/重放/Mock）；
   - `internal/monitor/web/styles.css`：拓扑粒子动画、呼吸灯指示、树形卡片折叠、Side-by-Side 差异高亮样式；
   - `internal/monitor/web/app.js`：会话状态管理、拓扑图动态渲染、事务树折叠交互、单调用原地重放触发与 Diff 呈现。

---

## 2. 自动化验收标准 (AC)

- [ ] **AC-01 (会话状态机与透明录制)**：调用 `/api/session/start` 开启会话，外部客户端发起多次调用，所有入站和出站帧正确记录入当前会话，调用 `/api/session/stop` 正常结束并固化。
- [ ] **AC-02 (端到端事务关联)**：包含下游查询的复合调用能够自动聚合成一个 `TransactionTrace`，清晰展示 1 个 Ingress Req、对应子 Egress 查询与 Ingress Resp。
- [ ] **AC-03 (单调用自愈重放)**：在关闭真实 Backend 的前提下，调用 `/api/replay/single` 能够成功驱动 Server 并由 Mock 引擎自愈响应，无差错时报告 `ByteExactMatch=true`；修改请求时报告字段级 Diff。
- [ ] **AC-04 (Web 控制台完整体验)**：前端页面展示置顶 Master Bar、动态粒子流动拓扑、可折叠事务树列表，点击任意调用可一键发起原地重放并实时显示 Diff。
- [ ] **AC-05 (全工程质量门禁)**：`go build ./...`、`go vet ./...`、`go test -v -race ./...` 100% 通过，无后台进程泄漏。
