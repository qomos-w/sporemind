---
id: skill:spore-app-builder
type: skill
name: spore-app-builder
description: Build, register, test, and troubleshoot Spore Apps (Runtime="spore"). Use when the user wants to create a sub-app, write Spore Script, define callables/views/panels, or manage app lifecycle via AppManager.
when_to_use: |
  - The user says "create a spore app", "build a sub-app", "write a spore script", or "make a callable".
  - The user wants to define entrypoints (view/panel) or projections for a sub-app.
  - The user needs to register, invoke, reload, or unregister an app via AppManager.
  - The user asks about Spore Script syntax, AppManifest fields, or the app lifecycle.
  - The user wants to troubleshoot export validation, capability binding, or session errors.
context: inline
---

# Spore App Builder

构建、注册、测试和故障排查 Spore App（Runtime="spore"）的完整参考文档。

---

## 1. 概述

**Spore App** 是指 `Runtime="spore"` 的子应用,使用 **Spore Script**（一种轻量级表达式语言）编写,以 `export fun` 导出 callable,运行在 `SporeApp` actor 内部,由 `AppManager` actor 管理全生命周期。

与 Native Plugin（Runtime="native",通过 PluginAbi/FFI 加载原生共享库）不同,Spore App 完全运行在 Sporemind 宿主内部的脚本沙箱中,安全性通过 `AppSecurityPolicy` 和 capability 声明机制保障。

```
AppManager (actor)
  ├── 管理注册/注销/审计
  ├── 转发 invoke/cast/emit 到 SporeApp actor
  └── Session 管理 (iframe 前端绑定)
        │
        v
SporeApp (actor, 每个 app 一个实例)
  ├── 加载 Spore Script 模块
  ├── 绑定 capability host 函数
  ├── 验证 exports (validateExports)
  ├── 调用 export fun
  └── 管理 app 状态 (state)
```

---

## 2. Spore Script 语法

Spore Script 是 Spore App 的模块源代码语言,每个模块是一个文本文件,扩展名 `.spore`。

### 模块结构

```
import TypeName from "app"
export fun functionName(param1: type1, ...): returnType = expression
```

### 关键语法

| 语法 | 说明 | 示例 |
|------|------|------|
| `import Type from "app"` | 从 app schema 导入一个结构体类型 | `import DemoEchoReq from "app"` |
| `export fun name(params): ret = expr` | 导出一个 callable 函数 | `export fun ping(): string = "pong"` |
| `export fun name(params): ret` | 多语句函数（需 `expr` 表达式） | 见下 |
| 字面量 | 字符串、整数 | `"hello"`, `42`, `3.14` |
| 二元运算 | `+`, `-`, `*`, `/` | `n * 2` |
| 函数调用 | 同一模块内的函数 | `double(5)` |
| 字段访问 | 结构体字段 | `req.Text` |

### 真实示例（来自 demoapp）

```spore
import DemoEchoReq from "app"
export fun ping(): string = "pong"
export fun twice(n: int): int = n * 2
export fun echo(req: DemoEchoReq): DemoEchoReq = req
```

### 支持的类型

| 类型 | 说明 |
|------|------|
| `int` | 整数 |
| `string` | 字符串 |
| `long` | 64 位整数 |
| `bool` | 布尔值 |
| `struct-type` | 从 schema 导入的结构体类型（如 `DemoEchoReq`） |

### validateExports 规则

一个已注册的 app 必须在入口模块中 `export` 所有 Manifest.Callables 中声明的 callable。如果某个 callable 在 manifest 中声明但脚本中没有对应 `export fun`,则 validateExports 失败,app 加载被拒绝：

```
error: manifest callable "echo" not exported by package
```

此规则在每次 `loadRuntime` 和 `buildReloadCandidate`（热重载）时执行,确保 manifest 和实现始终一致。

---

## 3. AppManifest 完整参考

`AppManifest` 是 Spore App 的声明式定义,包含元数据、接口、安全策略和 agent 绑定。

### AppManifest 字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 全局唯一标识符,如 `"builtin.demo"` |
| `Name` | `string` | 是 | 可读名称,如 `"Demo"` |
| `Version` | `string` | 是 | 语义版本号,如 `"1.0.0"` |
| `Runtime` | `string` | 是 | 运行时类型。Spore App 值为 `"spore"` |
| `ProtocolVersion` | `int` | 是 | 协议版本号,目前固定为 `1` |
| `Namespace` | `string` | 是 | 唯一的命名空间,如 `"sporeapp.builtin.demo"` |
| `Permissions` | `array<string>` | 是 | 请求的能力列表,如 `["state"]` |
| `Schemas` | `array<AppSchemaRef>` | 是 | 引用的 schema 列表 |
| `Callables` | `array<AppCallableDescriptor>` | 是 | 声明的 callable 列表 |
| `Events` | `array<AppEventDescriptor>` | 是 | 声明的事件列表 |
| `Projections` | `array<AppProjectionDescriptor>` | 是 | 声明的投影列表 |
| `Entrypoints` | `array<AppEntrypoint>` | 是 | 声明的入口点（前端视图/面板） |
| `Dependencies` | `array<AppDependency>` | 是 | 依赖的其他 app/plugin |
| `AgentBinding` | `optional AppAgentBinding` | 否 | agent 绑定配置 |
| `Security` | `optional AppSecurityPolicy` | 否 | 安全策略（默认限制） |

