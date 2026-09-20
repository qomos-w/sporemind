# workbench 终审报告（三线交付独立核对）

- 卡片: `workbench-review`（父 `workbench-integration`）
- 被审范围: 工作流 `workbench-integration` 全部已合入提交（HEAD `c2b22ef` = merge workbench-card-base-implementation）
- 方法: 只看代码/生成物/测试本身（零信任 agent 陈述）。逐条比对 `schemas/` → codegen 产物 → 两端调用点，以及对 `web/src/ui/workbench/blackboard-reference.html` 定稿的常量/颜色逐行核对。
- **本环境没有 shell 工具，无法执行任何 `go test` / `vitest` / `tsc` / `make gen-*-check`**。下面每处"测试存在"只代表我读了测试源码，不代表我跑过。凡涉及"跑绿"的结论一律标 UNVERIFIED。

---

## 一、逐条结论（VERIFIED 已亲自读代码确认 / UNVERIFIED 未能确认）

### 线① 插件图标颜色链路端到端 + 名称哈希稳定

| 环节 | 结论 | 证据 |
|---|---|---|
| schema 声明 `optional Color` | VERIFIED | `schemas/app._2032.spore:182-185` |
| AST / parser | VERIFIED | `pkg/appdef/ast.go:118-120`、`pkg/appdef/parser.go:852-853` |
| codegen 写 manifest | VERIFIED | `pkg/codegen/generate.go:1280-1286` |
| Go 生成物 | VERIFIED | `pkg/domain/gen/app.gen.go:94` |
| wire 生成物（gmanifest / static fragment） | VERIFIED | `gen/gmanifest.json:9722-9730`（AppBundle 字段序 Title/Description/Icon/Color/GuideCardId/Tools，与 schema 一致）、`gen/static_schema_fragment.json` 含 `"Color"` |
| 前端 wire 类型 | VERIFIED | `web/src/gen-clients/system/types.ts:1271` |
| appdef 样例 | VERIFIED | `plugin-dev-example/app.appdef:152`（`color: "#9333ea"`） |
| bundle 卡去写死 | VERIFIED | `pkg/actor/appmanager/bundle_cards.go:99-105`（`Visual: bundleVisual(...)`），无 `Accent:"purple"` 残留 |
| 显式色覆盖 + 最近 RGB 推 accent | VERIFIED | `bundle_cards.go:158-173,196-229`；测试 `bundle_visual_test.go:12-55` |
| 名称哈希稳定（FNV-1a，同输入同输出） | VERIFIED（实现+golden 测试都在） | `bundle_cards.go:175-181`；`bundle_visual_test.go:75-111`（golden 表 + 100 次稳定 + 500 例覆盖全 8 档） |
| 调色板顺序/hex 与前端契约一致 | VERIFIED | Go `bundle_cards.go:138-150` 与 `web/src/ui/ai/components/cardVisual.ts:34,37-46`（`CARD_ACCENTS` 顺序与 light 边框 hex 逐项相同） |
| 前端磁贴着色 | VERIFIED | `web/src/application/app-registry.ts:81`（含 lifecycle 保留 `:133-135`）→ `web/src/ui/ai/components/SidebarLauncher.tsx:92` → `appIconResolver.tsx:40-66` |
| 前端标签页着色 | VERIFIED | `AIShellLayout.tsx:5173`；`PluginView.color` 定义 `plugin-registry.ts:8`；四个构造点 `AIShellLayout.tsx:339,3752,3797,4952` |
| omnibox 着色 | VERIFIED | `omniboxLabels.tsx:35-39`（`componentIcon` + `.app-icon-colored`）；`AppOmniboxOverlay.tsx:123` 渲染预构建 icon 节点 |
| 暗色可读性 | VERIFIED | `AIShellLayout.css:1066-1068`（只作用于带 marker class 的 svg）；契约测试 `appIconColorCss.test.ts:14` |
| bundle 卡（非 app 磁贴）走 `data.visual.color` | VERIFIED | `bundle_cards.go:163` 写 `Visual.Color` → `cardVisual.ts:86` → `componentIcon` |

### 线② 消息流 run command 视觉对齐定稿终端卡（亮暗双主题）

