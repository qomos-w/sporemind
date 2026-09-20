# sporemind web 前端 npm 依赖版权审计

- **审计日期**: 2026-08-23
- **范围**: `web/package.json` 的 `dependencies` + `devDependencies`（dev 依赖在表中标注为开发期工具）
- **精确版本来源**: `web/package-lock.json`（lockfileVersion 3，条目为锁定解析版本）
- **方法（真实数据，非估算）**:
  1. 对每个直接依赖以锁定版本执行 `npm view <pkg>@<ver> license homepage repository.url --json`（npm registry 实测，79/79 成功）；
  2. 对重点包逐一拉取包内/上游 LICENSE 文件（unpkg / jsdelivr CDN）核验版权行；
  3. 全量锁文件 951 个包扫描 copyleft / 受限许可。
- **一方组件（不计入第三方）**: `@qomos/gospore-client`（file:../../gospore/web-client，自有，MIT）、`@qomos/spore-ts`、`@qomos/sporemind-shell`、`@qomos/sporemind-theme`。

## 汇总统计

直接依赖共 **83** 项：production 63（其中 4 项为 file: 一方组件）、devDependencies 20。计入第三方注册表包 **79** 个：

| SPDX | 数量 | 包 |
|---|---|---|
| MIT | 74 | 绝大多数（见分组表） |
| Apache-2.0 | 3 | echarts、class-variance-authority、typescript |
| ISC | 1 | lucide-react |
| Apache-2.0 OR MIT | 1 | vis-network（双许可，可取 MIT） |

**直接依赖无 GPL/AGPL/LGPL/SSPL/MPL/商业受限许可。** 传递依赖中的受限许可见文末「风险项」。版权行标注（npm author 字段）者为未逐字核验 LICENSE 原文、以 registry author 字段回填的近似值。

## 分组明细

### React 生态（运行时）

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| react | 19.2.5 | MIT | https://react.dev/ | Copyright (c) Meta Platforms, Inc. and affiliates.（已核验 LICENSE） |
| react-dom | 19.2.5 | MIT | https://react.dev/ | 同 react（Meta Platforms, Inc. and affiliates.） |
| @base-ui/react | 1.7.0 | MIT | https://base-ui.com | Copyright (c) 2019 Material-UI SAS（已核验 LICENSE） |
| @lobehub/icons-static-svg | 1.94.0 | MIT | https://github.com/lobehub/lobe-icons | Copyright (c) 2023 LobeHub（provider 品牌 SVG，MIT） |
| react-markdown | 10.1.0 | MIT | https://github.com/remarkjs/react-markdown | Copyright (c) Titus Wormer <tituswormer@gmail.com>（上游 remark 系） |
| remark-gfm | 4.0.1 | MIT | https://github.com/remarkjs/remark-gfm | Copyright (c) Titus Wormer <tituswormer@gmail.com>（已核验 LICENSE） |

### CodeMirror 6

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| @codemirror/lang-cpp | 6.0.3 | MIT | https://github.com/codemirror/lang-cpp | Copyright (C) 2018-2021 by Marijn Haverbeke and others（已核验） |
| @codemirror/lang-css | 6.3.1 | MIT | https://github.com/codemirror/lang-css | 同上（Marijn Haverbeke） |
| @codemirror/lang-go | 6.0.1 | MIT | https://github.com/codemirror/lang-go | 同上 |
| @codemirror/lang-html | 6.4.11 | MIT | https://github.com/codemirror/lang-html | 同上 |
| @codemirror/lang-java | 6.0.2 | MIT | https://github.com/codemirror/lang-java | 同上 |
| @codemirror/lang-javascript | 6.2.5 | MIT | https://github.com/codemirror/lang-javascript | 同上 |
| @codemirror/lang-json | 6.0.2 | MIT | https://github.com/codemirror/lang-json | 同上 |
| @codemirror/lang-markdown | 6.5.0 | MIT | https://github.com/codemirror/lang-markdown | 同上 |
| @codemirror/lang-python | 6.2.1 | MIT | https://github.com/codemirror/lang-python | 同上 |
| @codemirror/lang-rust | 6.0.2 | MIT | https://github.com/codemirror/lang-rust | 同上 |
| @codemirror/lang-sql | 6.10.0 | MIT | https://github.com/codemirror/lang-sql | 同上 |
| @codemirror/lang-xml | 6.1.0 | MIT | https://github.com/codemirror/lang-xml | 同上 |
| @codemirror/language | 6.12.4 | MIT | https://github.com/codemirror/language | 同上 |
| @codemirror/lint | 6.9.7 | MIT | https://github.com/codemirror/lint | 同上 |
| @codemirror/search | 6.7.1 | MIT | https://github.com/codemirror/search | 同上 |
| @codemirror/autocomplete | 6.20.3 | MIT | https://github.com/codemirror/autocomplete | 同上 |
| @codemirror/state | 6.7.0 | MIT | https://github.com/codemirror/state | 同上 |
| @codemirror/theme-one-dark | 6.1.3 | MIT | https://github.com/codemirror/theme-one-dark | 同上 |
| @codemirror/view | 6.43.4 | MIT | https://github.com/codemirror/view | 同上 |
| codemirror（basic-setup） | 6.0.2 | MIT | https://github.com/codemirror/basic-setup | 同上（Marijn Haverbeke） |
| w3c-keyname | 2.2.8 | MIT | https://github.com/marijnh/w3c-keyname | Marijn Haverbeke（npm author 字段） |

