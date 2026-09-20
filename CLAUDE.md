# sporemind · Claude 项目指引

sporemind = gospore + spore 之上的 agent 协作运行时。任何代码或文档修改前必读本文件。项目在开发中,允许大规模重构，不要求向后兼容，但必须过五面约束。

## 五面约束（每次变更必过）

| 面 | 约束 |
|---|---|
| 注入面 | **最小**：actor 要实现的接口/字段尽可能少 |
| 操作面 | **紧凑**：暴露的 callable 少、语义高层 |
| 约束面 | **显式**：policy / role / secret 写在策略，不靠默认或注释 |
| 工具链面 | **自动**：schema → codegen → 测试 全自动，契约漂移 CI 抓 |
| 主体面 | **放大 + 反馈**：agent 默认开放、动作必须有结果回流 |

冲突仲裁：约束面 vs 主体面 → 主体面优先。

## 改代码

- **Actor 优先**：持久状态与业务逻辑必须归属具体 actor。禁止包级变量、全局单例、跨 actor 共享的 mutable service、utility 包里的业务逻辑函数。
- **actor 形状**：`OnStart` 只拿 gospore 默认 seams（`Plan / LookupService / Expose / Register`），仅嵌入 `actor.Host` + 必要 handler，不接受业务依赖容器
- **存档重建**：从持久化状态恢复 actor 时必须用 `Props.WithID(id)` 预分配原 actor ID；禁止 auto-generate 导致 ID 漂移后子树挂载失败
- **callable**：数量最小、语义高层；可变行为（路由 / 主循环 / 工具策略）走 sporescript，不在 Go 写 if-else 清单
- **secret**：API key / 凭证 不进 anonymous role 可达的 callable 或 component；policy / role / scope / secret 必须显式声明在策略或代码中，不靠默认值、注释或硬编码
- **前端持久化**：禁止使用 `localStorage` / `sessionStorage`；所有 durable UI / preference 状态必须走 actor-owned backend state
- **纯净**：理念变了代码彻底重写；不留向后兼容残留
- **完备**：修改链条完整（接口 → 实现 → 注册 → 测试），不提交半成品
- **前后端协议**：前后端之间的所有 callable / message / event 必须先在 spore 定义 schema，再由 codegen 生成两端代码；禁止手写协议类型；契约漂移由 CI/测试抓取。**分片生成文件写入规则**：向已拆分为 `*.gen.part1.go` / `*.gen.part2.go` 的 schema 生成产物写入新内容时，必须先选择最后一个 part，检查其区段 ID 占用；仅当该 part 区段快满时才向后追加新 part，禁止默认回填 part1
- **Plugin 数据面走宿主网关**：面板 iframe 加载网关路由 `/plugin/{id}/{route}?v={generation}`（reload generation 失效缓存，HTML `Cache-Control: no-store`），网关反向代理到插件进程自己的 HTTP listener 并附网关令牌头（`X-Gateway-Token`）——面板不知子进程 origin 与 session secret；bridge 仅留管理面（console 捕获 / DOM 快照 / openView / theme / locale 下发）；callable / event 线格式为 JSON——无 base64、无 binary codec；视频 / 音频 / 图片走 HTTP 原生 binary（raw POST body / `<video src>` / chunked + MSE）；事件通道 WS-first（`GET /events` 带 Upgrade 头升级 WebSocket，帧格式 `{"event":"<id>","data":<raw JSON>}`，服务端 25s ping；浏览器对同源 HTTP/1.1 限 6 连接/主机、面板 iframe 共享网关源，per-panel 永久 SSE 会饿死全部瞬态请求（浏览器同源并发连接数限制的固有约束），SSE 保留为 standalone/旧客户端回退），`sdk.EmitEvent` 双路投递（本地事件通道（WS-first）+ host `app.emit` 跨 app 可见）；网关注入单一来源 bootstrap 片段（`sdk.BridgeBootstrapSnippet`），resolve `window.__sporemindAppBaseReady` 供 client.gen.ts 的 async `appBase()` 拼 URL（消除首取 404 竞态）；listener 直连同源打开时用 `spore_session` cookie（per-instance HMAC secret，宿主 OnLoad config 下发，空 secret = dev mode 关闭鉴权）。插件开发走 appmanager 循环：appdef → dev_generate（产物含 `server.gen.go` 与 HTTP 版 `client.gen.ts`）→ 实现 handlers → register_project → open_view 验证
- **STT/TTS 独立**：语音识别（stt）与语音合成（tts）是两种独立服务，各自维护独立的账户列表与激活账户；同一份账户配置不得同时服务 recognize 与 synthesize
- **持久化**：actor 的持久化状态必须经 `persist.Persist` 接口（`pkg/persist`）读写；禁止 actor 绕过 persist 直接读写文件系统的配置/状态 JSON。语义合同（contract_test.go 固化，新后端必须过同一套测试）：
  - **name = 文档路径语义**：正斜杠相对键（如 `agent/abc-123`、`workspace/<actorID>/rmounts`），不是文件系统路径；实现必须拒绝空名、绝对路径、`..` 穿越。Save/Load/Delete 映射单文档 `<root>/<name>.json`，原子写（tmp→fsync→rename），decode 失败备份 `.corrupt-<ts>`，`Load` 以 `ErrNotExist` 区分"首次启动"与真实 I/O 错误
  - **`<actorID>/` 从属命名空间与 Delete 级联**：actor 的从属文档必须挂在其 `<actorID>/` 子命名空间下（`workspace/<actorID>/rmounts` 等），`Delete(actorID)` 级联回收 `<actorID>.json` + `<actorID>/` 整棵子树；禁止再发明扁平后缀名（`actorID.rmounts` 一类）平铺从属状态
  - **Appender 原子性边界**：append-only 数据（step jsonl、request ledger）走 `persist.Appender`；单次 Append 调用原子——与同路径 `WriteFileAtomic` 共享 per-path 锁、并发不交错——但仅到"单次调用"为止：调用方负责批内每条记录自界定（各自 `\n` 结尾），Append 不解析、不校验、不补换行；name 原样映射路径（无 `.json` 后缀，调用方控扩展名）
  - **可选能力优雅降级**：`Lister`（枚举）/`Appender`/`BasePather` 都是可选接口，调用方必须 type-assert 优雅降级，不得假设具体后端
  - **backend 不得静默回退**：`persist.New` 对未知/未实现 backend 返回错误，config 启动校验，typo 不可见是约束面违规
  - **绕过 persist 的判定标准（"非状态数据"豁免）**：音频样本等媒体落地、派生缓存（如 boot-theme 投影缓存，权威状态仍在 prefs 卡片）、插件 app 私有数据目录（`app.data` capability 授权的 `<appDir>/.sporecode/appdata`，插件自写文件/嵌入式数据库如 goleveldb，SDK 侧 `sdk.DataDir()` 读取）可绕过；agent session 的 turns/steps/snapshots/requests 目录树经 `BasePather` 派生、append 部分已收编 Appender。除豁免外，任何不得不直接写文件的存量必须经 `persist.WriteFileAtomic` + corrupt 备份模式（registry.json 即范例）
  - **后端可插拔（fs / goleveldb / redis / etcd / mongo / mysql / postgres / oss / webdav）**：后端经 `pkg/persist` registry 注册（单一注册点），`persist.New` 未命中即报错、config 启动校验同表；仅 JSON 文档（`Persist` 接口语义）可切换后端——MarkdownPersist 与 agent session 文件树是文件系统特有布局，**任何非 fs 后端下仍走 fs**（依赖 `BasePather` 的调用方必须 type-assert 优雅降级）。后端语义差异（Appender 实现、事务、原子性、跨进程并发安全、TLS 默认）见 wiki《persist 后端选型矩阵》
  - **隧道引用写法**：yaml `backend_tunnel: <sshHostID>` → `PersistConfig.TunnelRef` → 后端构造时 `ResolveTunnel` 调 sshmanager `tunnel_open` 拿 localAddr 替换拨号地址（SSH `-L` 语义，隧道生命周期归 sshmanager）；解析失败/无 resolver = 启动显式报错，**禁止静默回退直连**；解析只做一次，断连重连 = sshmanager 在原 localAddr 恢复监听 + 客户端自动重拨（测试：`TestTunnelDisconnectReconnect`）。TLS 默认：直连 on（loopback 豁免）、隧道 off（SSH 信道已加密），显式 scheme 覆盖；隧道内 HTTPS 的 SNI 校验目标保持真域名（见 wiki《persist TLS 语义与 SNI 校验规则》）。DB 凭据走 dbmanager profile + `ResolveCredential` dial 时解析，secret 清单见 wiki《persist secret 声明清单》