### AppCallableDescriptor

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 唯一 callable 标识符,如 `"ping"`、`"twice"` |
| `RequestSchema` | `string` | 是 | 请求 payload 的 schema 名称,如 `"DemoEchoReq"`。无参 callable 可留空 |
| `ResponseSchema` | `string` | 是 | 响应 payload 的 schema 名称。无响应可留空 |
| `Permission` | `optional string` | 否 | 权限要求 |
| `Scope` | `optional string` | 否 | 作用域 |
| `TimeoutMs` | `optional long` | 否 | 超时毫秒数 |
| `Streaming` | `optional bool` | 否 | 是否流式响应 |

### AppEventDescriptor

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 事件标识符 |
| `PayloadSchema` | `string` | 是 | 事件 payload 的 schema 名称 |
| `Permission` | `optional string` | 否 | 权限要求 |

### AppProjectionDescriptor

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 投影标识符 |
| `PayloadSchema` | `string` | 是 | 投影 payload 的 schema 名称 |
| `Permission` | `optional string` | 否 | 权限要求 |

### AppEntrypoint

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Kind` | `string` | 是 | 入口点类型,如 `"view"`、`"panel"` |
| `Id` | `string` | 是 | 标识符,如 `"home"`、`"status"` |
| `Title` | `string` | 是 | 前端显示标题 |
| `Route` | `optional string` | 否 | 前端路由路径 |
| `Zone` | `optional string` | 否 | 前端布局区域 |

### AppSchemaRef

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Name` | `string` | 是 | schema 名称,如 `"DemoEchoReq"` |
| `Hash` | `string` | 是 | schema 内容的 SHA256 哈希 |
| `SchemaId` | `optional long` | 否 | schema ID,用于 BinaryCodec 编解码 |

### AppDependency

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 依赖的 app/plugin ID |
| `Version` | `string` | 是 | 依赖的版本约束 |
| `Hash` | `optional string` | 否 | 依赖的 hash 校验 |

### AppAgentBinding

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Capability` | `optional AgentCapabilityBinding` | 否 | agent 能力绑定 |
| `Surface` | `optional AgentSurfaceBinding` | 否 | agent 界面绑定 |
| `FreeAgent` | `optional FreeAgentBinding` | 否 | 自由 agent 策略 |

### AgentCapabilityBinding

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Callables` | `array<string>` | 是 | 授权给 agent 的 callable ID 列表 |
| `ProjectScope` | `optional string` | 否 | 项目作用域约束 |

### AgentSurfaceBinding

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Entrypoint` | `string` | 是 | 绑定的默认入口点 |
| `AgentId` | `optional string` | 否 | 绑定的 agent ID |
| `Projections` | `array<string>` | 是 | 授权给 agent 的投影列表 |
| `Events` | `array<string>` | 是 | 授权给 agent 的事件列表 |

### FreeAgentBinding

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `AllowCreate` | `bool` | 是 | 是否允许创建 agent |
| `AllowSwitch` | `bool` | 是 | 是否允许切换 agent |
| `AllowMessage` | `bool` | 是 | 是否允许发送消息 |
| `AgentKinds` | `optional array<string>` | 否 | 允许的 agent 种类 |

### AppSecurityPolicy

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `AllowNativeAccess` | `optional bool` | 否 | 是否允许原生访问（默认 false） |
| `MaxInstructions` | `optional long` | 否 | 最大指令数限制 |
| `MaxDurationMs` | `optional long` | 否 | 最大执行时间限制（毫秒） |
| `MaxHostCalls` | `optional long` | 否 | 最大宿主调用次数限制 |
| `MaxOutputBytes` | `optional long` | 否 | 最大输出字节数限制 |

### Runtime 值说明

| 值 | 说明 |
|------|------|
| `"spore"` | Spore App —— 用 Spore Script 编写的脚本化子应用 |
| `"native"` | Native Plugin —— 用 PluginAbi/FFI 加载的原生共享库 |

---

## 4. Host 端接口参考 —— AppManager（完整 callable 列表）

AppManager 是 Spore App/Native Plugin 的生命周期管理器。以下为全部 callable 及其权限。

### appmanager.session_create

**权限**: `AdminOnly`

创建前端 iframe session,返回 **签名 Token** + Origin/ExpiresAt/Nonce/Generation。Token 是后续 resolve/revoke 的唯一凭证,iframe 无法伪造绑定的 AppId/ViewId。

- 请求: `AppSessionCreateReq`
- 响应: `AppSessionCreateResp`

**AppSessionCreateReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `AppId` | `string` | 是 | app ID |
| `ViewId` | `string` | 是 | 视图 ID |
| `Origin` | `optional string` | 否 | 来源,取值 `"builtin"`、`"user"`、`"project"`（默认 `"user"`） |

**AppSessionCreateResp**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `SessionId` | `string` | 是 | 创建的 session ID（内部标识,非凭证） |
| `AppId` | `string` | 是 | app ID |
| `ViewId` | `string` | 是 | 视图 ID |
| `AgentId` | `optional string` | 否 | 绑定的 agent ID |
| `ProjectId` | `optional string` | 否 | 绑定的 project ID |
| `Origin` | `string` | 是 | 签发时捕获的来源 |
| `ExpiresAt` | `long` | 是 | Unix 秒;`0` 表示不过期 |
| `Nonce` | `string` | 是 | 服务端签发的随机 nonce |
| `Generation` | `long` | 是 | 签发时捕获的 app generation |
| `Token` | `string` | 是 | **签名 session 凭证**（`payload.signature`）,用于后续 resolve/revoke |

### appmanager.session_resolve

**权限**: `AdminOnly`

验证并解析 **签名 Token**,返回绑定的 AppId/ViewId 及 session 元数据。前端不应信任 iframe 传来的参数,应调用此 callable 验证。

- 请求: `AppSessionResolveReq`
- 响应: `AppSessionResolveResp`

**AppSessionResolveReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Token` | `string` | 是 | 签名 session 凭证 |

