# Milestone 4 作战卡：Keploy 流量录制、二进制 Mock 劫持与回放验证

- **文档编号**：`05-m4-keploy-verification.md`
- **归属阶段**：阶段三/四：M4 任务拆解与验收标准
- **前置依赖**：Milestone 3（三端二进制与全链路 E2E 验证通过）

---

## 1. M4 实施目标

完成 Keploy 配置文件、自动化录制脚本（`scripts/keploy-record.sh`）、无 `Backend` 离线回放验证脚本（`scripts/keploy-test.sh`）、标准录制基准资产模板（`keploy/`）以及完整的运维与实测验证指南（`docs/06-ops/01-keploy-verification-guide.md`）。

---

## 2. 详细任务拆解 (Tasks Breakdown)

### Task 4.1：配置 Keploy 与录制/回放编排脚本 (`keploy/` & `scripts/`)
1. `keploy/keploy.yml`：配置 Keploy 录制与回放参数（目标端口 `9100`、构建延迟、超时设置与测试集路径）。
2. `scripts/keploy-record.sh`：
   - 检查二进制构建与 `keploy` CLI 环境（若当前机器未安装 `keploy` 或无 `sudo`/eBPF 权限，给出清晰的安装命令与诊断提示）；
   - 启动 `flowbench-backend` 监听 `127.0.0.1:9200`；
   - 使用 `keploy record -c "./bin/flowbench-server -addr=127.0.0.1:9100 -backend-addr=127.0.0.1:9200 -deterministic=true"` 启动被测 `Server`；
   - 待端口就绪后执行 `./bin/flowbench-client -mode=suite` 产生 6 大标准场景流量；
   - 向 `Server`/Keploy 发送优雅停止信号，自动回收 `Backend` 进程并展示生成的 `keploy/test-set-*` 文件。
3. `scripts/keploy-test.sh`：
   - 确保 `flowbench-backend` **完全处于关闭状态**（验证 `127.0.0.1:9200` 无进程监听）；
   - 执行 `keploy test -c "./bin/flowbench-server -addr=127.0.0.1:9100 -backend-addr=127.0.0.1:9200 -deterministic=true"`；
   - 支持传入 `--mutation` 或 `--non-deterministic` 参数运行回归突变检出与非确定性对照实验，退出时自动清理所有进程。

### Task 4.2：构建进程内二进制录制/回放模拟器与契约校验 (`tests/replay_verify_test.go`)
为了在无 root/eBPF 权限的受限容器或 CI 环境中也能 100% 验证“录制 Ingress TestCase + 劫持 Egress Binary Mock + 离线关闭 Backend 回放 Server”的完整机制，在 `tests/replay_verify_test.go` 中额外实现一套**二进制流量录制与离线 Mock 回放验证测试**：
1. **阶段 A（录制态）**：通过透明 TCP 录制代理拦截 `Client -> Server` 与 `Server -> Backend` 的原始 24B+Protobuf 二进制帧并序列化保存为测试资产；
2. **阶段 B（离线回放态）**：彻底关闭真实 `Backend`，启动二进制 Mock 桩响应 `Server` 的出站连接，向 `Server` 重放录制的入站请求帧，逐字节断言 `Server` 输出帧与录制期 100% 一致；
3. **阶段 C（突变拦截态）**：开启 `FaultMutation="discount_bug"`，重放相同请求与相同 Backend Mock，断言回放比对器精准捕获字节流与 CRC32 差异。

### Task 4.3：编制 Keploy 运维与验证手册 (`docs/06-ops/01-keploy-verification-guide.md`)
详细记录：
1. Keploy eBPF 环境要求与一键安装指令；
2. 三端独立联调、Keploy 录制、关闭 `Backend` 离线回放的标准 SOP；
3. 录制的二进制帧 Hex 结构剖析、突变检出实验现象与确定性改造最佳实践总结。

---

## 3. 验收标准 (Acceptance Criteria)

- [ ] **AC-M4-01**：`scripts/keploy-record.sh` 与 `scripts/keploy-test.sh` 语法检查通过（`bash -n scripts/*.sh`）且具备可执行权限。
- [ ] **AC-M4-02**：`go test -v -race ./...`（含离线二进制 Mock 回放验证 `TestBinaryRecordAndOfflineReplay`）100% 通过。
- [ ] **AC-M4-03**：`docs/06-ops/01-keploy-verification-guide.md` 完整归档，所有后台进程清理完毕，代码提交并推送至 `origin/main`。
