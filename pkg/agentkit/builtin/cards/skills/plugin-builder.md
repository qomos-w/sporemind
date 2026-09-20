---
id: skill:plugin-builder
type: skill
name: plugin-builder
description: Build and integrate a Plugin using the distributed sporemind-plugin-sdk, without requiring the sporemind host source. Use when native ABI or an existing native library is required.
when_to_use: |
  - The user wants to build a plugin (C-shared library) for in-process execution.
  - The user mentions "native ABI", "plugin", "c-shared", "plugin-sdk", or "PluginInvoke".
  - The user wants to reuse an existing Go library as a host plugin.
  - The user asks to load, reload, or unload a plugin artifact.
  - The user encounters "sporemind-plugin-sdk" or needs to author a plugin manifest.
context: inline
---

# Plugin Builder

## 1. 概述

Plugin 有两种运行传输，同一份 SDK 代码两用：

- **subprocess（dev 默认）**：编译为零-cgo 独立可执行文件，经 stdin/stdout 帧协议（`[4-byte BE length][1-byte msg type][payload]`，0x01 invoke-req / 0x02 invoke-resp / 0x03 reverse-req / 0x04 reverse-resp / 0x05 log / 0x06 error / 0x07 reverse-chunk 流式中间块，按 correlation callID 关联、以 0x04 终结）与宿主通信。入口是生成的 `main_run.gen.go`（`//go:build !cgo`，调 `sdk.RunProcess()`）。构建 mode 缺省即 subprocess。除帧协议外，该进程还自带 **HTTP listener**（`sdk.ServeHTTP`）服务面板数据面后端：静态资源、`POST /invoke/{id}`、`GET /events` SSE——面板 iframe 经宿主网关路由 `/plugin/{id}` 反向代理到此 listener（附网关令牌头），不经宿主 bridge 中继数据，见 §4.9.3。
- **in-process（release）**：编译为 C-shared library（`-buildmode=c-shared`，`.dll`/`.so`/`.dylib`），宿主 `dlopen`/`LoadLibrary` 加载，七个 `//export` 符号由 release 构建在分期 cgo shim（`main_cgo.gen.go`）自动生成，不手写。

适用场景：
- 需要 native ABI 直接调用能力
- 复用现有 Go native 库，无需 IPC 或子进程
- first-party 可信代码在宿主进程内执行（当前仅 first-party；第三方 NO-GO）

在线权威文档：`appmanager.dev_guide`（工作流 / 安全约束 / 常见错误）。本卡是其 SDK API 参考补充，两者命名已对齐。

## 2. SDK 边界

开发者基于**分布式** `sporemind-plugin-sdk` 模块工作，不依赖 sporemind host 源码。

```
import sdk "github.com/qomos-w/sporemind-plugin-sdk"
```

- **禁止** import host 内部 `pkg/...` 路径。
- SDK 是独立 Go module，**唯一外部依赖是 `github.com/ebitengine/purego`**（公开可拉取）；不需要本地 `spore` 仓库或任何 `replace github.com/qomos-w/spore` 指令。模板脚手架把 SDK 整模块复制进 app 的 `vendor-sdk/`（go.mod replace 指向 `./vendor-sdk`），app 因此完全自包含；非模板 app 的 replace 由宿主解析（dev 检出或 release 解压缓存），可随时用 `appmanager.sdk_vendor` 转为自包含布局。

## 3. 标准工作流（codegen，推荐）

SDK 手写 `main.go` + manifest 只用于特殊场景。标准链路以 `.appdef` 为唯一声明源，五步循环：

1. **写 `<name>.appdef`** — 声明 app{}（id/name/version/namespace 四字段必填）、callable（request/response 命名 struct 引用、effect、toolName、service、timeout、**expose**、**watch**）、entrypoint、event、bundle、`optional free_agent {}` 块。callable id 限 `[A-Za-z_][A-Za-z0-9_]*`（拒绝点号）。`expose: frontend|agent|both`（缺省 both）控制 callable 的消费面：agent 仅 LLM 工具面、不进插件 HTTP listener（前端 `/invoke` 对其 404）；frontend 仅在投影时被排除出 agent 工具注册。`watch: [event_id, ...]` 声明该 callable 缓存结果会因哪些事件失效——每项必须引用同 .appdef 声明的 event（悬空引用 = 校验错误），codegen 据此生成 `useXxxList()` hooks，manifest_consistency gate 抓 Watch 漂移。
2. **`appmanager.dev_generate`** — 生成八产物 + `go.mod`：`main.gen.go`（仅注册入口，cgo-free）、`main_run.gen.go`（subprocess 入口，`//go:build !cgo`，调 `sdk.RunProcess()` 并启动 `sdk.ServeHTTP("127.0.0.1:0")`（网关数据面的后端 listener））、`server.gen.go`（HTTP 路由分发表：每个 frontend 暴露面 callable（expose: frontend|both）一个 `sdk.RegisterHTTPHandler`，package init 注册；expose: agent 刻意不服务）、`handlers.go`（可编译存根，agent 唯一实现文件：零值响应 + 同名请求字段回显；声明 watch 事件的 effect: "mutate" callable 自动对每个 watched event 调 `sdk.EmitEvent`（nil payload，TODO 换真实 payload）——无 ErrNotImplemented 占位）、`app.manifest.json`、`schemas_gen.go`、`app.descriptors.json`（仅当所有 callable schema 解析为已声明 struct 时写入；供协议注册与 LLM 工具 schema）、`client.gen.ts`（HTTP 客户端：async appBase() 网关基路径闸（await `window.__sporemindAppBaseReady` 再拼 URL）、fetch POST `{base}/invoke/{id}` 类型化 invoke wrapper、共享自动重连 `EventSource({base}/events)` + 按 kind 过滤的类型化 `on<Event>` 订阅、watch callable 的框架无关 `useXxxList()` hooks（立即 fetch + watched SSE 事件到达即 refetch）、`uploadFile`/`streamVideo` 大媒体 helper）。模板脚手架同时把 SDK 整模块复制进 `vendor-sdk/`（go.mod replace 指向 `./vendor-sdk`），app 完全自包含、可在无 sporemind 检出的机器上构建；存量 app 用 `appmanager.sdk_vendor` 转换，replace 已指向 `./vendor-sdk` 而目录缺失时 dev_generate 自动重新物化。
3. **实现 `handlers.go`** — 填 `handleXxx(req sdk.Request) (sdk.Response, error)` 存根。**handler 签名无 Context 参数**：宿主调用走 `sdk.ActiveHost()`（subprocess 注入的 IPC host / c-shared FFI bridge host），日志走包级 `sdk.Log(level, format, ...)`（best-effort，无活跃 state 时丢弃）——`ctx.Host()`/`ctx.Log` 仅在 OnLoad 闭包内可用。
4. **`appmanager.dev_gate`** — 四道 gate：build / stub_filling / manifest_consistency / coverage。
5. **`appmanager.register_project`** — 编译 + 校验 + 装载 + 注册（inline 重跑四 gate）。热更新走 `appmanager.reload_project`（同一循环）。注册后开面板验证：`appmanager.open_view {Id}` 挂载视图，配合 `pluginhost.plugin_dom` / `pluginhost.plugin_logs` 观察前端渲染与日志。