**AppSessionResolveResp**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `SessionId` | `string` | 是 | session ID |
| `AppId` | `string` | 是 | app ID |
| `ViewId` | `string` | 是 | 视图 ID |
| `AgentId` | `optional string` | 否 | 绑定的 agent ID |
| `ProjectId` | `optional string` | 否 | 绑定的 project ID |
| `Origin` | `string` | 是 | 来源 |
| `ExpiresAt` | `long` | 是 | Unix 秒;`0` 表示不过期 |
| `Nonce` | `string` | 是 | nonce |
| `Generation` | `long` | 是 | app generation |

### appmanager.session_revoke

**权限**: `AdminOnly`

吊销 session（以签名 Token 为凭证）。

- 请求: `AppSessionRevokeReq`
- 响应: `AppSessionRevokeResp`（空结构体）

**AppSessionRevokeReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Token` | `string` | 是 | 要吊销的签名 session 凭证 |

### appmanager.register

**权限**: `AdminOnly`

注册一个新的 Spore App。

- 请求: `AppManagerRegisterReq`
- 响应: 无（load 失败返回 error）

**AppManagerRegisterReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Manifest` | `AppManifest` | 是 | app 的完整 manifest |
| `EntryModule` | `string` | 是 | 入口模块名,如 `"main"` |
| `Modules` | `map<string, string>` | 是 | 模块名 → 源代码的映射 |
| `Assets` | `optional map<string, bytes>` | 否 | 静态资源 |
| `SchemaDescriptors` | `optional map<string, AppObjectDescriptor>` | 否 | schema 的类型描述 |
| `PackageHash` | `optional string` | 否 | 包内容的哈希 |
| `ArtifactPath` | `optional string` | 否 | 制品路径（Native Plugin 用） |
| `ArtifactHash` | `optional string` | 否 | 制品哈希 |
| `Abi` | `optional PluginAbi` | 否 | 插件 ABI（Native Plugin 用） |
| `Origin` | `optional string` | 否 | 来源,取值 `"builtin"`、`"user"`、`"project"`（默认 `"user"`） |

### appmanager.unregister

**权限**: `AdminOnly`

注销并销毁 app。

- 请求: `AppManagerUnregisterReq`
- 响应: 无

**AppManagerUnregisterReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 要注销的 app ID |

### appmanager.list

**权限**: `Public`

列出所有已注册的 app。

- 请求: `AppManagerListReq`（空）
- 响应: `AppManagerListResp`

**AppManagerListResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Items` | `array<AppStatus>` | 所有已注册 app 的状态列表 |

### appmanager.get

**权限**: `Public`

获取指定 app 的详细状态。

- 请求: `AppManagerGetReq`
- 响应: `AppManagerGetResp`

**AppManagerGetReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |

**AppManagerGetResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Status` | `AppStatus` | app 的当前状态 |

### AppStatus

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Runtime` | `string` | 是 | 运行时类型 |
| `State` | `string` | 是 | 状态值（如 `"running"`、`"error"`） |
| `Version` | `optional string` | 否 | app 版本 |
| `Namespace` | `optional string` | 否 | 命名空间 |
| `PackageHash` | `optional string` | 否 | 包哈希 |
| `ArtifactHash` | `optional string` | 否 | 制品哈希 |
| `Error` | `optional string` | 否 | 错误信息 |
| `SchemaDescriptors` | `optional map<string, AppObjectDescriptor>` | 否 | schema 描述 |
| `Entrypoints` | `array<AppEntrypoint>` | 是 | 入口点列表 |

### appmanager.invoke

**权限**: `Public`

调用 app 的某个 callable。

- 请求: `AppManagerInvokeReq`
- 响应: `AppManagerInvokeResp`

**AppManagerInvokeReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Callable` | `string` | 是 | 要调用的 callable 名称 |
| `Payload` | `bytes` | 是 | BinaryCodec 编码的 payload |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `Role` | `optional string` | 否 | 角色上下文 |
| `ProjectId` | `optional string` | 否 | 项目上下文 |
| `RequestId` | `optional string` | 否 | 请求追踪 ID |
| `ExpectedPackageHash` | `optional string` | 否 | 期望的包哈希（一致性校验） |
| `RouteDepth` | `optional int` | 否 | 路由深度 |
| `RouteToken` | `optional string` | 否 | 路由令牌 |

**AppManagerInvokeResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Payload` | `bytes` | BinaryCodec 编码的响应 payload |

### appmanager.audit

**权限**: `AdminOnly`

查询 app 的审计记录。

- 请求: `AppManagerAuditReq`
- 响应: `AppManagerAuditResp`

**AppManagerAuditReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `AppId` | `optional string` | 否 | 按 app ID 过滤 |
| `Limit` | `optional int` | 否 | 返回记录数上限 |

**AppManagerAuditResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Records` | `array<AppAuditRecord>` | 审计记录列表 |

### AppAuditRecord

