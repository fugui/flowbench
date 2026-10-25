# flowbench: 阶段四工程实施与全链路交付验证 (SDD) 提示词指令

## 角色定位与工程原则
你是一名资深 Go 分布式系统工程师与测试专家。在 `flowbench` 的阶段一（特性矩阵）、阶段二（系统架构设计）与阶段三（二进制帧契约、Protobuf 消息契约与 M1~M4 作战卡）全部定稿冻结后，现在正式进入：
**「阶段四：工程实施、自动化测试与交付验证」**。

---

## 实施范围与递进步骤 (Implementation Pipeline)

请严格遵循 `docs/03-api/` 的契约规范与 `docs/04-tasks/` 的里程碑作战卡，按顺序完成以下 4 个里程碑的代码实现与验证：

1. **实施 Milestone 1（协议与编解码器）**：
   - 初始化 `go.mod`；
   - 落地 `proto/flowbench/v1/messages.proto` 与 `internal/protocol/pb/messages.pb.go`、`internal/protocol/marshal.go`；
   - 实现 `internal/codec/frame.go`、`internal/codec/stream.go` 及单元测试 `internal/codec/frame_test.go`。
2. **实施 Milestone 2（TCP 传输层、`Backend` 与 `Server`）**：
   - 实现 `internal/transport/server.go` 与 `internal/transport/client.go`（支持 `short` 与 `keepalive`）；
   - 实现 `internal/backend/store.go`、`internal/backend/handler.go`、`internal/backend/handler_test.go` 与 `cmd/backend/main.go`；
   - 实现 `internal/server/config.go`、`internal/server/processor.go`、`internal/server/handler.go`、`internal/server/processor_test.go` 与 `cmd/server/main.go`。
3. **实施 Milestone 3（`Client` 驱动器、全链路 E2E 与自动化脚本）**：
   - 实现 `cmd/client/main.go`（支持 `ping`, `quote`, `credit`, `suite`, `framing` 模式）；
   - 实现 `tests/e2e_test.go`（覆盖 6 大场景、短路零调用、确定性字节相等、突变检测、TCP 拆粘包）；
   - 编写 `Makefile` 与 `scripts/verify-e2e.sh` 并实机运行验证。
4. **实施 Milestone 4（Keploy 配置、录制/回放脚本与离线 Mock 回放验证）**：
   - 落地 `keploy/keploy.yml`、`scripts/keploy-record.sh`、`scripts/keploy-test.sh`；
   - 实现 `tests/replay_verify_test.go` 验证录制入站用例 + 劫持出站 `Backend` 二进制 Mock + 关闭 `Backend` 离线回放及突变拦截全过程；
   - 编写 `docs/06-ops/01-keploy-verification-guide.md` 并在 `docs/05-adr/01-custom-binary-frame-and-determinism.md` 中沉淀核心架构决策记录；
   - 更新 `README.md` 与 `docs/README.md` 全部阶段状态为已完成。

---

## 交付红线
1. 全量代码通过 `go fmt ./...`、`go vet ./...`、`go test -v -race ./...`。
2. 实机执行 `./scripts/verify-e2e.sh` 验证三端二进制全链路联调通过。
3. 单次回复前确保关闭并清理所有拉起的后台测试进程，确认无误后自动提交并 push 至 `origin/main`。
