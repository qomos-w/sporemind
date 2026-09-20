<div align="center">

[English](README.en.md) | **简体中文**

<img src="assets/icon.svg" width="110" alt="sporemind" />

# SporeMind

**AI 原生的 Agent 开发工作台**

一切提供函数的扩展都是组件,SporeMind 用脚本与工作流编排它们——多智能体协作,本地优先,全部开源于 [www.sporemind.ai](https://www.sporemind.ai)。

[![Release](https://img.shields.io/badge/release-v0.5.0-7c5cff?logo=github)](https://github.com/qomos-w/sporemind/releases)
[![License](https://img.shields.io/badge/license-AGPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go)](https://go.dev)
[![Plugin SDK](https://img.shields.io/badge/%E6%8F%92%E4%BB%B6%20SDK-MIT-green)](sporemind-plugin-sdk/LICENSE)
[![Platform](https://img.shields.io/badge/%E5%B9%B3%E5%8F%B0-Windows%20%7C%20macOS%20%7C%20Linux%20%7C%20Android-lightgrey)](#构建)

**[⬇ 下载桌面版](https://www.sporemind.ai)** · Windows v0.3 现已可用,macOS / Android 开发中

</div>

---

## 组件生态

**比插件更多:一切提供函数的扩展都是组件。** 三条轨道、同一个模型——组件向运行时注册函数,脚本与工作流把它们接线成管线。

| 轨道 | 说明 |
|---|---|
| 🧬 **Native Plugin** | Go 原生深度集成——进程内运行,或走子进程 frame 协议 |
| 🔌 **MCP Server** | 标准 MCP 生态直插,外部工具开箱即入运行时 |
| 📦 **Bundle** | 内置能力包:调试、教学、工作流工具,装好即用 |

## 编排

组件提供函数,**Spore Script 与可视化工作流**决定它们何时、以什么顺序运行。

```spore
// fetch-and-translate.spore — 组件提供函数,脚本负责编排
fn run(task) {
  page    = http.fetch(task.url)          // 组件函数
  summary = ai.summarize(page.text)
  out     = ai.translate(summary, task.lang)
  kb.store("articles", out)               // 存入知识库
  notify.send("done", out.title)
}
```

- **脚本调用函数** —— 强类型 Spore Script 调用任意组件函数,schema 先行、类型安全
- **工作流即程序** —— 用 `/workflow` 描述目标,得到任务卡拓扑图:节点按依赖连线,上下文在节点间流动
- **多智能体协作** —— 十个内置角色自由搭配,子 agent 并行推进,全程可中断、可回放

## 原则

| | |
|---|---|
| 🏠 **本地优先** | 数据默认不出机器,离线可用,云只是增强 |
| 🔑 **BYOK** | OpenAI、Anthropic、Gemini、Ollama 等,密钥归你,永不锁定 |
| 🔍 **结构化审计** | turn / step / request 三级账本,每个决策可追溯、可回放 |
| 🌍 **完全开源** | 运行时到组件生态全部源码公开,自己读、自己审、自己改 |

## 底座

构建于 [gospore](https://github.com/qomos-w/gospore)(actor 编排引擎)与 [spore](https://github.com/qomos-w/spore)(schema、codec、身份与脚本层)之上:agent、工具、面板与 LLM 主循环运行在同一棵被监督的 actor 树里,端到端强类型契约,行为热重载,签名加密的插件工坊(Ed25519 + AES-GCM)。

## 构建

> 裸 `go build ./...` 需先具备生成资产(web 前端 `dist/`、`sporemind.apk`、tiktoken 数据文件)——由 Makefile 目标自动处理。Go 1.27+ · Node.js / bun · Wails CLI v3。

```bash
make dev-desktop      # 开发热重载
make build-desktop    # 桌面构建
make release-desktop  # 发布构建
make build-apk        # Android apk → ./build/sporemind-v{X.Y.Z}.apk
go test ./...         # 测试
```

## 目录结构

| 路径 | 内容 |
|---|---|
| `pkg/actor/...` | actor:agent、appmanager、browsermanager、computeruse、aiaggregator、lspserver、storeclient 等 |
| `pkg/desktop/` | Wails 桌面宿主 |
| `web/` | 前端(Vite + React) |
| `schemas/` | spore schema —— 跨语言契约的唯一事实来源 |
| `cmd/` | server / codegen 入口 |
| `sporemind-plugin-sdk/` | 插件 SDK(MIT,仓内模块,go.mod replace) |
| `CLAUDE.md` | 贡献者必读的工程约束与惯例 |

## 许可证

[AGPL-3.0](LICENSE) · 插件 SDK 单独 [MIT](sporemind-plugin-sdk/LICENSE)——第三方插件可商用,不承担 AGPL 义务。

## 友链

- [LINUX DO](https://linux.do) —— 本项目首次分享的开发者社区

<div align="center">

**[www.sporemind.ai](https://www.sporemind.ai)**

</div>
