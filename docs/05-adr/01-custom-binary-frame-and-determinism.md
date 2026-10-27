# ADR-01: 采用 24 字节定长大端二进制帧头与确定性控制保障离线流量回放

- **文档编号**：`01-custom-binary-frame-and-determinism.md`
- **日期**：2026-10-25
- **状态**：已采纳 (Accepted)

---

## 1. 背景与问题陈述 (Context & Problem Statement)

`flowbench` 旨在构建一个 `Client -> Server -> Backend` 的三级 Go 微服务系统，通信层要求**使用 Protobuf 但严禁使用 gRPC**，而采用自定义二进制协议，并支持全链路流量检测、监控与离线自愈回放。

在工程落地中面临三大关键架构权衡：
1. **如何在原生 TCP 流上界定 Protobuf 消息边界？** Protobuf Wire Format 本身不是自定界的（Not Self-Delimiting），直接将多个 Protobuf 字节流写入 TCP 会产生粘包与半包问题。
2. **如何在不使用 `protoc` 外部编译器的环境下保持 100% 原生 Protobuf Wire 兼容？**
3. **如何解决自定义二进制协议在离线回放时的动态噪声（Noise）问题？** 私有二进制帧（尤其包含帧头 `CRC32` 校验和）若在未解析私有协议的情况下直接比对，任何时间戳或随机因子的变化都会导致整帧校验和失配。

---

## 2. 决策方案 (Decisions)

### 决策 1：设计 24 字节定长大端（Big-Endian）二进制帧头 (FBWP/1.0)
- 采用 8 字节对齐的 24 字节定长帧头：`Magic(2B: 0xFB01) + Version(1B) + OpCode(1B) + Flags(1B) + Status(1B) + Reserved(2B) + RequestID(8B) + PayloadLen(4B) + CRC32(4B)`。
- 写入时将 `24B Header + Payload` 合并为单个连续切片执行一次 `conn.Write`；读取时采用两阶段 `io.ReadFull` 状态机。
- **收益**：既彻底解决 TCP 粘包/半包问题，又保证单帧对应单次内核 `write` 系统调用，使 eBPF 探针捕获的报文边界清晰稳定。

### 决策 2：基于 `google.golang.org/protobuf` 的 `protoimpl.TypeBuilder` 与确定性 Marshal
- 通过 `descriptorpb.FileDescriptorProto` 与 `protoimpl.TypeBuilder` 在 `internal/protocol/pb` 中绑定全部 11 个 Protobuf 消息结构，100% 遵循标准 Protobuf 二进制编码规范且零外部工具链依赖。
- 统一通过 `proto.MarshalOptions{Deterministic: true}` 进行序列化。

### 决策 3：引入注入式逻辑时钟与 `-deterministic` / `-fault-mutation` 双开关
- 在 `Server` 侧加工引擎中，默认启用确定性模式（响应时间戳由请求携带的逻辑时间戳 `client_timestamp + 1` 派生），使相同输入产生逐字节 100% 一致的响应帧与 CRC32。
- 保留 `-deterministic=false` 与 `-fault-mutation=discount_bug` 开关，用于实证检验离线回放对非确定性噪声的敏感度以及对业务回归缺陷的拦截能力。
