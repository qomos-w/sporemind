---
id: skill:design-taste
type: skill
name: design-taste
description: Anti-slop frontend design skill for landing pages, portfolios, and redesigns. Reads the brief, infers design direction via three dials, and ships interfaces that do not look templated. Enforces typography/color/layout discipline, image-asset strategy, AI-tell bans, and a mandatory pre-flight check. Adapted from Leonxlnx/taste-skill.
when_to_use: |
  - The user asks for a landing page, portfolio, marketing site, editorial page, or a visual redesign (落地页, 作品集, 视觉重设计).
  - The user wants a real visual design (not a wireframe) for a frontend surface.
context: inline
---

# Design Taste (Anti-Slop Frontend Skill)

> 适用：落地页、作品集、营销页、编辑页、重设计。**不适用**：仪表盘、数据表、多步表单、代码编辑器、原生移动端——这些走对应设计系统（Fluent / Carbon / Polaris / Apple HIG）。
> 所有规则**按需触发**：先读 brief，只取适用的部分。本 skill 负责最终视觉。

## 0. Brief Inference（先读场子）

动手前推断 6 个信号：页面类型（landing/portfolio/editorial/redesign）、用户用的 vibe 词（minimalist / Awwwards / brutalist / premium / Apple-y）、参考链接与截图、受众（B2B 采购 vs 消费者 vs 招聘方）、已有品牌资产、隐性约束（无障碍/公共部门/信任优先）。

**输出一行 Design Read**："Reading this as: `<页面类型>` for `<受众>`, with a `<vibe>` language, leaning toward `<设计系统或美学家族>`." 若 brief 有真实分歧，只问一个问题，不问一堆；能推断就不要问。

## 1. 三个旋钮（Dial）

| Dial | 1 | 10 |
|---|---|---|
| `DESIGN_VARIANCE` | 完美对称 | 艺术混乱 |
| `MOTION_INTENSITY` | 静态 | 电影级物理 |
| `VISUAL_DENSITY` | 画廊留白 | 驾驶舱密集 |

基线 `8 / 6 / 4`。按信号调整：minimalist→5-6/3-4/2-3；premium consumer→7-8/5-7/3-4；Awwwards 实验→9-10/8-10/3-4；信任优先→3-4/2-3/4-5；redesign-preserve→对齐现状；redesign-overhaul→前两项 +2。

**移动端降级**：VARIANCE 4-10 的非对称布局在 <768px 必须坍缩为单列。

## 2. 设计系统选择

Brief 读起来像微软企业级→Fluent；Material 风味→Material 3 tokens；IBM B2B→Carbon；Shopify→Polaris；Atlassian→Atlaskit；GitHub 风格→Primer；现代 React 基础→Radix Themes；自有组件→shadcn/ui（**禁止默认状态出场**，必须改圆角/颜色/阴影/字体）。美学方向（glassmorphism/bento/brutalism/editorial/aurora）没有官方包，用原生 CSS + Tailwind 实现并诚实标注。**一个项目一个系统，不混。**

## 3. 排版

- 展示标题默认 `text-4xl md:text-6xl tracking-tighter leading-none`；正文 `max-w-[65ch]`。
- **Sans 首选**：Geist / Outfit / Cabinet Grotesk / Satoshi。Inter 只在用户明确要求中立风格或无障碍优先时用。
- **Serif 纪律**：serif 绝不是默认。"有创意感"不是理由。只有品牌明确指定、或确实是编辑/奢侈/出版美学且能说清理由时才用。禁默认用 Fraunces / Instrument_Serif。强调用同字体的 italic/bold，**禁止 sans 标题里塞一个 serif 词**。
- **Italic descender**：斜体词含 `y g j p q` 时 `leading` 至少 1.1 并留 `pb-1`，否则裁切。

## 4. 色彩