- **浮层必须走 useBrowserOverlay**：任何可能出现在内嵌原生浏览器窗口之上的 HTML 浮层（modal、dropdown、context menu、popover 等）必须在 open 时经 `useBrowserOverlay(open)`（`web/src/ui/ai/browserOverlay.ts`）注册，由 `BrowserOverlayManager` 的引用计数统一驱动原生窗口隐藏/恢复（0→1 隐、1→0 显）。业务代码不得自行调用 `HideAllRightBrowserWindows` / `ShowAllRightBrowserWindows`，也不得用 z-index 方案绕过原生窗口遮挡
- **测试边界**：turn/engine 的启动路径（如 `startTurnWithName`）涉及完整 turn engine + LLM client 装配，难以单元测试；其中纯逻辑片段（如思考强度三级优先级解析）应提取为无副作用的纯函数并单独覆盖单元测试，而非通过重型装配间接测试
- **Unit 为唯一选择单位**：模型选择（model selection）的唯一 primitive 是 unit（`ModelUnit` = `(model, provider)` 二元组）。任何让"选一个裸 model"成为操作单位的路径都是 bug：`ModelRef.Kind` 只允许 `unit` / `aggregator` / `auto`，禁止 model-kind；承载选择状态的字段必须单一且一致（不得同时存在互不同步的快照与可写副本，否则运行中切换 unit 会被旧快照静默覆盖）
- **数据层 UTC 存储**，展示层/输入层转本地时间
- **高频 callable 注册模式（stateless 优先）**：高频基础设施 callable 不得注册为默认 stateful（owner lane）。读/查询/状态获取 → handler 首参 `actor.PureContext`（纯快照读，跨 lane 共享状态用互斥 + 拷贝，禁止在 pure handler 写 actor 状态字段）；写状态但高频（自调度 tick、事件/诊断入站回调、图同步）→ `ctx.RegisterLoop` 专用 stateful lane + `actor.WithLoop` 路由（命名先例：`graph_sync`/`glass_poll`/`aiagg_poll`/`coord_event`/`diag_ops`）。真正低频 mutate（submit/cancel/create/delete/save）才留默认 owner lane。全量违规表与判定口径见 wiki [[callable-inventory-hot-stateful-table]]；panic 包装用 pure 变体（参照 computeruse `recoverPureHandler`）保持无状态签名。
- **Owner Lane 禁阻塞（分层执行）**：stateful handler（owner 或任何共享 lane）只做状态变更与消息发送，预算 µs~ms、上限秒级；超过秒级的长任务必须三选一：专用 `RegisterLoop` lane（如 `agent_exec`/`lifecycle`）、`PureContext` + 结果回流消息、fire-and-forget self-call（`agent_run` 模式）。**新代码禁止在 stateful handler 内同步 `planner.Call(...).Await()` 跨 actor/网络 IO**（存量 25+ 处不追溯，但不得新增；`inspect_actor` 链式 Await 为已知存量例外）。控制面 callable（submit/cancel/pause/status）不得与长执行共用 lane。配套要求：per-(lane, callID) 执行时长与 queue 等待时长必须可观测（cell.stats）；超预算走标记 + 降级（blocked 语义、nudge 抑制——宁可降级也不撕裂状态），**禁止运行时强杀 handler**（Go 无法安全抢占，中途杀 = 状态撕裂）。明确不做：全 handler 异步化迁移、静态证明非阻塞、lane 内优先级抢占（待真实同 lane 饥饿事故再议）
- **构建输出**：mobile APK 编译到 `./build/` 目录下，不混入 `mobile/android/` 源码树；运行 `make build-apk` 自动递增 patch 版本并输出 `./build/sporemind-v{X.Y.Z}.apk`
- **桌面构建**：**禁止** `go build ./cmd/sporemind-desktop`、`go build ./cmd/sporemind-desktop/...`、`go run ./cmd/sporemind-desktop`；Wails 桌面必须走 `make build-desktop`（普通构建，非 production）或 `make release-desktop`（发布，production）；`make dev-desktop`（开发热重载），否则前端不构建、Wails 绑定不生成、web 资产不嵌入