| 字段 | 类型 | 说明 |
|------|------|------|
| `Time` | `string` | 时间戳 |
| `RequestId` | `string` | 请求 ID |
| `AppId` | `string` | app ID |
| `Runtime` | `string` | 运行时类型 |
| `AgentId` | `string` | agent ID |
| `Role` | `string` | 角色 |
| `ProjectId` | `string` | 项目 ID |
| `Callable` | `string` | 调用的 callable |
| `Allowed` | `bool` | 是否被允许 |
| `Reason` | `string` | 原因/说明 |

### appmanager.cast

**权限**: `Public`

向 app 发送事件（event）。

- 请求: `AppManagerCastReq`
- 响应: `AppManagerCastResp`

**AppManagerCastReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Event` | `string` | 是 | 事件名称 |
| `Payload` | `bytes` | 是 | BinaryCodec 编码的 payload |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `Role` | `optional string` | 否 | 角色上下文 |
| `ProjectId` | `optional string` | 否 | 项目上下文 |

**AppManagerCastResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Delivered` | `int` | 投递数量 |

### appmanager.emit

**权限**: `Public`

触发 app 声明的事件,广播给所有监听者。

- 请求: `AppManagerEmitReq`
- 响应: `AppManagerEmitResp`

**AppManagerEmitReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Event` | `string` | 是 | 事件名称 |
| `Payload` | `bytes` | 是 | BinaryCodec 编码的 payload |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `Role` | `optional string` | 否 | 角色上下文 |
| `ProjectId` | `optional string` | 否 | 项目上下文 |

**AppManagerEmitResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Accepted` | `bool` | 是否被接受 |

### AppEventMessage（事件消息结构）

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | 事件消息 ID |
| `Event` | `string` | 是 | 事件类型 |
| `Payload` | `bytes` | 是 | payload |
| `Sender` | `optional string` | 否 | 发送者 |

### appmanager.agent_action

**权限**: `Public`

代理 agent action（create/switch/message）,受 FreeAgentBinding 策略控制。

- 请求: `AppManagerAgentActionReq`
- 响应: `AppManagerAgentActionResp`

**AppManagerAgentActionReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Action` | `string` | 是 | 动作类型：`"create"`、`"switch"`、`"message"` |
| `Kind` | `optional string` | 否 | agent 种类（create 时使用,如 `"coder"`） |
| `TargetAgentId` | `optional string` | 否 | 目标 agent（switch/message 时使用） |
| `Payload` | `optional map<string, any>` | 否 | 动作 payload |
| `AgentId` | `optional string` | 否 | 绑定的 agent 身份 |
| `Role` | `optional string` | 否 | 角色 |
| `ProjectId` | `optional string` | 否 | 项目 ID |
| `RequestId` | `optional string` | 否 | 请求追踪 ID |

**AppManagerAgentActionResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Accepted` | `bool` | 是否被接受 |

### appmanager.reload

**权限**: `AdminOnly`

热重载 app 的包。支持乐观锁（ExpectedStateVersion）和状态迁移（MigratedState）。失败时保留旧版本。

- 请求: `AppManagerReloadReq`
- 响应: `AppManagerReloadResp`

**AppManagerReloadReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `EntryModule` | `string` | 是 | 新入口模块名 |
| `Modules` | `map<string, string>` | 是 | 新模块映射 |
| `Assets` | `optional map<string, bytes>` | 否 | 新静态资源 |
| `SchemaDescriptors` | `optional map<string, AppObjectDescriptor>` | 否 | 新 schema 描述 |
| `PackageHash` | `optional string` | 否 | 新包哈希 |
| `ExpectedStateVersion` | `optional long` | 否 | 乐观锁:期望的当前 state version |
| `MigratedState` | `optional map<string, any>` | 否 | 迁移后的状态 |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `RequestId` | `optional string` | 否 | 请求追踪 ID |
| `CandidateManifest` | `optional AppManifest` | 否 | 候选新 manifest |
| `CandidateAbi` | `optional PluginAbi` | 否 | 候选 ABI |
| `CandidateArtifactPath` | `optional string` | 否 | 候选制品路径 |
| `CandidateArtifactHash` | `optional string` | 否 | 候选制品哈希 |

**AppManagerReloadResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Status` | `AppStatus` | 重载后的状态 |
| `StateVersion` | `long` | 当前 state version |

### appmanager.project_package

**权限**: `AdminOnly`

从项目 actor 加载包信息（Manifest、Modules、SchemaDescriptors、PackageHash）。

- 请求: `AppManagerProjectPackageReq`
- 响应: `AppManagerProjectPackageResp`

**AppManagerProjectPackageReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `ProjectId` | `string` | 是 | 项目 ID |
| `AppId` | `optional string` | 否 | app ID（可选,用于过滤） |
| `EntryModule` | `optional string` | 否 | 入口模块名 |

**AppManagerProjectPackageResp**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Manifest` | `AppManifest` | 是 | 从项目加载的 manifest |
| `EntryModule` | `string` | 是 | 入口模块名 |
| `Modules` | `map<string, string>` | 是 | 模块源代码 |
| `Assets` | `optional map<string, bytes>` | 否 | 静态资源 |
| `SchemaDescriptors` | `optional map<string, AppObjectDescriptor>` | 否 | schema 描述 |
| `PackageHash` | `string` | 是 | 包哈希 |

### appmanager.register_project

**权限**: `AdminOnly`

