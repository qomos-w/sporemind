# plugin-spike — T1 subprocess framing spike

Gating gate for the Native Plugin dual-transport architecture
([[Native Plugin 双模式传输架构]]). Verifies that a "hello plugin" spawned
as an **independent OS process** can run the full round trips over
stdin/stdout duplex pipes with length-prefix framing:

```
[4-byte big-endian length] [1-byte message type] [payload bytes]
```

Message types: `0x01 invoke-req`, `0x02 invoke-resp`, `0x03 reverse-req`,
`0x04 reverse-resp`, `0x05 log`, `0x06 error`.

Run:

```
go test ./cmd/plugin-spike/ -v
```

The test binary re-executes itself as the plugin process (env
`PLUGIN_SPIKE_PROCESS=1`); the child is a real separate OS process speaking
the framing protocol. No production package is imported or modified — the
capability gate and reverse dispatch here are standalone mirrors of
`pkg/actor/pluginhost/host_bridge.go` (Allows/Dispatch) and the wire shape in
`sporemind-plugin-sdk/bridge.go` (`{callID, JSON payload}`, `__host_error__`
envelope).

## What the tests prove

| Test | Question answered |
|------|-------------------|
| `TestForwardRoundTrip` | invoke-req → invoke-resp + `0x05 log` frame before resp + graceful unload via stdin EOF (child exit 0) |
| `TestReverseCallRoundTrip` | gating gate: invoke in flight, host receives interleaved reverse-req → dispatch → reverse-resp → invoke-resp, verified by exact event order. **No deadlock.** |
| `TestReverseCallCapabilityDenied` | per-plugin capability gate on the IPC path: denied reverse-call → `{"__host_error__": ...}` reverse-resp → error surfaces inside invoke-resp |
| `TestLargePayloadsNoDeadlock` | 1 MiB frames in both directions (forward + reverse), far beyond pipe buffer (4 KiB Windows / 64 KiB Linux): no write/write deadlock |
| `TestConcurrentInvokesSerialized` | 8 goroutines × 10 invokes (each with a reverse call): per-process invoke mutex serialization is sufficient, no cross-talk, no request-id multiplexing needed |
| `TestPluginCrashDetectedMidInvoke` | killing the child mid-invoke unblocks the host read via pipe EOF → error, no hang (crash isolation) |
| `TestInvokeTimeoutKillsPlugin` | "超时即 kill+respawn": invoke budget expiry kills + reaps the child and a fresh spawn works |

## Verdict (conclusion for [[T1 Spike: subprocess framing 正向+reverse 往返]])

- **Framing 无死锁**：单根 duplex pipe，两方向各只有一个 writer/reader，
  同步消息流 host 等 invoke-resp 时收到 reverse-req → dispatch → 写
  reverse-resp → 继续等，完整往返通过。
- **串行化够用**：per-process invoke 互斥锁（`processTransport.invokeMu`）
  使并发 host goroutine 安全，初版无需 request-id multiplexing；帧头不引入
  额外字段，协议保持简单。
- **附带验证**：能力门禁（Allows 语义）、`__host_error__` 编码、
  崩溃检测（EOF）、超时 kill+respawn 均在 IPC 路径可行；`0x05 log` 帧
  直接替代 PluginLog FFI 轮询。