| 环节 | 结论 | 证据 |
|---|---|---|
| `.ai-console` 容器包住命令/输出/状态 | VERIFIED | `BashToolView.tsx:146-209`（命令块 155-168、stdout 173-177、stderr 179-189、exit 198-200、diff 208） |
| 输出区可滚动、`$` 提示符、12px 圆角、无外框 | VERIFIED | `parts.css:939-957`（`border:none;border-radius:12px`）、`:980-989`（`overflow-y:auto;max-height:260px`）、`:1002-1006`（`content:"$ "`） |
| 亮色变体 | VERIFIED | `parts.css:959-965`（`[data-theme="light"]`，米纸 `#f4f1e8` + 墨绿 `#2f4a38`）；`applyTheme` 确实写 `data-theme='light'`（`theme-apply.test.ts:18`） |
| 组件/DOM + 样式契约测试存在 | VERIFIED | `BashToolView.console.test.tsx:44-93` |
| 与定稿 `blackboard-reference.html` 终端卡逐色一致 | **不成立（见 D3）** | 参考 `.termbody` = `#151a17/#c9e7ce`、dim `#5d6f61`（`blackboard-reference.html:167-168`）；`.ai-console` 用 `#9db8a0`/`#7d8a80`（`parts.css:941-942`） |

### 线③ 工作台外壳 + 卡片基座 + 注意力 actor + 终端卡

| 环节 | 结论 | 证据 |
|---|---|---|
| ContentMode + 侧栏入口 + 渲染分支 + 恢复白名单 + 隐藏 composer | VERIFIED | `content-modes.ts:24`；`AIShellSidebar.tsx:234,416-424`；`AIShellLayout.tsx:5645-5647`、`1341-1344`、`6361` |
| 卡片基座契约（组合+注册表，非继承） | VERIFIED | `cardTypes.ts:19-34`、`cardRegistry.ts:13-63`、`WorkbenchCard.tsx:57-109`、`WorkbenchCardCompact.tsx:16-36` |
| 插件 compact 模式（compact=宿主元数据，无 iframe；展开=PluginIframe 全尺寸） | VERIFIED | `appCard.tsx:30-43`（`render` 仅 expanded 挂 iframe）；`WorkbenchCard.tsx:104-105`；测试 `WorkbenchSurface.test.tsx:134-151` |
| 布局引擎为 blackboard `layout()` 的真移植 | VERIFIED | `cardLayout.ts:25-33,46-67,80-109` 与 `blackboard-reference.html:319-334,367-404` 常量/算法逐项一致（MAINSLOT 61、MAINSLOT_NT 93、TERMSLOT、MAXSLOT/MAXSLOT_T、sideSlots x=3/77、railSlot x=84 w=16、HYSTERESIS=15）；测试 `cardLayout.test.ts:23-137` |
| 注意力 actor：lane/纯读/事件入站/持久化 | VERIFIED | `workbench.go:157`(RegisterLoop) `:163-198`(WithLoop/Internal/PureContext)、`events.go:43-78`(订阅转发)、`workbench.go:250-316`(persist.Persistent)；`cmd/internal/actorset/actorset.go:66`(RequirePersistent + DependsOn workspace/appmanager) |
| 投影协议两端生成、无手写 | VERIFIED | `schemas/workbench._6400.spore` + `workbench.ingest._6448.spore`；Go `pkg/domain/gen/workbench.gen.go:14-20,37-50`；TS `web/src/gen-types/workbench.ts`、`web/src/gen-clients/system/types.ts:8003-8020`、`web/src/gen-clients/system/registry.ts:1325-1331`；manifest 面 `gen/gmanifest.json:79686-79865`（5 public + 4 internal，与注册完全一致）、投影 `:82388-82394`(Snapshot/6401)。ingest 类型未泄漏到 TS 面（`gen-types` / `gen-clients` 无 Workbench*Ingest*） |
| **注意力 actor 接到工作台外壳** | **未接通（见 D1）** | `WorkbenchSurface.tsx:86` 用本地 `useWorkbenchLayout`；`useWorkbenchCards` 无人 import |
| 终端卡可用（真 shell 会话、召唤/退避/关闭） | VERIFIED | `useShellSession.ts:86-277`（先订阅再 open、`session_fetch(0)` 按 Idx 去重、写/缩放/关闭）、`terminalCard.tsx:31-60`、`terminalPresence.ts:97-133`、`WorkbenchSurface.tsx:59-77,164-167,181`；测试 `terminalIntegration.test.tsx`、`WorkbenchSurface.test.tsx:119-132,153-171` |
| 主题 token 亮暗两套 | VERIFIED | `theme/src/tokens.css:132-165`（light）/`:251-284`（dark）；使用点 `workbench-surface.css:20-117`、`workbenchTerminal.css:13-14,45,53` |

### 五面约束