从项目源码一步完成加载+注册（project_package + register 组合）。

- 请求: `AppManagerRegisterProjectReq`
- 响应: `AppManagerRegisterProjectResp`

**AppManagerRegisterProjectReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `ProjectId` | `string` | 是 | 项目 ID |
| `AppId` | `optional string` | 否 | app ID（可选,用于过滤） |
| `EntryModule` | `optional string` | 否 | 入口模块名 |

**AppManagerRegisterProjectResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Status` | `AppStatus` | 注册后的状态 |

### appmanager.reload_project

**权限**: `AdminOnly`

从项目源码一步完成重新加载（project_package + reload 组合）。

- 请求: `AppManagerReloadProjectReq`
- 响应: `AppManagerReloadProjectResp`

**AppManagerReloadProjectReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `ProjectId` | `string` | 是 | 项目 ID |
| `AppId` | `string` | 是 | app ID |
| `EntryModule` | `optional string` | 否 | 入口模块名 |
| `ExpectedStateVersion` | `optional long` | 否 | 乐观锁 |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `RequestId` | `optional string` | 否 | 请求追踪 ID |

**AppManagerReloadProjectResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Status` | `AppStatus` | 重载后的状态 |
| `StateVersion` | `long` | state version |

---

## 5. Host 端接口参考 —— SporeApp runtime callables

SporeApp actor 的 callable,实际是 AppManager 在内部调用的。外部用户通过 `appmanager.invoke` 间接调用。

### sporeapp.invoke

**权限**: `Public`

调用 app 脚本中的 export fun。

- 请求: `SporeAppInvokeReq`
- 响应: `SporeAppInvokeResp`

**SporeAppInvokeReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Callable` | `string` | 是 | 要调用的 callable 名称 |
| `Payload` | `bytes` | 是 | BinaryCodec 编码的 payload |
| `AgentId` | `optional string` | 否 | agent 上下文 |
| `Role` | `optional string` | 否 | 角色上下文 |
| `ProjectId` | `optional string` | 否 | 项目上下文 |
| `RequestId` | `optional string` | 否 | 请求追踪 ID |
| `RouteDepth` | `optional int` | 否 | 路由深度 |
| `RouteToken` | `optional string` | 否 | 路由令牌 |

**SporeAppInvokeResp**:

| 字段 | 类型 | 说明 |
|------|------|------|
| `Payload` | `bytes` | BinaryCodec 编码的响应 payload |

### sporeapp.state

**权限**: `Public`

读取 app 的完整状态。

- 请求: `SporeAppStateReq`
- 响应: `map<string, string>`（返回完整状态 kv 映射）

**SporeAppStateReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |

### sporeapp.state_set

**权限**: `Public`

设置 app 的状态。

- 请求: `SporeAppStateSetReq`
- 响应: 无

**SporeAppStateSetReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `State` | `map<string, string>` | 是 | 要设置的状态键值对 |

### sporeapp.reload

**权限**: `AdminOnly`

热重载 app 的包和状态。

- 请求: `SporeAppReloadReq`
- 响应: `SporeAppReloadResp`

**SporeAppReloadReq**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `EntryModule` | `string` | 是 | 新入口模块名 |
| `Modules` | `map<string, string>` | 是 | 新模块映射 |
| `Assets` | `optional map<string, bytes>` | 否 | 新静态资源 |
| `SchemaDescriptors` | `optional map<string, AppObjectDescriptor>` | 否 | 新 schema 描述 |
| `PackageHash` | `optional string` | 否 | 新包哈希 |
| `ExpectedStateVersion` | `optional long` | 否 | 乐观锁 |
| `MigratedState` | `optional map<string, any>` | 否 | 状态迁移 |

**SporeAppReloadResp**:

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Id` | `string` | 是 | app ID |
| `Version` | `string` | 是 | app 版本 |
| `StateVersion` | `optional long` | 否 | 当前 state version |

### Host 能力绑定

当 manifest 的 `Permissions` 中包含 `"state"` 时,脚本运行时可以调用以下两个 host 函数：

| Capability | Namespace | 函数名 | 签名 | 说明 |
|------------|-----------|--------|------|------|
| `"state"` | `app` | `stateGet` | `() -> map<string, any>` | 获取完整状态 |
| `"state"` | `app` | `stateSet` | `(key: string, value: any) -> void` | 设置单个状态键值 |

脚本内调用方式：

```spore
// 需要在 manifest.Permissions 中声明 "state"
let data = app.stateGet()
app.stateSet("count", 5)
```

每次 `stateSet` 调用会自增 `StateVersion` 并持久化。

**重要规则**：如果 manifest 声明了一个 capability 但没有对应的 host binding 注册（capabilityHostBindings 中不存在）,则 `loadRuntime` 失败,报错：

```
sporeapp: declared capability "xxx" has no host binding
```

---

## 6. Host 端接口参考 —— Session（前端 iframe）

Session 用于前端 iframe 与应用后端的安全绑定。

### 工作流程

```
前端 iframe 加载
  → 调用 appmanager.session_create(AppId, ViewId)
  → 获得 SessionId
  → iframe 将 SessionId 传给后端
  → 后端调用 appmanager.session_resolve(SessionId) 验证
  → 获得真实的 AppId、ViewId、AgentId、ProjectId
```

### appmanager.session_create

**权限**: `AdminOnly`

创建一个 session,返回 `SessionId`。