### ProseMirror

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| prosemirror-commands | 1.7.1 | MIT | https://github.com/prosemirror/prosemirror-commands | Copyright (C) 2015-2017 by Marijn Haverbeke <marijn@haverbeke.berlin> and others（已核验） |
| prosemirror-dropcursor | 1.8.3 | MIT | https://github.com/prosemirror/prosemirror-dropcursor | 同上 |
| prosemirror-gapcursor | 1.4.1 | MIT | https://github.com/prosemirror/prosemirror-gapcursor | 同上 |
| prosemirror-history | 1.5.0 | MIT | https://github.com/prosemirror/prosemirror-history | 同上 |
| prosemirror-inputrules | 1.5.1 | MIT | https://github.com/prosemirror/prosemirror-inputrules | 同上 |
| prosemirror-keymap | 1.2.3 | MIT | https://github.com/prosemirror/prosemirror-keymap | 同上 |
| prosemirror-markdown | 1.13.5 | MIT | https://github.com/prosemirror/prosemirror-markdown | 同上 |
| prosemirror-model | 1.25.11 | MIT | https://github.com/prosemirror/prosemirror-model | 同上（已核验 LICENSE） |
| prosemirror-state | 1.4.4 | MIT | https://github.com/prosemirror/prosemirror-state | 同上 |
| prosemirror-tables | 1.8.5 | MIT | https://github.com/ProseMirror/prosemirror-tables | 同上 |
| prosemirror-transform | 1.12.0 | MIT | https://github.com/prosemirror/prosemirror-transform | 同上 |
| prosemirror-view | 1.42.1 | MIT | https://github.com/prosemirror/prosemirror-view | 同上 |

### xterm.js（终端）

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| @xterm/xterm | 6.0.0 | MIT | https://github.com/xtermjs/xterm.js | Copyright (c) 2017-2019 The xterm.js authors；2014-2016 SourceLair Private Company；2012-2013 Christopher Jeffrey（已核验 LICENSE） |
| @xterm/addon-fit | 0.11.0 | MIT | https://github.com/xtermjs/xterm.js | 同 xterm.js |
| @xterm/addon-search | 0.16.0 | MIT | https://github.com/xtermjs/xterm.js | 同 xterm.js |
| @xterm/addon-serialize | 0.14.0 | MIT | https://github.com/xtermjs/xterm.js | 同 xterm.js |

### 图标

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| lucide-react | 1.8.0 | ISC | https://lucide.dev | Copyright (c) 2026 Lucide Icons and Contributors（已核验 LICENSE） |
| @iconify/react | 6.0.2 | MIT | https://iconify.design/ | license 字段 MIT（registry）；上游 iconify/iconify，包内未附 LICENSE 文件（版权行未逐字核验） |
| @iconify-json/material-icon-theme | 1.2.69 | MIT | https://icon-sets.iconify.design/material-icon-theme/ | 图标集作者 Material Extensions（material-extensions/vscode-material-icon-theme），license 指向其 MIT LICENSE（包内 info.json 实测） |

### 图表与可视化

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| echarts | 6.1.0 | Apache-2.0 | https://echarts.apache.org | NOTICE: "Apache ECharts Copyright 2017-2026 The Apache Software Foundation"（已核验 NOTICE；Apache-2.0 分发须保留 LICENSE + NOTICE） |
| mermaid | 11.16.0 | MIT | https://github.com/mermaid-js/mermaid | Copyright (c) 2014-2022 Knut Sveidqvist（已核验 LICENSE） |
| vis-network | 10.1.0 | Apache-2.0 OR MIT | https://visjs.github.io/vis-network/ | MIT 文本: Copyright (c) 2014-2017 Almende B.V.（已核验 LICENSE-MIT/LICENSE-APACHE-2.0 双文件） |