- 最多 1 个强调色，饱和度 <80%。禁纯黑 `#000`、禁霓虹外发光、禁过饱和强调、禁大面积渐变标题字。
- **LILA RULE**：禁默认 AI 紫/蓝光晕。用中性底（Zinc/Slate/Stone）+ 高对比单色强调。
- **COLOR CONSISTENCY LOCK**：强调色选定后全页一致，第七节不许突然换蓝 CTA。
- **PREMIUM-CONSUMER 调色板禁令**：高端消费品 brief（厨具/养生/手作/奢侈）禁默认米黄+黄铜+oxblood+浓缩咖啡色系（`#f5f1ea`/`#b08947`/`#1a1714` 等）。换用：冷奢银灰、深绿+骨色+琥珀、纯黑+暖褐、钴蓝+奶油、陶土+板岩、橄榄+砖红、纯单色+一个饱和点缀。连续项目不许重复同一家族。
- **PAGE THEME LOCK**：整页一个主题（light/dark/auto），区块不许中途反转明暗。

## 5. 布局纪律（硬伤规则）

- **Hero 必须一屏放下**：标题 ≤2 行，副文案 ≤20 词且 ≤4 行，CTA 不滚动可见，顶部 padding ≤6rem，文字元素 ≤4 个（eyebrow 或品牌条 + 标题 + 副文案 + CTA）。CTA 下方的小 tagline、信任条、特性列表全部移到下面独立区块。
- **导航桌面单行**，高度 ≤80px。
- **ANTI-CENTER**：VARIANCE>4 时避免居中 hero，用 50/50 分屏、左文右图、非对称留白。
- **Section 布局不重复**：8 个区块至少 4 种布局家族；图文分屏 zigzag 最多连续 2 次。
- **EYEBROW 限量（最容易违反）**：小写宽间距标签（eyebrow）每 3 个区块最多 1 个，hero 计 1。机械检查：`uppercase tracking` 出现次数 ≤ ceil(区块数/3)。默认直接不写。
- **SPLIT-HEADER BAN**：禁"左大标题+右上角漂浮小段落"，要解释就竖向堆叠（标题在上，正文 ≤65ch）。
- **Bento**：单元格数 = 内容数（不许空格）；至少 2-3 格有真实视觉变化（图片/渐变/纹理），不许 6 个白底文字卡。
- **SHAPE LOCK**：全页一套圆角体系（全直角/全 12-16px/全 pill），混用必须有文档化规则且处处遵守。
- 卡片只在高度传达真实层级时用，否则用留白/分隔线分组。

## 6. 内容与文案

- **默认区块形状**：短标题 ≤8 词 + 副段 ≤25 词 + 一个视觉资产或一个 CTA。
- **长列表换组件**：>5 项不用 `<ul> divide-y`，改双列分组/卡片网格/tab/横向 snap/轮播。规格表禁每行 hairline，改 2 列卡片或分簇。
- **真实文案**：禁 John Doe / Acme / 99.99% / "Elevate/Seamless/Unleash"。用地道的、语境真实的名字与有机数据（`47.2%`）。
- **Copy 自审（交付前必做）**：重读页面上每个可见字符串，重写语法破碎、指代不明、AI 味的句子。
- **引言 ≤3 行**，署名 = 名字 + 角色（+公司）。
- **EM-DASH 全面禁止**（`—` 和作分隔的 `–`）：标题、eyebrow、按钮、正文、引言、署名、alt 文本里一个都不许有。用连字符 `-`、句号、逗号、括号或冒号。日期/数字区间也用 `-`。

## 7. 图像资产策略

1. 有图像生成工具就用它生成各区块专用图（正确宽高比）。
2. 没有用真实图源：`https://picsum.photos/seed/{描述}/{w}/{h}` 或 brief 提供的 URL。
3. 都没有就留带标注的占位槽并告诉用户哪里需要图。
4. **纯文字页不是极简，是未完成**——至少 2-3 张真实图片。
5. **禁 div 拼的假截图**、禁手绘装饰 SVG、禁图片上叠标签 pill、禁装饰性照片署名。
6. Logo 墙 = 只有 logo（Simple Icons / devicon / 生成的字母标），不印行业标签；放在 hero **下方**独立区块。
7. 图标用库（Phosphor / HugeIcons / Radix / Tabler），一个项目一族，不手绘 SVG path。