- 请求: `AppSessionCreateReq`
  - `AppId: string` — app ID
  - `ViewId: string` — 视图 ID
- 响应: `AppSessionCreateResp`
  - `SessionId: string` — 创建的 session 标识符
  - `AppId: string`
  - `ViewId: string`
  - `AgentId: optional string`
  - `ProjectId: optional string`

### appmanager.session_resolve

**权限**: `AdminOnly`

验证 session ID 并获取绑定的上下文。

- 请求: `AppSessionResolveReq`
  - `SessionId: string`
- 响应: `AppSessionResolveResp`
  - `AppId: string`
  - `ViewId: string`
  - `AgentId: optional string`
  - `ProjectId: optional string`

### appmanager.session_revoke

**权限**: `AdminOnly`

吊销 session。

- 请求: `AppSessionRevokeReq`
  - `SessionId: string`
- 响应: `AppSessionRevokeResp`（空）

### 安全说明

**前端不应信任 iframe 传来的 pluginID/agentID/projectID**。这些值可能被伪造。后端收到请求后,应：

1. 从请求中提取 `SessionId`
2. 调用 `appmanager.session_resolve(SessionId)`
3. 使用 resolve 返回的 `AppId` / `AgentId` / `ProjectId` 作为真实身份

---

## 7. 教程：从零构建一个 Spore App

本教程基于 demoapp 模式,逐步构建一个完整的 Spore App。

### Step 1: 设计 AppManifest

首先定义 app 的元数据和接口声明。

```go
var Manifest = gen.AppManifest{
    ID:              "myapp.counter",
    Name:            "Counter",
    Namespace:       "sporeapp.myapp.counter",
    Version:         "1.0.0",
    Runtime:         "spore",
    ProtocolVersion: 1,
    Permissions:     []string{"state"},
    Schemas: []gen.AppSchemaRef{},
    Callables: []gen.AppCallableDescriptor{
        {ID: "ping"},
        {ID: "getCount"},
        {ID: "increment", RequestSchema: "IncrementReq", ResponseSchema: "IncrementResp"},
    },
    Events: []gen.AppEventDescriptor{},
    Entrypoints: []gen.AppEntrypoint{
        {ID: "counter", Kind: "view", Title: "Counter"},
    },
    Security: &gen.AppSecurityPolicy{
        MaxInstructions: 100000,
        MaxDurationMs:   5000,
        MaxHostCalls:    100,
        MaxOutputBytes:  4096,
    },
}
```

关键注意：
- `Runtime` 必须为 `"spore"`
- `Permissions` 声明需要的能力（如 `"state"`）
- 每个 `Callables` 条目最终必须在 Spore Script 中有对应的 `export fun`

### Step 2: 编写 Spore 模块

入口模块（`modules/main.spore`）：

```spore
export fun ping(): string = "pong"
export fun getCount(): int = 0
export fun increment(n: int): int = n + 1
```

### Step 3: 定义 Schema（可选）

如果 callable 有 typed payload,在 `schemas/` 目录下定义 struct 类型。

```spore
// schemas/counter._9999.spore
struct IncrementReq {
    Delta: int
}

struct IncrementResp {
    NewCount: int
}
```

### Step 4: 构造 SchemaDescriptors

对于有 typed payload 的 callable,需要构造 `AppObjectDescriptor` 用于 BinaryCodec 编解码。

```go
schemaDescriptors := map[string]gen.AppObjectDescriptor{
    "IncrementReq": {
        Kind: string(sporesch.TypeKindStruct),
        Name: "IncrementReq",
        Fields: []gen.AppFieldDescriptor{
            {Name: "Delta", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "int"}},
        },
    },
    "IncrementResp": {
        Kind: string(sporesch.TypeKindStruct),
        Name: "IncrementResp",
        Fields: []gen.AppFieldDescriptor{
            {Name: "NewCount", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "int"}},
        },
    },
}
```

### Step 5: 通过 appmanager.register 注册

```go
req := gen.AppManagerRegisterReq{
    Manifest:     manifest,
    EntryModule:  "main",
    Modules:      map[string]string{"main": mainSource},
    SchemaDescriptors: schemaDescriptors,
    Origin:       "user",
}

// 通过 actor context 调用
resp, err := ctx.Call("appmanager.register", req)
```

`AppManagerRegisterReq` 完整字段：

| 字段 | 说明 |
|------|------|
| `Manifest` | app 的完整 manifest |
| `EntryModule` | 入口模块名,如 `"main"` |
| `Modules` | 模块名 → 源代码映射 |
| `SchemaDescriptors` | schema 类型描述（可选） |
| `PackageHash` | 包内容哈希（可选） |
| `Origin` | `"builtin"` / `"user"` / `"project"`（默认 `"user"`） |

注册时 SporeApp 内部会依次执行：
1. 加载入口模块和依赖模块
2. 绑定 capability host 函数
3. 调用 validateSchemaRefs 验证 schema 引用
4. 调用 validateExports 确保所有 callable 有 export fun
5. 调用 validateCapabilityConsistency 确认 host 绑定一致性

### Step 6: 通过 appmanager.invoke 调用 callable

```go
payload, _ := codec.Marshal(someStruct)
req := gen.AppManagerInvokeReq{
    Id:       "myapp.counter",
    Callable: "increment",
    Payload:  payload,
}
resp, err := ctx.Call("appmanager.invoke", req)
```

### Step 7: 状态管理

声明 `"state"` capability 后,在 Spore Script 内使用：

