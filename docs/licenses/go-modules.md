# Go 第三方依赖版权清单（sporemind）

> 审计日期：2026-08-23 ｜ 审计基准：根 `go.mod`（module `github.com/qomos-w/sporemind`，go 1.26.0）
> 审计方法：`go list -m -f` 导出依赖图 → 逐一读取 `$GOMODCACHE` 中各模块 LICENSE/COPYING 原文，按 SPDX 关键字分类；copyleft 扫描覆盖全部 493 个在图中模块的许可文本；再用 `go mod why -m` 确认风险模块是否实际链接进构建。

## 范围说明

- 本清单覆盖根 `go.mod` 的 **直接依赖**（28 个第三方模块）；传递依赖仅在涉及 copyleft（MPL）时单列于『风险项』。
- 一方组件不计入第三方：`github.com/qomos-w/gospore`、`github.com/qomos-w/spore`（`replace => ../gospore`、`../spore`），以及 `web/package.json` 的 `file:../shell`、`file:../theme`。
- 本地 fork 亦为第三方代码但按上游许可记录：`github.com/wailsapp/wails/v3` / `wails/webview2`（`replace => ../wails-v3-fork`，上游 MIT）、`golang.org/x/tools/gopls`（`replace => ../gopls/gopls`，上游 BSD-3-Clause）、`golang.org/x/tools`（`replace => ../gopls`，BSD-3-Clause）。

## 直接依赖清单（按 SPDX 分组）

### MIT

| 模块 | 版本 | SPDX | 主页 | 版权行 |
|---|---|---|---|---|
| github.com/aymanbagabas/go-pty | v0.2.2 | MIT | https://github.com/aymanbagabas/go-pty | Copyright (c) 2023 Ayman Bagabas |
| github.com/creack/pty | v1.1.24 | MIT | https://github.com/creack/pty | Copyright (c) 2011 Keith Rarick |
| github.com/getcharzp/go-ocr | v0.0.0-20260126073315-15e83dd6ccce | MIT | https://github.com/getcharzp/go-ocr | Copyright (c) 2025 getcharzp |
| github.com/go-chi/chi/v5 | v5.3.1 | MIT | https://github.com/go-chi/chi | Copyright (c) 2015-present Peter Kieltyka, Google Inc. |
| github.com/go-ole/go-ole | v1.3.0 | MIT | https://github.com/go-ole/go-ole | Copyright © 2013-2017 Yasuhiro Matsumoto |
| github.com/golang-jwt/jwt/v5 | v5.3.1 | MIT | https://github.com/golang-jwt/jwt | Copyright (c) 2012 Dave Grijalva; Copyright (c) 2021 golang-jwt maintainers |
| github.com/golang-migrate/migrate/v4 | v4.19.1 | MIT | https://github.com/golang-migrate/migrate | Copyright (c) 2016 Matthias Kadenbach; Copyright (c) 2018 Dale Hui |
| github.com/jackc/pgx/v5 | v5.10.0 | MIT | https://github.com/jackc/pgx | Copyright (c) 2013-2021 Jack Christensen |
| github.com/pkoukk/tiktoken-go | v0.1.8 | MIT | https://github.com/pkoukk/tiktoken-go | Copyright (c) 2023 ImmortalFog |
| github.com/wailsapp/wails/v3（本地 fork `../wails-v3-fork/v3`） | v3.0.0-alpha2.107 | MIT | https://github.com/wailsapp/wails | Copyright (c) 2018-Present Lea Anthony |
| github.com/wailsapp/wails/webview2（本地 fork `../wails-v3-fork/webview2`） | v1.0.27 | MIT | https://github.com/wailsapp/wails | Copyright (c) 2018-Present Lea Anthony |

小计：MIT 11 项（含 2 个 fork）。

### BSD