## 8. 动效

- 每个动效必须一句话说清目的（层级/叙事/反馈/状态转移），说不清就删。
- 只动 `transform` 和 `opacity`；禁 `window.addEventListener('scroll')`、禁滚动值进 React state（用 motion value）。
- MOTION>3 必须处理 `prefers-reduced-motion`；motion>5 的页面必须真的在动，否则降到 3 交静态页。
- 跑马灯全页最多 1 个。连续值（鼠标/滚动进度）禁 `useState`。
- 性能目标：LCP<2.5s、INP<200ms、CLS<0.1；z-index 只用于系统层级（sticky 导航/弹层），不撒 `z-50`。
- 全高区块用 `min-h-[100dvh]`，不用 `h-screen`（移动端地址栏跳动）。

## 9. 交互状态与表单

- 完整状态周期：骨架屏（匹配最终布局形状）、空态（说明如何填充）、内联错误态；`:active` 给 `scale-[0.98]` 触感。
- **CTA 不换行**（桌面单行，标签 ≤3 词）；**同意图 CTA 全页一个标签**（"联系我们"/"开始合作"=同意图，只留一个）。
- 按钮文字对比度 ≥4.5:1（大字 3:1）；ghost 按钮压图必须加 scrim/描边。表单输入/占位/焦点环/错误文同样过 AA。
- Label 在输入框上方，禁 placeholder 当 label。

## 10. 高频 AI Tell（硬禁）

- Hero 里的版本标签（V0.6 / BETA / EARLY ACCESS）；区块编号 eyebrow（`00 / INDEX`）；`01 / 4` 分页标签；滚动提示（`↓ scroll`，用户知道什么是滚动）。
- 中点 `·` 每行最多 1 个，不作为万能分隔符；装饰性彩色状态点默认零个（只有真实语义状态可用）。
- 营销页页脚的版本号（`v1.4.2`）；eyebrow 下的元句子（"Each of these is a feature we ship today..."）；hero 底部装饰词带（`DESIGN · BUILD · SHIP`）。
- "Quietly in use at"、"From the field" 式诗意标签——用平实功能标签或省略。
- 城市/时间/天气条（`LIS 14:23 · 18°C`）除非 brief 真是跨时区工作室/旅行品牌。
- 通用步骤标签（"Stage 1 / Phase 01"）——步骤内容本身就是标签。
- 旋转 90° 竖排装饰文字；纯装饰十字线/发丝网格。

## 11. 重设计协议

先判模式：greenfield / redesign-preserve / redesign-overhaul。重设计先审计：品牌 token、信息架构、内容块、要保留的签名模式、要退休的 slop。现代化杠杆按序用：① 字体刷新 ② 间距节奏 ③ 色彩重校 ④ 动效层 ⑤ hero 重组 ⑥ 整区块替换。**绝不静默改动**：URL 结构、主导航标签、表单字段名/顺序、logo、法律文案。

## 12. Pre-Flight Check（交付前逐项过，任何一项不过就是没做完）

- Design Read 已声明一行；三个 dial 值显式且有理由。
- 零 em-dash；整页一个主题；强调色全页一致；圆角体系一致。
- Hero 一屏放下（标题≤2行/副文≤20词/CTA 可见/padding≤6rem/文字元素≤4）。
- Eyebrow 数 ≤ ceil(区块数/3)；无 split-header；无 3 连 zigzag；无同意图双 CTA；导航单行 ≤80px。
- Bento 格数=内容数且 ≥2 格有视觉变化；logo 墙只有 logo 且在 hero 下方。
- 每个可见字符串自审过；每个动效能一句话说清目的；跑马灯 ≤1。
- 真实图片就位，无 div 假截图、无图上 pill、无装饰性照片署名。
- 无滚动提示、无版本标签、无城市天气条、无装饰状态点。
- 按钮/表单对比度过 AA；动效有 reduced-motion 降级；CTA 桌面单行。
- 空/加载/错误态齐备；一个设计系统到底。

任何一项勾不掉 → 先修再交付。