目录根部的非源码文件（index.html、icon.png、styles.css 等）隐式打包为资产（经宿主 `/plugin/{id}/<name>` 服务），插件进程自己也从应用目录经 `GET /` 服务这些静态文件——面板经网关 `/plugin/{id}/{route}` 代理加载它们。

## 4. SDK 公开 API 参考

### 4.1 sdk.Plugin

```go
type Plugin struct {
    Manifest       Manifest
    OnLoad         func(ctx Context) error
    OnUnload       func(ctx Context) error
    OnConfigChange func(ctx Context, config json.RawMessage) error
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `Manifest` | `Manifest` | 插件身份、能力、callable、入口声明 |
| `OnLoad` | `func(Context) error` | 加载回调，在此注册 callable handler。返回 error 则加载失败 |
| `OnUnload` | `func(Context) error` | 卸载回调，清理资源（handler 注销由 SDK 统一完成） |
| `OnConfigChange` | `func(Context, json.RawMessage) error` | 配置变更回调，`config` 为完整配置 JSON |

### 4.2 sdk.Context

```go
type Context interface {
    PluginID() string
    Manifest() Manifest
    Host() Host
    RegisterCallable(method string, h Handler)
    UnregisterCallable(method string)
    Log(level int, format string, args ...interface{})
}
```

| 方法 | 返回 | 说明 |
|---|---|---|
| `PluginID()` | `string` | 当前插件 ID（与 Manifest.ID 一致） |
| `Manifest()` | `Manifest` | 当前 Manifest 副本 |
| `Host()` | `Host` | Host 能力客户端，用于反向调用 host |
| `RegisterCallable(method, h)` | - | 注册 callable handler（按 pluginID 域隔离） |
| `UnregisterCallable(method)` | - | 注销 callable handler |
| `Log(level, format, args...)` | - | 写结构化日志。c-shared 模式进 ring buffer、宿主每次 invoke 后经 `PluginLog` 排水；subprocess 模式直接发 0x05 日志帧。**诊断首选通道**，不要用 `fmt.Println` |

### 4.3 sdk.Manifest

标准流程下 manifest 由 `.appdef` 生成（`app.manifest.json`），无需手写。手写仅用于无 codegen 场景（见 §6 附录）：

```go
type Manifest struct {
    ID              string       `json:"Id"`
    Name            string       `json:"Name"`
    Version         string       `json:"Version"`
    Runtime         string       `json:"Runtime,omitempty"`
    ProtocolVersion int32        `json:"ProtocolVersion,omitempty"`
    Namespace       string       `json:"Namespace,omitempty"`
    Permissions     []string     `json:"Permissions,omitempty"`
    Callables       []Callable   `json:"Callables,omitempty"`
    Entrypoints     []Entrypoint `json:"Entrypoints,omitempty"`
    Bundles         []Bundle     `json:"Bundles,omitempty"`
}
```

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | `string` | `"Id"` | 全局唯一 ID，如 `"com.example.hello"` |
| `Name` | `string` | `"Name"` | 人类可读名称 |
| `Version` | `string` | `"Version"` | 语义化版本 |
| `Runtime` | `string` | `"Runtime"` | plugin 必须 `"native"`（`Normalize` 默认填充） |
| `ProtocolVersion` | `int32` | `"ProtocolVersion"` | 当前为 `2`（v2-only 帧协议，见 `pkg/pluginhost/contract.go` `HostProtocolVersion`） |
| `Namespace` | `string` | `"Namespace"` | 命名空间（默认 = ID） |
| `Permissions` | `[]string` | `"Permissions"` | 请求的能力，取值见 §4.6；声明即授权——manifest 声明的能力即运行时权限集 |
| `Callables` | `[]Callable` | `"Callables"` | 声明的 callable（Id/RequestSchema/ResponseSchema 必填） |
| `Entrypoints` | `[]Entrypoint` | `"Entrypoints"` | UI 入口 |
| `Bundles` | `[]Bundle` | `"Bundles"` | 可发现工具组 |

`Normalize()` 填默认值；`ToAppManifest()` 转为宿主校验用的 `AppManifest`（校验 Id/Name/Version、Runtime=native、callable 完整性、toolName 合法性）。

### 4.4 sdk.Callable / sdk.Bundle

```go
type Callable struct {
    ID             string `json:"Id"`
    RequestSchema  string `json:"RequestSchema"`
    ResponseSchema string `json:"ResponseSchema"`
    Effect         string `json:"Effect,omitempty"`
    Service        string `json:"Service,omitempty"`
    ToolName       string `json:"ToolName,omitempty"`
    Streaming      bool   `json:"Streaming,omitempty"`
    TimeoutMs      int64  `json:"TimeoutMs,omitempty"`
    Expose         string `json:"Expose,omitempty"` // frontend | agent | both（空=both）
    Watch          []string `json:"Watch,omitempty"` // 失效该 callable 缓存的事件 id 列表
}
```

- `ID` 本地名，宿主自动 namespace 化；`.` 生成 `main.gen.go` 里的 `toolName`（缺省为点号折叠成连字符的 callable id，LLM 工具名限 `[a-zA-Z0-9_-]`）。
- `Effect` / `Service` / `Streaming` / `TimeoutMs` / `Expose` / `Watch` 对齐发现目录的运行时声明。`Expose` 控制消费面（见 §3 步骤 1）；`Watch` 是缓存失效关联（codegen 生成 `useXxxList()` hooks）。

```go
type BundleTool struct { CallableID, Effect, Service, ToolName string; Stream bool }
type Bundle struct { Title, Description string; Tools []BundleTool }
```

Bundle 是 app 暴露为可发现协议资产的工具组；bundle 卡无 icon 会在 composer 挂载徽章中隐藏（icon 用 `appmanager.icon_names` 查合法名）。

### 4.5 sdk.Entrypoint

```go
type Entrypoint struct {
    Kind  string `json:"Kind"` // view
    ID    string `json:"Id"`
    Title string `json:"Title"`
    Route string `json:"Route,omitempty"`
}
```

- `Kind` 当前规范值为 `"view"`（宿主 shell 内 plugin iframe 面板）。`.appdef` 语法：`entrypoint view <id> { title: ...; route: index.html }`（kind 是块标签，id 紧随其后）。
- `Route` 指向应用目录内的资产文件名（如 `index.html`）。经宿主网关路由 `/plugin/{id}/{route}` 反向代理到**插件进程自己的 listener**（`GET /` 静态文件，无 CORS；网关注入 bootstrap 片段并提供 appBase）；打包资产同样经 `/plugin/{id}/<name>` 服务。面板经 `POST /invoke/{id}` 调 callable、经 `GET /events` SSE 收事件（见 §4.9.3），宿主 bridge 退化为纯管理面（console 捕获 / DOM 快照 / openView / theme / locale 下发），不再中继数据。locale：宿主当前语言（BCP47，如 `zh-CN`）经 bootstrap 与 `sporemind:locale-update` 落到面板 `<html lang>`，面板读 `window.sporemind.locale`、用 MutationObserver 监听 `lang` 变化。

### 4.6 Permission 常量（宿主能力目录）

```go
const (
    PermLLMInvoke      = "llm.invoke"      // LLM 完成/对话
    PermFSRead         = "fs.read"         // 读工作区文件
    PermFSWrite        = "fs.write"        // 写工作区文件
    PermShellExec      = "shell.exec"      // 执行 shell 命令
    PermConfigRead     = "config.read"     // 读插件配置
    PermProviderRead   = "provider.read"   // 读 provider 配置
    PermAggregatorRead = "aggregator.read" // 读 aggregator 配置
    PermAppState       = "app.state"       // per-app KV 存储（State() 客户端）
    PermAppEmit        = "app.emit"        // 发布 appdef 声明的事件（声明 event 块时 codegen 自动派生）
)
```

**点号命名，冒号式（`llm:call`、`project:read` 等）是旧词表，宿主一律拒绝。** 目录之外的任何能力串在注册校验时即被拒；manifest 声明的 `Permissions` 是唯一的运行时权限集（无 host allowlist，无 consent round-trip）。

### 4.7 sdk.Request / sdk.Response / sdk.Handler

```go
type Request = gen.PluginSdkRequest   // { PluginId string; CallId string; Payload []byte;
                                       //   optional RequestId, SessionId string; CallSeq int64 }
