# Milestone 10: 业务开发视角单组件测试工作台与经典 UML 时序图作战卡

- **文档编号**：`11-m10-single-component-testing-console.md`
- **文档版本**：v1.0.0
- **编制日期**：2026-10-28
- **当前阶段**：阶段三：作战卡与验收标准
- **状态**：定稿待审 (Ready for Review)

---

## 1. 作战目标与核心交付物

从**“负责开发与测试单微服务组件（SUT）的业务开发人员”**视角出发，重塑控制台交互体系：
1. **彻底消除主视觉中的二进制底层压迫感**：默认以 Protobuf 业务明文为主导，底层 24B 帧头与 Hex 隐入按需折叠抽屉；
2. **经典 UML 交互式时序图**：按真实时间先后呈现垂直生命线与水平实线/虚线箭头，彻底讲清“谁发给谁、第几步、耗时多少”；
3. **下游智能识别**：别名（如 `credit-svc`）优先，IP:Port（如 `127.0.0.1:9200`）兜底；
4. **单组件测试闭环**：在时序图上点击任意步骤，就地查看/修改入参重放、就地修改下游响应作为 Mock 挡板重放、高亮展示业务字段 Diff。

核心交付代码清单：
1. **后端 UML 数据模型与时序构建器 (TDD)**：
   - `internal/monitor/uml.go`：定义 `UMLParticipant`、`UMLSequenceStep`、`UMLDiagramData` 并实现 `BuildUMLDiagram(tr TransactionTrace, backends []BackendEndpoint, procName string)`；
   - `internal/monitor/uml_test.go`：TDD 单元测试，覆盖单请求-响应、含多下游子调用的全链路时序编排、下游别名与 IP:Port 兜底识别、Mock 标志与耗时计算；
   - `internal/monitor/session.go`：在 `TransactionTrace` 中内嵌或派生 `UMLDiagram`，并在 API `/api/session/traces` 与 `/api/session/current` 中暴露；
2. **前端 Web 控制台重构**：
   - `internal/monitor/web/index.html`：重构顶部拓扑（三元业务拓扑）、左栏用例列表、右栏上半区经典 UML 时序图容器、右栏下半区业务 Protobuf 载荷与原地重放/Mock/Diff 一体化工作台、底部折叠帧头抽屉；
   - `internal/monitor/web/styles.css`：经典 UML 时序图样式（生命线、实线/虚线消息箭头、步骤圆圈、发光高亮）、Protobuf 业务明文编辑器样式、折叠抽屉样式；
   - `internal/monitor/web/app.js`：解析并渲染 UML 时序图、支持点击消息线联动切换下方载荷、支持原地改参重放、支持原地下游 Mock 保存并触发重放、支持业务 Diff 展示。

---

## 2. 自动化验收标准 (AC)

- [ ] **AC-01 (UML 数据模型与别名解析单元测试)**：`internal/monitor/uml_test.go` 100% 通过。验证生命线生成准确、步骤序号 `①, ②, ③...` 严格单调递增、实线调用/虚线返回分类正确、配置的后端别名优先匹配且未配置时精准回退至 `IP:Port`。
- [ ] **AC-02 (经典 UML 时序图前端呈现)**：在 Web 控制台选中任意调用用例，右栏上半区正确渲染经典 UML 时序图，显示 `Caller`、`SUT`、`Backends` 垂直虚线生命线与水平连线，明确标出步骤序号、OpName、耗时和组件往返。
- [ ] **AC-03 (业务 Protobuf 载荷主导与单组件原地重放闭环)**：主工作区默认 100% 展示所选步骤的 Protobuf 业务 JSON；可在步骤 ① 上就地改参并重放；可在下游返回步骤上就地改 Mock 返回并触发重放；重放结果直观展现业务字段级 Diff。
- [ ] **AC-04 (底层协议帧头按需折叠)**：24 字节帧头矩阵与 Hex Dump 默认折叠隐藏，不干扰业务测试；点击抽屉开关时能平滑展开并在发生校验异常时主动警示。
- [ ] **AC-05 (全工程质量门禁与进程无泄漏)**：`go build ./...`、`go vet ./...`、`go test -v -race ./...` 全部通过，无任何僵尸进程遗留。