### Markdown 与代码高亮

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| markdown-it | 14.3.0 | MIT | https://github.com/markdown-it/markdown-it | Copyright (c) 2014 Vitaly Puzrin, Alex Kocharin（已核验 LICENSE） |
| markdown-it-table | 4.1.1 | MIT | https://github.com/torifat/markdown-it-table | Rifat Nabi（npm author 字段） |
| lowlight | 3.3.0 | MIT | https://github.com/wooorm/lowlight | Copyright (c) Titus Wormer <tituswormer@gmail.com>（已核验 LICENSE） |

### UI 工具类

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| clsx | 2.1.1 | MIT | https://github.com/lukeed/clsx | Copyright (c) Luke Edwards <luke.edwards05@gmail.com>（lukeed.com）（已核验 LICENSE） |
| class-variance-authority | 0.7.1 | Apache-2.0 | https://github.com/joe-bell/cva | Apache-2.0（已核验 LICENSE 全文为 Apache 文本）；作者 Joe Bell |
| tailwind-merge | 3.6.0 | MIT | https://github.com/dcastil/tailwind-merge | Copyright (c) 2021 Dany Castillo（已核验 LICENSE.md） |
| tw-animate-css | 1.4.0 | MIT | https://github.com/Wombosvideo/tw-animate-css | Luca Bosin（npm author 字段） |
| shadcn（CLI，声明于 dependencies，构建期使用） | 4.18.0 | MIT | https://github.com/shadcn-ui/ui | Copyright (c) 2023 shadcn（已核验 LICENSE.md） |
| 2 | 3.0.0 | MIT | https://github.com/lamansky/2 | Fr. John Lamansky（npm author 字段） |

### 平台运行时

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| @wailsio/runtime | 3.0.0-alpha.94 | MIT | https://v3.wails.io | Copyright (c) 2018-Present Lea Anthony（已核验上游 Wails LICENSE） |
| qrcode | 1.5.4 | MIT | http://github.com/soldair/node-qrcode | Copyright (c) 2012 Ryan Day（已核验 LICENSE） |

### 开发期工具（devDependencies）

| name | version | license | homepage | 版权行 |
|---|---|---|---|---|
| vite | 6.4.2 | MIT | https://vite.dev | Copyright (c) 2019-present, VoidZero Inc. and Vite contributors（已核验 LICENSE.md） |
| vitest | 4.1.5 | MIT | https://vitest.dev | Copyright (c) 2021-Present VoidZero Inc. and Vitest contributors（已核验 LICENSE.md） |
| @vitest/ui | 4.1.5 | MIT | https://vitest.dev/guide/ui | 同 vitest（VoidZero Inc.） |
| @vitejs/plugin-react | 4.7.0 | MIT | https://github.com/vitejs/vite-plugin-react | Evan You（npm author 字段） |
| typescript | 5.8.3 | Apache-2.0 | https://www.typescriptlang.org/ | Apache-2.0（已核验 LICENSE.txt）；© Microsoft Corporation |
| tsx | 4.21.0 | MIT | https://tsx.is | Copyright (c) Hiroki Osame <hiroki.osame@gmail.com>（已核验 LICENSE） |
| tailwindcss | 4.3.3 | MIT | https://tailwindcss.com | Copyright (c) Tailwind Labs, Inc.（已核验 LICENSE） |
| @tailwindcss/postcss | 4.3.3 | MIT | https://tailwindcss.com | Copyright (c) Tailwind Labs, Inc. |
| postcss | 8.5.26 | MIT | https://postcss.org/ | Copyright 2013 Andrey Sitnik <andrey@sitnik.es>（已核验 LICENSE） |
| happy-dom | 20.9.0 | MIT | https://github.com/capricorn86/happy-dom | Copyright (c) 2019 David Ortner (capricorn86)（已核验 LICENSE） |
| eventsource | 3.0.7 | MIT | https://github.com/EventSource/eventsource | Copyright (c) EventSource GitHub organisation（已核验 LICENSE） |
| @testing-library/react | 16.3.2 | MIT | https://github.com/testing-library/react-testing-library | Copyright (c) 2017 Kent C. Dodds（npm author 字段） |
| @testing-library/jest-dom | 7.0.1 | MIT | https://github.com/testing-library/jest-dom | Ernesto Garcia（npm author 字段） |
| @testing-library/user-event | 14.6.5 | MIT | https://github.com/testing-library/user-event | Giorgio Polvara（npm author 字段） |
| @types/react | 19.2.14 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped（types@microsoft.com） |
| @types/react-dom | 19.2.3 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped |
| @types/node | 22.19.18 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped |
| @types/hast | 3.0.5 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped |
| @types/qrcode | 1.5.6 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped |
| @types/markdown-it | 14.1.2 | MIT | https://github.com/DefinitelyTyped/DefinitelyTyped | © DefinitelyTyped |