| 面 | 结论 | 证据 |
|---|---|---|
| 注入面最小 | VERIFIED | `workbench.go:127-151`：`OnInit/OnStart(ctx actor.Context)` 只取 gospore seams（`Self/Lifecycle/Logger/Register/RegisterLoop/RegisterDomain/SubscribeEventKind`），仅嵌 `actor.Host`，无业务依赖容器 |
| callable 高频注册模式 | VERIFIED | 读 `handleSnapshot(actor.PureContext,…)`（`workbench.go:389`, 注册 `:177`）；全部写（public 4 个 + 4 个 ingress + drift tick）挂 `WithLoop(laneScore)`（`:163-198`）；stateful handler 内无 `Await`/网络 IO；高频路径（step/turn/drift）不落盘（`workbench.go:503-505`、`events.go:114-157` 无 `saveLocked`） |
| schema→codegen 无漂移 | VERIFIED（人工比对，非执行 check） | schema 7 结构体 ⇄ `workbench.gen.go` 6400-6406 / ingest 6448-6450 ⇄ `registry.ts` / `gmanifest.json` 字段序一致性均逐项核对通过；**`make gen-*-check` 未执行（无 shell）** |
| 无手写协议 | VERIFIED | 工作台前端只从 `gen-clients/*`、`gen-types/*` 取 wire 类型（`useWorkbenchCards.ts:3-9`）；`WorkbenchCardDescriptor` 是纯 UI 契约，非 wire |
| 无 localStorage | VERIFIED（新代码） | `web/src/ui/workbench/**`、`pkg/actor/workbench/**` 内 `localStorage|sessionStorage` 0 命中；`AIShellLayout.tsx`/`AIShellSidebar.tsx` 从工作流基点 `9d17b3af` 到 HEAD 的 diff 只含 workbench 模式与 color 透传，**未新增** localStorage 行。存量违规仍在别处（`AIShellLayout.tsx:255,349,642,1772-1778`、`AIComposer.tsx:818,823`、`AIShellSidebar.tsx:64,72`、`CommitModal.tsx:83,175`），非本次引入、本次也未清理 |

---

## 二、缺陷清单（按严重度）

### D1 [高] 注意力 actor 与工作台外壳完全未接通（线③核心承诺未闭环）
- `web/src/ui/workbench/useWorkbenchCards.ts:39` 是唯一消费生成投影客户端的模块（`web/src/gen-clients/workbench/projection-client.ts:6-12`），而它**没有任何调用点**（全仓 grep 只有自身定义 + 自身 import）。也没有单测文件。
- `web/src/ui/workbench/WorkbenchSurface.tsx:86` 用本地 hooks 组板：`useWorkbenchLayout.ts:57-78` 的 `boosts/pinnedIds/maximizedId` 全是 React state，`appCard.tsx:36` 给每张 app 卡固定 `score: 10`。
- 后果：`workbench.promote / set_hidden / set_pinned / upsert_card`（`gen/gmanifest.json:79763-79865`）**前端零调用**；actor 算出的 score/slot、以及它基于 step 关键词的终端召唤（`events.go:130-132`）都到不了板面。线③"注意力 actor"只在服务端存在并被 Go 侧测到（`pkg/runtime/workbench_attention_integration_test.go:20-99`），UI 上是另一套平行实现（布局引擎双份：`cardLayout.ts` 与 `pkg/actor/workbench/cards.go`）。
- 备注：两侧 card id 约定已对齐（Go `workbench.go:73-75` `term`/`app:`/`agent:` vs 前端 `terminalCard.tsx:9`、`appCard.tsx:7`），说明本意就是要接；缺的只是最后一跳。

### D2 [中] ❄ 冻结不冻结布局
- `useWorkbenchLayout.ts:78` 无条件 `computeLayout(...)`，`frozen` 只在 `select`（`:117`）和 `proposeCapture`（`:147`）做门禁；descriptor/`hidden` 变化仍会重排（例如冻结后再键入关键词召唤终端，`WorkbenchSurface.tsx:82-86` 的 hidden 集变化会把终端条插进来）。
- 参考定稿：`blackboard-reference.html:381`（`layout()` 冻结即 return）与 `:440`（`drift()` 冻结即 return）。语义未对齐。

### D3 [中] run command 控制台配色与"定稿基准"是两个值
- 卡片要求"与 `tmp/blackboard/blackboard.html` 终端卡一致"，规格 §"视觉与交互基准一律对照 `web/src/ui/workbench/blackboard-reference.html`"；参考 `.termbody` 为 body `#c9e7ce` / dim `#5d6f61` / `.p` `#7fd08a` / `.err` `#ff8480`（`blackboard-reference.html:167-168`）。
- `.ai-console` 用 body `#9db8a0` / dim `#7d8a80`（`parts.css:941-942`）——与卡片正文里写的 hex 一致，但与它点名的参考文件不一致。
- 同一交付的终端卡却用参考值：`workbenchTerminal.css:14` + `theme/src/tokens.css:161-162`（`#c9e7ce` / `#5d6f61`）。即：同一次交付里两套"终端皮肤"两种灰绿。需 owner 裁定以哪个为准（若以参考文件为准，`parts.css` + 其契约测试 `BashToolView.console.test.tsx:81-83` 需一并改）。