type Response = gen.PluginSdkResponse // { Payload any }
type Handler func(req Request) (Response, error)
```

- `Payload` 为原始 JSON 请求体；`SessionId` 仅为审计/关联元数据，**不是授权依据**（授权在宿主调用插件前建立）。
- `Response.Payload` 序列化为 JSON 返回；error 经 framed error envelope 传递。handler panic 被 SDK 捕获转为错误，不会杀死宿主进程。
- 便捷函数：顶层 `RegisterCallable/UnregisterCallable/Dispatch`（测试与无生命周期场景用；正式注册走 `ctx.RegisterCallable`）。

### 4.8 sdk.Host 接口

在线版：`appmanager.dev_guide`（`Topic: "host_api"`）。

**获取方式**：handler 内用 `sdk.ActiveHost()`（返回当前 host 客户端，handler 签名无 Context 参数）；OnLoad 闭包内用 `ctx.Host()`。日志：handler 内用包级 `sdk.Log(level, format, ...)`，OnLoad 内用 `ctx.Log(...)`。

```go
type Host interface {
    Invoke(callID string, payload any) ([]byte, error)
    InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error)
    Config() ConfigClient
    LLM() LLMClient
    Project() ProjectClient
    Providers() ProviderClient
    Aggregators() AggregatorClient
    State() StateClient
}
```

| 方法 | 返回 | 说明 |
|---|---|---|
| `Invoke(callID, payload)` | `([]byte, error)` | 调用宿主 callable（需对应能力授权） |
| `InvokeStream(callID, payload, onChunk)` | `([]byte, error)` | 流式调用：中间块经 `onChunk(raw []byte)` 按到达序回调，返回值为终值（见 4.9.1） |
| `Config()` | `ConfigClient` | 配置客户端（`config.read`） |
| `LLM()` | `LLMClient` | LLM 能力客户端（`llm.invoke`） |
| `Project()` | `ProjectClient` | 项目文件客户端（`fs.read` / `fs.write`） |
| `Providers()` | `ProviderClient` | Provider 配置只读（`provider.read`） |
| `Aggregators()` | `AggregatorClient` | Aggregator 配置只读（`aggregator.read`） |
| `State()` | `StateClient` | per-app KV 存储（`app.state`） |

### 4.9 子客户端接口与 Host CallID

| 客户端 | 方法 | Host CallID |
|---|---|---|
| `ConfigClient` | `Get(scope, key)` | `config.get` |
| `LLMClient` | `Complete(payload)` / `Chat(payload)` | `llm.complete` / `llm.chat` |
| `LLMClient` | `CompleteStream(payload, onChunk)` / `ChatStream(payload, onChunk)` | `llm.complete` / `llm.chat`（流式） |
| `ProjectClient` | `ReadFile(path)` / `WriteFile(path, content)` | `project.read_file` / `project.write_file` |
| `ProviderClient` | `List()` / `Get(id)` | `provider.list` / `provider.get` |
| `AggregatorClient` | `List()` / `Get(id)` | `aggregator.list` / `aggregator.get` |
| `StateClient` | `Get(key)` / `Set(key, value)` / `Delete(key)` | `state.get` / `state.set` / `state.delete` |
| 包级 `sdk.EmitEvent(kind, payload)`（OnLoad 内 `ctx.EmitEvent`） | 发布一个 appdef 声明的事件 | `app.emit` |

`StateClient` 宿主侧注入调用方插件身份，客户端只传 key/value——这是 app 持久化唯一正道（TOTP 体检多轮验证）。

### 4.9.1 LLM 流式消费（CompleteStream / ChatStream）

handler 内逐 chunk 消费 LLM 输出，而非等聚合终值。payload 与 unary 形式完全相同（model+provider 组成 unit 二元组，缺省自动选池；可带 `tools` + `tool_choice` 强制模型以工具调用收尾——裸工具名强制该工具、`"required"` 强制任一工具，此时终值 `Text` 为空、调用清单在 `ToolCalls`，由插件自行执行并把 assistant `tool_use` / user `tool_result` 块回显进 `messages` 续循环）。终值返回 `{Text, Usage?, Reasoning?, ToolCalls?}`（`ToolCalls` 项为 `{Id, Name, Arguments}`，Arguments 是原始 JSON 字符串）：

```go
resp, err := sdk.ActiveHost().LLM().CompleteStream(
    map[string]any{"prompt": "...", "model": "...", "provider": "..."},
    func(c sdk.LLMChunk) error {
        switch c.Kind {
        case "text_delta":     // c.Text 是增量文本，逐块渲染/转发
        case "reasoning_delta":
        case "tool_use_start": // data {tool_use_id, name}：一次工具调用开始
        case "tool_use_input_delta": // data {tool_use_id, input_delta}：参数 JSON 片段
        case "tool_use_complete": // data {tool_use_id, name, input}：参数拼装完成
        case "stop":           // data {stop_reason}："tool_use" 表示以工具调用收尾
        case "usage":          // c.Usage 是原始 JSON（InputTokens/OutputTokens）
        default:              // c.Data 携带未知 kind 的原始 JSON（前向兼容），忽略即可
        }
        return nil // 返回非 nil 错误 ⇒ 中止消费，错误上抛给 handler
    })