## Actor 通信模式

层级：`Invoke`（原语）→ `Plan`（可观测增强）→ `Call / Stream`（便捷包装）

### Invoke（原语）

`target.Invoke(ctx, callID, payload, headers...)` 返回 `*invoke.Call`，统一 3 态：

| 态 | callID 模式 | 消费方式 |
|---|---|---|
| Tell | 不等响应 | `call.Close()` 或忽略 |
| Unary | 等一个值 | `call.Value()` 或 `call.Final(ctx)` |
| Stream | 逐 chunk | `call.Next(ctx)` 循环至 `io.EOF`，再 `call.Final(ctx)` 取终值 |

`call.Cancel()` 随时取消；`<-call.Done()` 在终态时关闭。

### Plan（可观测增强）

在 Invoke 之上包装为 child actor（plan node），获得 Stop / Watch / State / Projection；状态：`Pending → Running → Completed | Failed | Cancelled`。

**不自动销毁**——node 终态后仍留在 actor tree，需显式 `ctx.Stop(node.Ref())` 清理。`Call / Stream` 是 `Plan(WithAutoStart())` + 自动清理的便捷方法，适合一次性调用。

### 选型

| 场景 | 选 | 理由 |
|---|---|---|
| fire-and-forget | Invoke (Tell) | 不需要响应，不产生 child actor |
| 一次性拿值 | `planner.Call()` | 自动生命周期，无残留 |
| 一次性流式消费 | `planner.Stream()` | 同上 |
| 需 Stop / Watch / 状态追踪 | `planner.Plan()` | 手动控制，node 可观测 |
| LLM 执行链 | FuncCaller (Invocation) | 保持因果链、backpressure、cancel；禁止 send |