### 一方组件（file: 依赖，不计入第三方）

| name | 说明 |
|---|---|
| @qomos/gospore-client | file:../../gospore/web-client，qomos-w 自有（其 package.json license 字段为 MIT） |
| @qomos/spore-ts | file:../../spore/ts，qomos-w 自有 |
| @qomos/sporemind-shell | file:../shell，qomos-w 自有 |
| @qomos/sporemind-theme | file:../theme，qomos-w 自有 |

## 风险项

**直接依赖中不存在 copyleft 或商业受限许可**，以下为传递依赖/分发义务类风险：

### R1. dompurify 3.4.12 — (MPL-2.0 OR Apache-2.0)，经 mermaid 进入运行时包
- 双许可，选择其中任意一个即可合规；取 **Apache-2.0**（宽松）处理，随前端产物分发时需包含 Apache-2.0 许可证文本。
- 影响: 低（双许可可选 Apache-2.0，仅署名/许可文本义务）。

### R2. lightningcss（1.32.0 + 10 平台二进制）— MPL-2.0，经 @tailwindcss/node（tailwindcss 4.x）引入
- 纯**开发期** CSS 编译工具（构建期运行，不进入前端产物），源文件未被修改分发。
- MPL-2.0 为文件级 copyleft；不改源、不并入发行包（仅构建时使用）时无传染义务，但建议在第三方声明中保留其许可证文本。
- 影响: 低（仅构建期工具）。

### R3. caniuse-lite 1.0.30001788 — CC-BY-4.0（数据许可），经 browserslist ← @babel/helper-compilation-targets（@vitejs/plugin-react，dev）与 shadcn CLI 引入
- 开发期浏览器支持数据，构建期使用；CC-BY-4.0 要求署名（caniuse 数据作者）。
- 影响: 低（构建期、仅署名义务）。

### R4. khroma 2.1.0 — npm 包 **未声明 license 字段**（传递依赖，经 mermaid 进入运行时包）
- 上游仓库（fabiospampinato/khroma）实际为 MIT（仓库 LICENSE），但 npm 包 metadata 无 license 字段、包内无 LICENSE 文件。
- 影响: 低，但需留意 —— 建议升级 mermaid 或向 mermaid 侧确认 khroma 授权；最稳妥做法是在第三方声明中纳入 khroma 并按 MIT 处理（以仓库 LICENSE 为准）。

### R5. Apache-2.0 署名/NOTICE 义务（运行时: echarts；开发期: typescript、class-variance-authority）
- echarts 的 NOTICE 文件（"Apache ECharts Copyright 2017-2026 The Apache Software Foundation"）与 Apache-2.0 LICENSE 文本需随发行保留。
- typescript / cva 为开发期工具，不打包进产物，无分发义务；如纳入 About 第三方声明则附 LICENSE 文本即可。
- 影响: 低（标准署名义务，About 声明需包含 Apache-2.0 文本 + echarts NOTICE）。

### R6. ISC（lucide-react）
- ISC 许可宽松，允许闭源捆绑，但须保留版权与许可声明（"Copyright (c) 2026 Lucide Icons and Contributors"）。About 声明需包含该行。
- 影响: 低（署名义务）。

### R7. 未知/缺失 license 字段的传递包
- 全量锁文件 951 个包中仅 khroma 缺失 license 字段（R4）；其余全部有 SPDX 可识别字段，无 GPL/AGPL/LGPL/SSPL/商业受限。
- `robust-predicates`（Unlicense，经 delaunator/d3-delaunay，mermaid 运行时）为公共领域放弃声明，无义务。
- `w3c-keyname` 等 MIT 包版权行以 npm author 回填的，如上表标注。

## 结论

79 个第三方直接注册表依赖全部为宽松许可（MIT 74 / Apache-2.0 3 / ISC 1 / Apache-2.0 OR MIT 1），无 copyleft，可直接闭源分发；唯一真正需要写入「第三方版权声明」资产的义务项是: 全部 MIT/ISC/Apache 包的许可证文本（重点: Apache-2.0 的 echarts NOTICE）、R1 的 dompurify（按 Apache-2.0 处理）、R2/R3 构建期工具可并入声明文本、R4 的 khroma 按 MIT 处理并留意。字体/图标资产见 `docs/licenses/assets.md`。