```

语义要点：

- **通用信封**：0x07 块的 wire 形状是通用信封 `{"kind": <kind>, "data": <JSON>}`——宿主只路由不解释；SDK 把 LLM 词表解码成 `LLMChunk{Kind, Text, Usage, Data}`（`text_delta`/`reasoning_delta` 的 data 是 `{"text": "..."}` 映射到 Text，`usage` 的 data 是用量对象映射到 Usage，`tool_use_*`/`stop` 的 data 是上述结构化对象，未知 kind 的 data 原样放 Data）。可流式 callID 集合是宿主侧目录（当前只有 `llm.complete`/`llm.chat`），未来新流式 callable 走同一条路。
- **deepseek 思考模型注意**：deepseek-v* 混合模型默认开思考，思考态下强制 `tool_choice` 会被 provider 400 拒绝；不带 `reasoning_effort` 的强制调用由宿主自动注入 thinking-disabled。
- **有序**：chunk 按生成顺序回调，全部先于终值返回（0x07 帧按 callID 关联，结构性保证）。
- **降级透明**：inprocess/FFI 传输或旧宿主 ⇒ 零中间块、终值正常返回，同一份代码无需分支。要判断是否真的流式，数 onChunk 回调次数即可。
- **预算**：reverse 调用继承外层 callable 的预算（含 .appdef 里声明的 `timeout:`）减去固定 headroom；慢首块模型请显式声明 `timeout: 120s+`，不要赌 30s 默认值。
- **abort**：onChunk 返回错误立即停止消费；subprocess 传输下 SDK 同时向宿主发 0x09 reverse-cancel 帧请宿主中止上游 LLM 派发（停止计费、释放 reverse slot），FFI/inprocess 传输降级为仅本地弃流（宿主侧调用仍会完成，终帧清理 waiter），错误以 Go error 形式上抛。
- **主动中止（ctx）**：需要用户点停止/超时主动切断流式调用时，把宿主类型断言成 `sdk.CanceledHost` 调 `InvokeStreamCtx(ctx, ...)`（或生成产物里对应的 `StreamXxxCtx` 变体）：ctx 到期立即返回 ctx.Err 并同样发送 0x09 帧，宿主侧取消整条上游 LLM 流。宿主不实现 `CanceledHost` 时（FFI 传输）ctx 只影响本地等待。这是 LLM 长任务可取消的唯一通道；Invoke/InvokeStream 无 ctx 的历史形态已视为只适配无取消场景。
- **能力门**：与 unary 相同，走 `llm.invoke` 能力（manifest `permissions` 声明）。

### 4.9.2 事件发布（app.emit）

`sdk.EmitEvent("todo_changed", payload)`（或 codegen 生成的 `EmitTodoChanged(payload TodoChanged)` 包装）沿**两条独立路径**投递 appdef `event <id> { payload: T }` 声明的事件：

1. **本地 SSE**：事件扇出到插件 HTTP listener 上所有已连接前端（`GET /events`），面板实时更新、零宿主往返；best-effort，无 listener 运行时为 no-op，不影响返回值。
2. **宿主总线**：经 `app.emit` host call 转发——pluginhost 注入调用方插件身份后转发 `appmanager.plugin_emit`，appmanager 对照 manifest 校验事件已声明（未声明的事件被拒）并以 `app_event` bus kind 广播，`listen app_event` 的应用（含其他 app）收到；host call 失败返回 error。

声明 `event` 块时 codegen 自动派生 `app.emit` 能力，无需手写 permissions；`main.gen.go` 生成 `Emit<ID>` 包装，`client.gen.ts` 生成 `on<ID>` 订阅包装（共享 `EventSource('/events')`，按 SSE `event:` 字段过滤，返回取消订阅函数）。无 payload 事件（`event <id> { }`）发布 nil。事件 `listen`（入站）见 appdef 文档：`OnXxx` 处理器经保留 callID `__event__:<kind>` 投递。

### 4.9.3 插件 HTTP 前端面（网关代理 / HTTP server / SSE / 鉴权）

数据面：面板 iframe 加载宿主网关路由 `/plugin/{id}/{route}?v={generation}`（按 reload generation 失效缓存，HTML 响应 `Cache-Control: no-store`），网关把每个 `/plugin/{id}/...` 请求反向代理到插件进程 HTTP listener（`sdk.ServeHTTP`）并附加 per-app 网关令牌头（`X-Gateway-Token`，`sdk.GatewayTokenHeader`）——面板文档永远不知道子进程 origin 与 session secret（backendUrl/cookieToken 是代理内部细节）。宿主 bridge 退化为纯管理面（console 捕获、DOM 快照、openView、theme、locale 下发），不中继数据。**无 binary codec**：invoke/event 线格式是纯 JSON，大媒体走 HTTP 原生 binary。

**Bootstrap 握手**：网关向每个 HTML 响应注入单一来源 bootstrap 片段（`sdk.BridgeBootstrapSnippet`，宿主网关从 SDK 导入同一常量）。片段解析时向 parent postMessage bootstrap 请求，宿主回复 app 基路径，片段据此 resolve `window.__sporemindAppBaseReady`（3 秒未答复回退 `""`，供独立浏览器直开）。生成的 client.gen.ts 一律 `await appBase()` 再拼 URL——消除首取 404 竞态（`/invoke/{id}` 早于 `/plugin/{id}` 基路径已知）。

**沙箱前端契约（面板跑在 sandboxed iframe 内）**：面板 = 沙箱 iframe（无 `allow-modals` / `allow-popups`），写前端必须守这些约束：
- **禁用原生对话框**：`window.alert/confirm/prompt` 被浏览器静默抑制（`confirm()` 直接返回 `false`、不渲染），不能用来做确认——把确认/选择做成面板内 HTML（`<dialog>` 或 iframe 内定位元素），绝不拿 `confirm()` 返回值决定动作。
- **禁弹窗与顶层跳转**：`window.open` / `target="_blank"` 被拦；跨源访问 `window.top/parent/opener` 抛异常；不要用 `location.href` 跳转，改为 iframe 内 hash/history 路由。
- **持久状态不走 web storage**：`localStorage`/`sessionStorage` 禁用，durable UI 状态经后端 callable（`app.state`）落库。
- **指针释放由宿主保证**：注入片段在 capture 阶段对 `pointerdown` 自动 `setPointerCapture`，按下后拖出 iframe 边界释放（含宿主 chrome / 原生窗口之上）仍会把 `pointerup`/`mouseup`/`click` 投递到按下元素——直接写普通 `pointerdown`/`pointerup` 处理即可，**不要**手写 window 级 mouseup/blur 兜底或"拖拽卡死"看门狗。`input/textarea/select/[contenteditable]` 目标除外（浏览器原生管理其按压语义）。插件自己调 `setPointerCapture` 则以后者为准（last-write-wins）。
- **只经生成客户端访问宿主**：`await appBase()` 后 fetch；不要硬编码宿主 origin/port/绝对 URL；静态资源同源从应用目录加载。
- 浮层限制在 iframe 视口内——iframe 即整个世界，宿主 chrome 与原生浏览器窗口在面板内不可达。

```go
func ServeHTTP(addr string, opts ...ServeHTTPOption) (*HTTPServer, error)
func WithStaticDir(dir string) ServeHTTPOption          // 缺省 "." = 应用目录
func RegisterHTTPHandler(callableID string, h HTTPHandler) // server.gen.go 在 init 中调用
func UnregisterHTTPHandler(callableID string)
type HTTPHandler func(payload json.RawMessage) (any, error) // raw JSON body → JSON 响应
```

路由：

| 路由 | 语义 |
|---|---|
| `POST /invoke/{callableId}` | HTTPHandler 分发；JSON body 进、JSON 响应出；handler error → 500 `{"error":...}`；未注册 callable → 404 |
| `GET /events` | `text/event-stream` SSE；`sdk.EmitEvent` 的本地扇出目的地；`event:` 字段 = 事件 kind |
| `GET /` | 应用目录静态文件（同源、无 CORS） |

`HTTPServer` 暴露 `Addr()`（绑定的 host:port，`":0"`  ephemeral 绑定时用）与 `Shutdown(ctx)`（幂等；unload 时自动调用，reload 不留悬挂端口）。`ServeHTTP` 运行中幂等（第二次调用返回现有 server，不重复绑定）。

启动两条路径互不冲突（先到先得）：生成的 `main_run.gen.go` 开机即 `sdk.ServeHTTP("127.0.0.1:0")`（端口写日志）；宿主也可以经 OnLoad config 下发地址，SDK 在 app 的 `OnLoad` 返回后自动启动并把绑定地址经 OnLoad 响应回报 `{"httpAddr": "<bound>"}`。

**鉴权**（`/invoke` 与 `/events`）：经网关代理的请求携带网关令牌头；直连同源打开走 cookie：

```go
const SessionCookieName = "spore_session" // HttpOnly, SameSite=Lax
func SetSessionSecret(s string)   // OnLoad config 自动应用；hex 或 raw；空 = 关闭鉴权（dev mode）
func MintSessionToken(secret []byte, sessionID string) string // sessionId + "." + hex(HMAC-SHA256(secret, sessionId))
type LoadConfig struct {          // OnLoad config JSON（宿主 → SDK）
    HTTPAddr      string `json:"httpAddr,omitempty"`      // 非空则自动启动 listener
    SessionSecret string `json:"sessionSecret,omitempty"` // 非空则强制 cookie 鉴权
}
```

空 secret → 全部放行（dev mode）；非空 → cookie 缺失/非法返回 401。同源 cookie 意味着 `<video>`/`<audio>`/`<img>` 与 fetch 自动携带。

**大媒体**（HTTP 原生 binary，零编解码）：上传用 raw request body（client.gen.ts `uploadFile(url, file, {onProgress})`，需要进度时走 XHR）；播放用同源 `<video src>`/`<audio src>`/`<img src>`；边下边播用 `streamVideo(url, videoEl, mimeType)`（chunked + MSE）。流式 callable 在 HTTP 上退化为 unary（中间 chunk 丢弃，只回终值；进度应建模为 SSE 事件）。`expose: agent` 的 callable 不注册进 HTTP listener（404）。

### 4.10 全局寄存器与诊断

```go
func Register(p *Plugin)      // init() 中调用；生命周期回调之前
func ActivePlugin() *Plugin   // 当前注册的 Plugin，nil 表示未注册
func ActiveHost() Host        // 当前 host 客户端；未加载返回空 bridge
func State() *pluginState     // 活动插件诊断句柄（pluginID/host/ctx）
```

### 4.11 生命周期与 ABI Helpers

所有函数均以 `unsafe.Pointer` 操作原始内存，由 C 导出符号包装调用。

```go
func WriteManifest(buf unsafe.Pointer, n int32) int32
```

将已注册 plugin 的 Manifest JSON 写入 `buf`（容量 `n`）。返回写入字节数（不含 NUL），失败 -1。

```go
func HandleOnLoad(pluginID unsafe.Pointer, config unsafe.Pointer) int32
func HandleOnUnload(pluginID unsafe.Pointer) int32
func HandleOnConfigChange(pluginID unsafe.Pointer, config unsafe.Pointer) int32
```

生命周期三回调：加载（初始化状态 + `Plugin.OnLoad`）、卸载（`Plugin.OnUnload` + 统一注销 callable；重复调用安全返回 0）、配置变更（NUL-terminated config → `json.RawMessage`）。均 0 成功 / -1 失败。

```go
// Deprecated: Use HandleInvokeFramed for length-prefixed binary ABI.
func HandleInvoke(pluginID, callID, req, res unsafe.Pointer, resLen int32) int32
```

遗留 NUL-terminated ABI，仅为兼容保留。

```go
func HandleInvokeFramed(reqPtr unsafe.Pointer, reqLen uintptr, respPtr unsafe.Pointer, respCap uintptr, respLen *uintptr) int32
```

**官方 ABI**。入参 `reqPtr`/`reqLen` 请求帧缓冲，`respPtr`/`respCap` 响应缓冲，出参 `respLen` 实际写入数。返回 0 成功 / -1 失败。

请求帧 payload 为 JSON envelope：

```json
{"callable":"greet","payload":{"name":"world"}}
```

响应帧 payload 为 `{"payload":{...}}`；错误时 `{"error":"callable not found"}`。

```go
func HandleSetHostBridge(bridge unsafe.Pointer) int32
func HandlePluginLog(buf unsafe.Pointer, n int32) int32
```

- `HandleSetHostBridge`：宿主经 `PluginSetHostBridge` 注入反向调用回调指针，SDK 用 `purego.RegisterFunc` 转为 Go 函数。
- `HandlePluginLog`：宿主排水日志 ring buffer（配合 `ctx.Log`），返回写出的日志载荷字节数。

### 4.12 ABI 帧格式

```
┌──────────────────────┬──────────────────────────┐
│  4-byte BE length    │       payload            │
│     (uint32)         │    (length 字节)          │
└──────────────────────┴──────────────────────────┘
```

- 前 4 字节大端 `uint32` 长度 + payload（JSON）
- `abiHeaderSize = 4`；`encodeFrame` / `decodeFrame` 为对应编解码助手

### 4.13 C 导出符号契约（in-process / release）

plugin 以 in-process 模式加载时必须导出以下 **7 个**符号，host 通过 `dlsym` / `GetProcAddress` 查找：

| 导出符号（//export） | 功能 |
|---|---|
| `PluginManifest` | 读取 manifest JSON 到 buf |
| `PluginOnLoad` | 加载回调 |
| `PluginOnUnload` | 卸载回调 |
| `PluginOnConfigChange` | 配置变更回调 |
| `PluginInvoke` | 官方 length-prefixed framed invoke |
| `PluginSetHostBridge` | 注入 host 反向调用函数指针 |
| `PluginLog` | 排水插件日志 ring buffer |

codegen 流程下 `main.gen.go` 已生成全部导出，agent 零编辑；手写场景见附录。

## 5. Host 端接口参考

### 5.1 PluginHost Actor 注册的 Callable（16 个）

| Callable | 权限 | 说明 |
|---|---|---|
| `pluginhost.list_plugins` | Public | 列出已加载 plugin 状态与产物元数据 |
| `pluginhost.register_actor` | AdminOnly | 注册插件 actor 描述符 |
| `pluginhost.unregister_actor` | AdminOnly | 注销插件 actor 描述符 |
| `pluginhost.invoke` | Public | 向特定 plugin 的 callable 分发请求 |
| `pluginhost.artifact_load` | AdminOnly | 加载已验证 plugin artifact |
| `pluginhost.artifact_reload_prepare` | AdminOnly | 热重载阶段一：准备新 artifact（返回 Token） |
| `pluginhost.artifact_reload_commit` | AdminOnly | 热重载阶段二：提交切换 |
| `pluginhost.artifact_reload_abort` | AdminOnly | 热重载回滚：中止 |
| `pluginhost.artifact_unload` | AdminOnly | 卸载已加载 plugin |
| `pluginhost.assets_put` | AdminOnly | 推送打包资产（面板前端文件） |
| `pluginhost.assets_remove` | AdminOnly | 移除打包资产 |
| `pluginhost.state_get` | AdminOnly | 读 per-app KV（app.state 后端） |
| `pluginhost.state_set` | AdminOnly | 写 per-app KV |
| `pluginhost.state_delete` | AdminOnly | 删 per-app KV |
| `pluginhost.native_build` | AdminOnly | 源码编译为 plugin artifact |

agent 日常经 `appmanager.*`（dev_generate/dev_gate/register_project/reload_project/plugin_load/plugin_unload/invoke/...）间接使用以上能力，不直接调 `pluginhost.*` 管理面。

### 5.2 关键 Schema 结构

来源 `schemas/plugin._1996.spore` 与 `schemas/plugin.build._2210.spore`。

#### PluginAbi

| 字段 | 类型 | 说明 |
|---|---|---|
| `Name` | `string` | ABI 名称 `"spore-plugin"` |
| `Version` | `int` | ABI 版本号（1） |
| `Encoding` | `string` | 编码格式 `"binarycodec-v1"` |
| `InvokeSymbol` | `string` | 调用入口符号，默认 `"PluginInvoke"` |
| `ContractVersion` | `string` (optional) | 合约版本（"1"） |
| `Isolation` | `string` (optional) | 必须为 `"inprocess"` |
| `TrustClass` | `string` (optional) | 必须为 `"first_party"` |
| `Signer` | `string` (optional) | 签名者（SDK 默认 `sporemind.first-party`），非空 |
| `Capabilities` | `array<string>` (optional) | 能力列表 |

SDK 侧 `sdk.DefaultAbi()` 返回满足契约的默认值。

#### PluginInvokeReq

| 字段 | 类型 | 说明 |
|---|---|---|
| `Id` | `string` | 插件 ID |
| `Callable` | `string` | Callable 本地名（host 自动 namespace） |
| `Payload` | `bytes` | 请求 payload |
| `AgentId` / `Role` / `ProjectId` / `RequestId` | `string` (optional) | 审计与追踪 |
| `SessionId` | `string` (optional) | 仅审计/关联，非授权依据 |

#### PluginSdkRequest / PluginSdkResponse

见 §4.7（SDK Request/Response 即此二结构的别名）。

#### NativeBuildReq / NativeBuildResult

`NativeBuildReq`：`ProjectId`、`AppId` 必填；`EntryModule`（默认 `main.gen.go`）、`TargetOS`/`TargetArch`（默认本机）、`ArtifactHash`、`AgentId`、`RequestId` 可选。
`NativeBuildResult`：`Success`、`ArtifactPath`、`ArtifactHash`（SHA256 hex）、`Diagnostic`。

#### artifact_load / reload_* / artifact_unload / assets_*

- `PluginArtifactLoadReq`：`Manifest` + `Abi` + `ArtifactPath`；可选 `ArtifactHash`（设则强制校验）、`EntrySymbol`（默认 `"PluginInvoke"`）、`AgentId`、`RequestId`。
- reload_prepare 同 Load 入参，返回 `Token`；commit/abort 以 `Token` 操作。in-process（c-shared）库不可热换，reload 走 prepare→commit 序列（c-shared 存量阻塞为已知限制）。
- `PluginArtifactUnloadReq`：`PluginId`；返回 `Removed`（移除 handler 数）。
- assets_put/remove：面板资产推送/移除（codegen 注册链路自动调用）。

### 5.3 共享类型（schemas/app._1900.spore）

#### AppManifest

| 字段 | 类型 | 说明 |
|---|---|---|
| `Id` / `Name` / `Version` | `string` | 身份三件套 |
| `Runtime` | `string` | plugin 为 `"native"` |
| `ProtocolVersion` | `int` | 协议版本（2，v2-only 帧协议） |
| `Namespace` | `string` | 命名空间 |
| `Permissions` | `array<string>` | 能力请求（§4.6 词表） |
| `Schemas` / `Callables` / `Events` / `Projections` / `Entrypoints` / `Dependencies` | array | 声明面 |
| `Bundles` | `array<AppBundle>` (optional) | 工具组 |
| `AgentBinding` | `AppAgentBinding` (optional) | free_agent 声明映射；缺省则 agent 动作被拒 |
| `Security` | `AppSecurityPolicy` (optional) | 执行预算封套 |

#### AppEntrypoint

| 字段 | 类型 | 说明 |
|---|---|---|
| `Kind` | `string` | 当前规范值 `"view"` |
| `Id` / `Title` | `string` | 标识与显示名 |
| `Route` | `string` (optional) | 资产文件名（如 `index.html`） |
| `Zone` | `string` (optional) | 布局区域 |

#### AppCallableDescriptor

| 字段 | 类型 | 说明 |
|---|---|---|
| `Id` / `RequestSchema` / `ResponseSchema` | `string` | 必填三件套 |
| `Effect` / `Service` / `ToolName` | `string` (optional) | 运行时声明与 LLM 工具名 |
| `Streaming` | `bool` (optional) | 是否流式 |

#### AppSecurityPolicy（执行预算）

| 字段 | 类型 |
|---|---|
| `MaxInstructions` / `MaxDurationMs` / `MaxHostCalls` / `MaxOutputBytes` | `long` (optional) |

> 注意：宿主侧不再有 `SecurityPolicy`/`AllowCapabilities`；manifest 声明的 `Permissions` 是唯一的运行时权限集（声明即授权）。

## 6. 附录：无 codegen 手工插件（高级）

不需要 `.appdef` 工具链时，可手写完整 `main.go`。要点与旧文档的差异已按当前契约修正：

- Manifest 用 §4.3 字段（Id/Name/Version 必填，Runtime 默认 native）；Permissions 用 §4.6 点式常量。
- Entrypoint 用 `Kind: "view"` + 资产 route。
- 导出 **7 个** C 符号（§4.13，含 `PluginLog`）。
- 构建：`go build -buildmode=c-shared -o plugin-<id>.dll|so|dylib`，随后走 `pluginhost.native_build` / `artifact_load`（或经 `appmanager.register_project` 完整校验注册）。

最小骨架（节选，完整见 SDK `examples/hello`）：

```go
func init() {
    sdk.Register(&sdk.Plugin{
        Manifest: sdk.Manifest{
            ID: "com.example.hello", Name: "Hello Plugin", Version: "1.0.0",
            Permissions: []string{sdk.PermConfigRead},
            Callables: []sdk.Callable{
                {ID: "ping", RequestSchema: "PingReq", ResponseSchema: "PingResp"},
            },
            Entrypoints: []sdk.Entrypoint{
                {Kind: "view", ID: "hello", Title: "Hello", Route: "index.html"},
            },
        },
        OnLoad: func(ctx sdk.Context) error {
            ctx.RegisterCallable("ping", func(req sdk.Request) (sdk.Response, error) {
                return sdk.Response{Payload: map[string]string{"pong": "ok"}}, nil
            })
            return nil
        },
    })
}