| 模块 | 版本 | SPDX | 主页 | 版权行 |
|---|---|---|---|---|
| github.com/fsnotify/fsnotify | v1.9.0 | BSD-3-Clause | https://github.com/fsnotify/fsnotify | Copyright © 2012 The Go Authors; Copyright © fsnotify Authors |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | https://github.com/google/uuid | Copyright (c) 2009,2014 Google Inc. |
| github.com/gorilla/websocket | v1.5.3 | BSD-3-Clause | https://github.com/gorilla/websocket | Copyright (c) 2013 The Gorilla WebSocket Authors |
| github.com/microcosm-cc/bluemonday | v1.0.27 | BSD-3-Clause | https://github.com/microcosm-cc/bluemonday | Copyright (c) 2014, David Kitchen |
| github.com/pkg/sftp | v1.13.10 | BSD-2-Clause | https://github.com/pkg/sftp | Copyright (c) 2013, Dave Cheney |
| github.com/redis/go-redis/v9 | v9.22.0 | BSD-3-Clause | https://github.com/redis/go-redis | Copyright (c) 2013 The github.com/redis/go-redis Authors |
| golang.org/x/crypto | v0.55.0 | BSD-3-Clause | https://cs.opensource.google/go/x/crypto | Copyright 2009 The Go Authors |
| golang.org/x/net | v0.58.0 | BSD-3-Clause | https://cs.opensource.google/go/x/net | Copyright 2009 The Go Authors |
| golang.org/x/oauth2 | v0.36.0 | BSD-3-Clause | https://cs.opensource.google/go/x/oauth2 | Copyright 2009 The Go Authors |
| golang.org/x/sync | v0.22.0 | BSD-3-Clause | https://cs.opensource.google/go/x/sync | Copyright 2009 The Go Authors |
| golang.org/x/tools/gopls（本地 fork `../gopls/gopls`） | v0.23.0 | BSD-3-Clause | https://cs.opensource.google/go/x/tools | Copyright 2009 The Go Authors |

小计：BSD 11 项（BSD-3-Clause 10 项 + BSD-2-Clause 1 项；`golang.org/x/crypto`、`x/net`、`x/sync` 等附带 PATENTS 谷歌专利授权文件，`x/oauth2`、`x/tools/gopls` 未附）。

### Apache-2.0

| 模块 | 版本 | SPDX | 主页 | 版权行 |
|---|---|---|---|---|
| github.com/ebitengine/purego | v0.10.1 | Apache-2.0 | https://github.com/ebitengine/purego | Copyright The Ebitengine Authors |
| github.com/fatedier/frp | v0.67.0 | Apache-2.0 | https://github.com/fatedier/frp | Copyright fatedier |
| github.com/fatedier/golib | v0.5.1 | Apache-2.0 | https://github.com/fatedier/golib | Copyright fatedier |
| github.com/go-git/go-git/v5 | v5.19.1 | Apache-2.0 | https://github.com/go-git/go-git | Copyright The go-git AUTHORS |
| gopkg.in/yaml.v2 | v2.4.0 | Apache-2.0（libyaml 移植部分为 MIT，见 LICENSE.libyaml） | https://github.com/go-yaml/yaml | Copyright 2011-2016 Canonical Ltd. |

小计：Apache-2.0 5 项。

### 其他（双许可 / 过渡期许可）

| 模块 | 版本 | SPDX | 主页 | 版权行 |
|---|---|---|---|---|
| github.com/modelcontextprotocol/go-sdk | v1.7.0 | MIT AND Apache-2.0（官方许可过渡期：新代码 Apache-2.0，旧贡献维持 MIT，按文件适用） | https://github.com/modelcontextprotocol/go-sdk | Copyright The MCP project contributors |

小计：1 项。

### 直接依赖合计

直接第三方依赖 **28 项**：MIT 11、BSD-3-Clause 10、BSD-2-Clause 1、Apache-2.0 5、MIT/Apache-2.0 双许可 1、MPL-2.0 0。直接依赖中**无任何 GPL/AGPL/LGPL/SSPL/MPL**。

## 全依赖图统计（含传递，493 个模块）

| SPDX | 数量 | 说明 |
|---|---|---|
| MIT | 208 | 含检出 205 + go-toast（Unlicense-OR-MIT 双许可，按 MIT 计）1 + 上游确认 MIT 但包内无 LICENSE 的 pp 与 go-localereader 各 1 |
| Apache-2.0 | 148 | |
| BSD-3-Clause | 107 | 含检出 106 + 无 LICENSE 的 go-qsort（按 Go 标准库派生计）1 |
| BSD-2-Clause | 18 | |
| ISC | 6 | |
| MPL-2.0 | 5 | 见『风险项』；另 filepath-securejoin 为逐文件 BSD-3 AND MPL-2.0 双许可（统计入 BSD-3） |
| 一方/自身模块 | 1 | `github.com/qomos-w/sporemind` 自身（`go list -m` 自引用行）；`github.com/qomos-w/gospore`、`github.com/qomos-w/spore`（replace 本地）不在此扫描范围，均不计第三方 |
| 合计 | 493 | |

GPL / AGPL / LGPL / SSPL 检出数：**0**（扫描全部模块许可文本，MPL 文本中提到 GPL/AGPL 仅为 MPL-2.0 第 3.3 节的兼容性说明，非本库许可；无独立 GPL 库）。