```spore
// manifest.Permissions 需包含 "state"
export fun getCount(): int = app.stateGet()["count"]
export fun increment(delta: int): int {
    let current = app.stateGet()["count"]
    let newVal = current + delta
    app.stateSet("count", newVal)
    return newVal
}
```

外部通过 `sporeapp.state` 和 `sporeapp.state_set` 读取/设置状态：

```go
state, _ := ctx.Call("sporeapp.state", gen.SporeAppStateReq{Id: "myapp.counter"})
ctx.Call("sporeapp.state_set", gen.SporeAppStateSetReq{Id: "myapp.counter", State: map[string]string{"count": "42"}})
```

状态变更会自增 `StateVersion` 并持久化。

### Step 8: 热重载（reload）

使用 `appmanager.reload` 可以热更新 app 的代码和状态,无需注销。

```go
newModules := map[string]string{"main": newSource}
req := gen.AppManagerReloadReq{
    Id:                   "myapp.counter",
    EntryModule:          "main",
    Modules:              newModules,
    ExpectedStateVersion: 3,  // 乐观锁：期望当前 state version 为 3
    MigratedState:        map[string]any{"count": 100},  // 状态迁移
}
resp, err := ctx.Call("appmanager.reload", req)
```

**安全机制**：
- reload 先构造候选 runtime（buildReloadCandidate）,通过 validateExports 和 validateCapabilityConsistency 验证后才原子替换
- 失败时保留旧的 runtime,不会变成不可用状态
- `ExpectedStateVersion` 提供乐观并发控制

### Step 9: 从项目源码加载

`appmanager.project_package` 可以从项目 actor 加载包信息：

```go
pkgResp, _ := ctx.Call("appmanager.project_package", gen.AppManagerProjectPackageReq{
    ProjectId: "proj-123",
    AppId:     "myapp.counter",
})
// pkgResp 包含 Manifest, EntryModule, Modules, SchemaDescriptors, PackageHash

// 用返回的信息注册
registerReq := gen.AppManagerRegisterReq{
    Manifest:          pkgResp.Manifest,
    EntryModule:       pkgResp.EntryModule,
    Modules:           pkgResp.Modules,
    SchemaDescriptors: pkgResp.SchemaDescriptors,
    PackageHash:       pkgResp.PackageHash,
}
```

或者一步到位使用 `appmanager.register_project`：

```go
resp, _ := ctx.Call("appmanager.register_project", gen.AppManagerRegisterProjectReq{
    ProjectId: "proj-123",
    AppId:     "myapp.counter",
})
```

### Step 10: 注销

```go
ctx.Call("appmanager.unregister", gen.AppManagerUnregisterReq{
    Id: "myapp.counter",
})
```

注销后,app 的状态和管理记录被清理。如果注销失败（如暂时不可用）,应保留记录并重试。

---

## 8. 完整示例

以下是一个完整的 Counter App,参考 demoapp 风格,包含 Go 构造代码和 Spore Script。

### 完整 Go 代码

```go
package counterapp

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"

    sporesch "github.com/qomos-w/spore/schema"
    gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

var Manifest = gen.AppManifest{
    ID:              "myapp.counter",
    Name:            "Counter",
    Namespace:       "sporeapp.myapp.counter",
    Version:         "1.0.0",
    Runtime:         "spore",
    ProtocolVersion: 1,
    Permissions:     []string{"state"},
    Schemas:         []gen.AppSchemaRef{},
    Callables: []gen.AppCallableDescriptor{
        {ID: "ping"},
        {ID: "getCount"},
        {ID: "increment", RequestSchema: "IncrementReq", ResponseSchema: "IncrementResp"},
    },
    Events: []gen.AppEventDescriptor{
        {ID: "count_changed"},
    },
    Entrypoints: []gen.AppEntrypoint{
        {ID: "counter", Kind: "view", Title: "Counter View"},
        {ID: "stats", Kind: "panel", Title: "Counter Stats"},
    },
    Security: &gen.AppSecurityPolicy{
        MaxInstructions: 100000,
        MaxDurationMs:   5000,
        MaxHostCalls:    100,
        MaxOutputBytes:  4096,
    },
}

const EntryModule = "main"

const MainModule = `import IncrementReq from "app"
import IncrementResp from "app"

export fun ping(): string = "pong"
export fun getCount(): int = app.stateGet()["count"]
export fun increment(req: IncrementReq): IncrementResp {
    let current = app.stateGet()["count"]
    let newVal = current + req.Delta
    app.stateSet("count", newVal)
    return IncrementResp{NewCount: newVal}
}`

func RegisterReq() gen.AppManagerRegisterReq {
    descIncReq := gen.AppObjectDescriptor{
        Kind: string(sporesch.TypeKindStruct), Name: "IncrementReq",
        Fields: []gen.AppFieldDescriptor{
            {Name: "Delta", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "int"}},
        },
    }
    descIncResp := gen.AppObjectDescriptor{
        Kind: string(sporesch.TypeKindStruct), Name: "IncrementResp",
        Fields: []gen.AppFieldDescriptor{
            {Name: "NewCount", Type: gen.AppTypeDescriptor{Kind: string(sporesch.TypeKindScalar), Name: "int"}},
        },
    }
    descriptors := map[string]gen.AppObjectDescriptor{
        "IncrementReq":  descIncReq,
        "IncrementResp": descIncResp,
    }

    // Compute schema hash (same pattern as demoapp)
    obj := sporesch.ObjectDesc{
        Kind: sporesch.TypeKindStruct, Name: "IncrementReq",
        Fields: []sporesch.FieldDesc{
            {Name: "Delta", Type: sporesch.TypeDesc{Kind: sporesch.TypeKindScalar, Name: "int"}},
        },
    }
    encoded, _ := json.Marshal(obj)
    hash := sha256.Sum256(encoded)

    manifest := Manifest
    return gen.AppManagerRegisterReq{
        Manifest:          manifest,
        EntryModule:       EntryModule,
        Modules:           map[string]string{"main": MainModule},
        SchemaDescriptors: descriptors,
        PackageHash:       hex.EncodeToString(hash[:]),
        Origin:            "user",
    }
}
```