### 禁止

- **send 用于 LLM 执行链**：破坏因果链、无 backpressure、无 cancel；监督 / CRUD performer 例外
- **Plan 用于高频瞬态调用**：每次 spawn 常驻 actor，膨胀 tree；简单场景用 Invoke 或 Call/Stream

## Agent 操作红线

- **禁止以 junction/symlink 复用 node_modules 做 clean-tree 验证**：`git worktree remove --force` 会递归穿透 junction 误删真实 node_modules 与 file: 依赖目标（曾致多目录依赖连锁丢失）。需要干净对照时复制目录或在新 worktree 内独立 `npm install`
- **禁止 agent 对主仓/linked worktree 执行 git stash**：禁 agent 对主仓及任何 linked worktree 执行 `git stash`（refs/stash 跨 worktree 共享，会撞 merge 的快照与未提交系统状态）。需干净 baseline 走 scratch worktree
- **禁止 agent 自行在主仓 checkout 切分支**：主仓工作树是用户领地，agent 自主的 `git checkout`（含 `git switch`）会原地改写用户 tracked 文件（用户显式要求除外）。worktree merge 机制内部（`mergeWorktreeIntoBase`）的受控 checkout + named-ref 快照恢复不受此限；agent 需要别的分支态走 scratch worktree

## 上下文

- `../gospore/` / `../spore/` — 上游运行时与脚本层