无 LICENSE 文件的 3 个传递模块（按上游仓库记录，限于离线审计未做在线复核）：
- `github.com/k0kubun/pp@v2.3.0+incompatible` —— 上游仓库以 MIT 发布，模块包内未含 LICENSE。
- `github.com/mattn/go-localereader@v0.0.1` —— mattn 系列仓库惯例 MIT，模块包内未含 LICENSE。
- `github.com/konoui/go-qsort@v0.1.0` —— 从 Go 标准库 `sort` 包派生（qsort.go/heapsort.go），按派生应视为 BSD-3-Clause；模块包内未含 LICENSE，**建议上游补证或替换该依赖**（低风险，代码量极小）。

## 风险项（copyleft / 受限许可）

> 规则：任何 GPL/AGPL/LGPL/SSPL/MPL/有限商业许可都单列。本次审计未发现 GPL/AGPL/LGPL/SSPL/商业受限许可；发现 **MPL-2.0** 家族 5+1 项，其中 2 项实际链接进构建：

### 已链接进构建的 MPL（真正进入发行物）

| 模块 | 版本 | 链接路径 | 说明 |
|---|---|---|---|
| github.com/hashicorp/yamux | v0.1.1 | `pkg/actor/frpinstance` → `github.com/fatedier/frp/client` | MPL-2.0 |
| github.com/cyphar/filepath-securejoin | v0.6.1 | `pkg/actor/project` → `github.com/go-git/go-git/v5` → `go-billy/v5/osfs` | 双许可 **BSD-3-Clause AND MPL-2.0**（逐文件声明，见 COPYING.md） |

### 仅存在于依赖图、未链接进构建的 MPL（供合规扫描留痕）

| 模块 | 版本 | 说明 |
|---|---|---|
| github.com/hashicorp/errwrap | v1.0.0 | MPL-2.0（go-multierror 的依赖，未被导入） |
| github.com/hashicorp/go-multierror | v1.1.1 | MPL-2.0（未被导入） |
| github.com/go-sql-driver/mysql | v1.5.0 | MPL-2.0（golang-migrate 的可选 MySQL 驱动子包，未被导入） |
| cyphar.com/go-pathrs | v0.2.1 | MPL-2.0（filepath-securejoin 的拆分子模块，未被单独导入） |

### 对发行的影响评估

- **MPL-2.0 是文件级弱 copyleft**，与 AGPL/GPL 的链接传染不同：MPL 义务仅及于「被修改的 MPL 文件」本身，不要求整个应用开源。sporemind（自身许可未定）可以**未经修改地链接/分发** MPL-2.0 库，前提是：
  1. 保留 MPL 许可文本与版权声明（发行物内嵌的第三方声明中须含 MPL 全文或对应 SPDX 标识与来源）；
  2. 若修改了 MPL 覆盖的源文件（yamux、filepath-securejoin 中标 MPL 的文件），修改后的这些文件必须以 MPL-2.0 发布并提供源代码（仅这部分文件，不含 sporemind 其他代码）。
- **filepath-securejoin 为逐文件双许可（BSD-3 AND MPL-2.0）**：对每个文件按其文件头声明的许可执行；本项目不修改 go-git 链路，视为仅引用，满足保留声明即可。
- **多端分发（Web/桌面/移动）影响相同**：Go 后端二进制（桌面端内置、Web/移动端经 frp/wails 桥接的服务端逻辑）只是「分发(redistribute)」二进制与许可文本，不触发 MPL 的源码公开义务；唯一触发点是修改 MPL 文件后对外分发修改版。当前 sporemind 未 fork 修改上述任一 MPL 库，风险等级为**低**。
- **建议**：在 About 页面第三方声明中加入 MPL-2.0 条目（yamux、filepath-securejoin、go-pathrs、go-sql-driver/mysql、hashicorp/errwrap、go-multierror 全部列出，即使未链接，便于未来重开传递依赖时不漏）；`go mod why` 定期复核确认链接集合不扩大。
- **CC-BY-SA 提示（非风险项）**：`github.com/opencontainers/go-digest` 的代码为 Apache-2.0，仅其文档（LICENSE.docs）为 CC-BY-SA-4.0；docs 不会进入二进制发行，无影响。

## 复核命令

```bash
go list -m -f '{{if .Indirect}}I{{else}}D{{end}} {{.Path}}@{{.Version}}' all   # 导出依赖图
go mod why -m github.com/hashicorp/yamux                                        # 确认链接路径
# go run github.com/google/go-licenses@latest report ./... 曾尝试执行，
# 因 go-licenses v1.6.0 不兼容 Go 1.26 标准库包遍历（'Package X does not have module info'）失败，
# 故按任务卡备用方案：go list -m + 模块缓存 LICENSE 原文逐项人工核对。
```