### D4 [中] "全 UI 卡片化"只落了 app + terminal 两种 kind
- `cardTypes.ts:12` 声明 `app|plugin|terminal|chat|file|note`；实际产出 descriptor 只有 `appCard.tsx:32`（`kind:'app'`）与 `terminalCard.tsx:45`（`kind:'terminal'`）。`chat/file/note`（对话/文件/外部内容）无任何 descriptor 或注册（`WorkbenchSurface.tsx:79-80` 只合并 app 卡 + 注册表）。验收句"工作台上所有 surface…统一为卡片"目前只覆盖 app 面板与终端。

### D5 [低] 注意力漂移语义与规格不符
- 规格 §3 写明"分数漂移 ±3~7/6s"、参考 `drift()` 是随机上调 +3..7 / 下调 −2..5（`blackboard-reference.html:439-448`）。
- 实现只做匀速 −2/6s 衰减、无上调（`workbench.go:83-88`、`:506-536`），代码注释承认是有意省略，但规格卡（`workbench-integration-spec` §3）与其 Decisions-so-far 未同步，属纸面漂移。

### D6 [低] 规格 §6 的 body 级主题挂载点缺失
- 规格要求 `web/src/application/workbench-surface.ts` 注入 body 级 class；该文件不存在，极光/纸纹改为 `.wb-board` 内部绝对定位（`workbench-surface.css:16-81`）。视觉效果等价，但规格点名的产物缺失。

### D7 [低] 8 个 locale 缺工作台文案 + 一处 CJK 注释
- `workbench.surface.*`、`shell.sidebar.workbenchMode`、`shell.workbench.commandPlaceholder` 只在 `web/src/i18n/locales/zh-CN.json:3005-3006,3404-3410` 与 `en-US.json:3003-3004,3402-3408`；其余 8 个 locale 靠 `provider.tsx:81` 回落 en-US（功能不坏，文案不本地化）。
- `web/src/ui/workbench/workbenchLayout.ts:5` 文档注释里留了中文 `候场轨`，会被 `scripts/check-i18n.py:56-73` 的未翻译扫描报出（非 strict 不阻断，属新增噪音）。

### D8 [低] actor 卡片元数据是占位
- `events.go:123-124,149-150` 每张 agent 卡一律 `title="Agent"`、`icon="bot"`；`:94` app 卡 title 用 appID 而非 manifest Name。actor 未接通板面（D1）所以当前无可见影响，接通前应补真名。

---

## 三、未能验证（本环境无 shell，无法执行）

- `go test ./pkg/actor/workbench/... ./pkg/runtime/... ./cmd/internal/actorset/...`（读到的测试：`cards_test.go`、`ingest_test.go`、`projection_test.go`、`workbench_attention_integration_test.go`、`bundle_visual_test.go`、`actorset.go:79-85` 断言，均无 skip）
- `cd web && npx tsc --noEmit`、`npx vitest run`（读到的测试：`WorkbenchSurface.test.tsx`、`cardLayout.test.ts`、`useWorkbenchLayout.test.tsx`、`cardRegistry.test.ts`、`appCard.test.tsx`、`terminal/*` 6 个文件；无 `.skip/.todo`）
- `make gen-schemas-check / gen-schema-ts-check / gen-static-fragment-check / gen-manifest-check / gen-sdk-check`（只做了人工逐项比对，见线①②③表内证据）
- `scripts/check-i18n.py`（只静态推演了 zh↔en parity 与 D7 的单条新增噪音）
- 亮暗两主题的截图/DOM 目视验收（卡片②"截图或 DOM 验证"）——测试里有 `data-theme` 轮换断言（`BashToolView.console.test.tsx:65-75`），但无真实渲染截图。

## 四、一句话结论（不作完成判定）

线①链路从我读到的代码看是端到端闭合的（schema→codegen→wire→三处前端调用点，哈希稳定有 golden 测试）；线②样式落地并有契约测试，但与它声称对齐的定稿文件存在 D3 的配色分歧；线③外壳/卡片基座/终端卡可用且移植保真，**唯独"注意力 actor"只完成了服务端一半（D1），未接入工作台板面**，另有 D2/D4 两处语义范围缺口。以上未跑任何构建/测试（无 shell），需 owner 或后续审查在可执行环境复核。