//export PluginManifest
func PluginManifest(buf *C.char, n C.int) C.int { return C.int(sdk.WriteManifest(unsafe.Pointer(buf), int32(n))) }
//export PluginOnLoad
func PluginOnLoad(id, cfg *C.char) C.int { return C.int(sdk.HandleOnLoad(unsafe.Pointer(id), unsafe.Pointer(cfg))) }
//export PluginOnUnload
func PluginOnUnload(id *C.char) C.int { return C.int(sdk.HandleOnUnload(unsafe.Pointer(id))) }
//export PluginOnConfigChange
func PluginOnConfigChange(id, cfg *C.char) C.int { return C.int(sdk.HandleOnConfigChange(unsafe.Pointer(id), unsafe.Pointer(cfg))) }
//export PluginInvoke
func PluginInvoke(req *C.char, reqLen C.size_t, resp *C.char, respCap C.size_t, respLen *C.size_t) C.int {
    return C.int(sdk.HandleInvokeFramed(unsafe.Pointer(req), uintptr(reqLen), unsafe.Pointer(resp), uintptr(respCap), (*uintptr)(unsafe.Pointer(respLen))))
}
//export PluginSetHostBridge
func PluginSetHostBridge(bridge unsafe.Pointer) C.int { return C.int(sdk.HandleSetHostBridge(bridge)) }
//export PluginLog
func PluginLog(buf *C.char, n C.int) C.int { return C.int(sdk.HandlePluginLog(unsafe.Pointer(buf), int32(n))) }

func main() {}
```

## 7. 安全约束

仅 first-party / 签名（`sporemind.first-party`）/ in-process，第三方 native 不可用；声明即授权（manifest `Permissions` 是唯一运行时权限集，无 allowlist、无 consent）；同步调用进共享库后不可中断、30s 超时，库内 crash 影响宿主进程；生成文件（`main.gen.go` / `app.manifest.json` / `schemas_gen.go` / `client.gen.ts`）写保护、`handlers.go` agent 自有；前端 `/invoke` 与 `/events` 由 per-instance HMAC secret 派生的 `spore_session` cookie 把守（空 secret = dev mode 关闭鉴权）。完整清单见 `appmanager.dev_guide` 的 `security` topic。