### 对应 Spore Schema

```spore
// schemas/counter._9999.spore
struct IncrementReq {
    Delta: int
}
struct IncrementResp {
    NewCount: int
}
```

### 注册流程

```go
// 在测试或初始化代码中
req := RegisterReq()
err := ctx.Call("appmanager.register", req)
if err != nil {
    // 注册失败处理
}

// 调用 callable
payload, _ := codec.Marshal(IncrementReq{Delta: 5})
invokeReq := gen.AppManagerInvokeReq{
    Id:       "myapp.counter",
    Callable: "increment",
    Payload:  payload,
}
resp, err := ctx.Call("appmanager.invoke", invokeReq)
```

---

## 9. 安全与规则

### validateExports

每个 manifest 中声明的 callable 必须在入口模块中有对应的 `export fun`,否则 `loadRuntime` 或 `buildReloadCandidate` 失败：

```
sporeapp: export validation failed: manifest callable "echo" not exported by package
```

### validateCapabilityConsistency

所有已绑定的 host 函数必须在 `allowedCapabilities` 中有对应声明,反之亦然。任何漂移都会导致 load 失败：

```
sporeapp: host function app.stateGet bound without granted capability
sporeapp: declared capability "state" has no host binding
```

### SecurityPolicy 限流

`AppSecurityPolicy` 提供多层安全限流：

| 策略 | 说明 | 零值含义 |
|------|------|----------|
| `AllowNativeAccess` | 是否允许原生访问 | `false`（拒绝） |
| `MaxInstructions` | 脚本执行最大指令数 | `0` = 不限制 |
| `MaxDurationMs` | 最大执行时间 | `0` = 不限制 |
| `MaxHostCalls` | 最大宿主调用次数 | `0` = 不限制 |
| `MaxOutputBytes` | 最大输出字节数 | `0` = 不限制 |

### Session 安全

- 前端不信任 iframe 来源：前端 iframe 传入的 pluginID/agentID/projectID 都不可信
- 后端必须调用 `appmanager.session_resolve(SessionId)` 验证

### Secret 管理

- secret 不进 manifest、schema、event、log、response
- 不使用 `localStorage` / `sessionStorage`

### 错误处理

- **reload 失败**：保留旧版本,不丢失可用性。候选 runtime 构建失败时回滚
- **unregister 失败**：保留记录,重试注销
- **invoke 失败**：返回 error,不影响 app 状态

---

## 10. 测试要求

| 测试场景 | 说明 | 验证点 |
|----------|------|--------|
| Callable happy path | 调用已注册的 callable 并验证返回值 | payload 正确、无 error |
| Malformed payload 拒绝 | 用无效 payload 调用 typed callable | 返回编解码错误 |
| Unknown callable 拒绝 | 调用 manifest 中不存在的 callable | 返回 callable not found 错误 |
| Export 漂移 | 注册 manifest 有 callable 但脚本无 export fun | validateExports 失败,load 拒绝 |
| Capability 漂移 | 声明不存在的 capability | validateCapabilityConsistency 失败,load 拒绝 |
| State round-trip | 通过脚本 stateSet/stateGet 写入和读取状态 | 值一致、StateVersion 递增 |
| Reload 保留旧版 | 用损坏的 manifests 或 modules 调用 reload | 原 runtime 正常运行不变 |
| Unregister 清理 | 注销 app 后尝试调用 | 返回 app not found 错误 |
| Session mismatch 拒绝 | 用伪造的 sessionId 或已吊销的 session resolve | 返回 session 无效 |
| Package hash 一致性 | 设置 ExpectedPackageHash 与 load 时 hash 不同 | invoke 返回 hash mismatch 错误 |

---

## 11. 完成标准

Spore App 构建完成后,报告以下信息：

| 项目 | 说明 |
|------|------|
| App ID | manifest 中的 `Id` |
| Namespace | manifest 中的 `Namespace` |
| Version | manifest 中的 `Version` |
| Runtime | `"spore"` |
| Callables 数量及名称 | 列出每个 callable 的 ID、request/response schema |
| Schema 数量及名称 | 列出每个 schema ref 的 Name 和 Hash |
| Capabilities | Permissions 列表 |
| Entrypoints | 每个 entrypoint 的 Kind、ID、Title |
| 注册结果 | register callable 成功/失败及错误信息 |
| 测试结果 | 通过/失败的测试列表及断言详情 |

示例报告摘要：

```
Spore App "myapp.counter" (v1.0.0)
  Runtime:     spore
  Namespace:   sporeapp.myapp.counter
  Callables:   ping, getCount, increment(IncrementReq→IncrementResp)
  Capabilities: state
  Entrypoints: view:counter, panel:stats
  Register:    OK
  Tests:       10/10 passed
```
