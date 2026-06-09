/* Reasonix Redesign — mock data + scripted agent turn.
   Chinese-first copy reused from the real app vocabulary. */
window.reasonixData = {
  directions: [
    { id: "slate", name: "Slate", zh: "精炼", note: "原生", desc: "冷调近黑配品牌蓝,克制专业的原生工作台。" },
    { id: "carbon", name: "Carbon", zh: "深邃", note: "高级", desc: "暖炭黑配青绿强调,质感更厚、对比更足。" },
    { id: "nocturne", name: "Nocturne", zh: "柔和", note: "呼吸", desc: "柔紫夜色配大圆角,弹性呼吸、偏阅读。" },
    { id: "amber", name: "Amber", zh: "琥珀", note: "暖阳", desc: "浅暖底配清爽橙,明亮亲和(亦含深色)。" },
  ],

  // Session tabs across the top of the window.
  tabs: [
    { id: "t1", title: "重新设计桌面端 UI", scope: "desktop/frontend", mode: "plan", dot: "run" },
    { id: "t2", title: "review provider cache", scope: "internal/provider", mode: "normal", dot: "ok" },
    { id: "t3", title: "MCP transport changes", scope: "internal/mcp", mode: "normal", dot: "idle" },
  ],

  // Left sidebar: project workspaces, each with sessions.
  projects: [
    {
      id: "p-frontend",
      name: "DeepSeek-Reasonix",
      tone: "accent",
      open: true,
      sessions: [
        { id: "s1", title: "重新设计桌面端 UI", meta: "当前 · 流式中", tone: "accent", unread: 0, active: true },
        { id: "s2", title: "refine mac sidebar 交互", meta: "今天 14:20", tone: "blue", unread: 2 },
        { id: "s3", title: "bot channel settings UI", meta: "今天 09:05", tone: "green", unread: 0 },
      ],
    },
    {
      id: "p-kernel",
      name: "go-kernel",
      tone: "blue",
      open: true,
      sessions: [
        { id: "s4", title: "review provider cache stability", meta: "昨天", tone: "blue", unread: 0 },
        { id: "s5", title: "summarize MCP transport changes", meta: "6月7日", tone: "muted", unread: 0 },
      ],
    },
    {
      id: "p-global",
      name: "Global",
      tone: "amber",
      open: false,
      sessions: [
        { id: "s6", title: "环境 uv 与 Python 升级", meta: "6月5日", tone: "muted", unread: 0 },
      ],
    },
  ],

  quickPrompts: [
    { icon: "layout", text: "整体审查 desktop/frontend 的 UI 层级与密度" },
    { icon: "wand", text: "把右侧工作区做成更克制的原生 inspector" },
    { icon: "git", text: "总结本分支相对 main-v2 的全部改动" },
    { icon: "compass", text: "对比 Claude / Arc 的对话区与输入框节奏" },
  ],

  // Dock file tree (single column state).
  files: [
    { id: "f-desktop", name: "desktop", type: "folder", depth: 0, open: true },
    { id: "f-frontend", name: "frontend", type: "folder", depth: 1, open: true },
    { id: "f-src", name: "src", type: "folder", depth: 2, open: true },
    { id: "f-app", name: "App.tsx", type: "file", depth: 3, badge: "M", lang: "tsx" },
    { id: "f-styles", name: "styles.css", type: "file", depth: 3, badge: "M", lang: "css", active: true },
    { id: "f-components", name: "components", type: "folder", depth: 3, open: false },
    { id: "f-main", name: "main.tsx", type: "file", depth: 3, lang: "tsx" },
    { id: "f-readme", name: "README.zh-CN.md", type: "file", depth: 0, lang: "md" },
    { id: "f-gomod", name: "go.mod", type: "file", depth: 0, lang: "mod" },
  ],

  // Code shown when a file is opened (dock split → viewer column).
  fileContents: {
    "f-styles": {
      name: "styles.css",
      path: "desktop/frontend/src/styles.css",
      lang: "css",
      lines: [
        { t: "comment", s: "/* Reasonix desktop — design tokens */" },
        { t: "sel", s: ":root {" },
        { t: "prop", s: "  --bg: #faf8f5;" },
        { t: "prop", s: "  --surface: #ffffff;" },
        { t: "prop", s: "  --text: #1b1a26;" },
        { t: "prop", s: "  --accent: #6d5efc;" },
        { t: "prop", s: "  --radius: 14px;" },
        { t: "plain", s: "}" },
        { t: "blank", s: "" },
        { t: "sel", s: '[data-theme="dark"] {' },
        { t: "prop", s: "  --bg: #0e0f1a;" },
        { t: "prop", s: "  --surface: #161724;" },
        { t: "prop", s: "  --text: #ecebf7;" },
        { t: "plain", s: "}" },
        { t: "blank", s: "" },
        { t: "sel", s: ".composer {" },
        { t: "prop", s: "  display: grid;" },
        { t: "prop", s: "  gap: 10px;" },
        { t: "prop", s: "  border-radius: var(--radius);" },
        { t: "prop", s: "  background: var(--surface);" },
        { t: "plain", s: "}" },
      ],
    },
    "f-app": {
      name: "App.tsx",
      path: "desktop/frontend/src/App.tsx",
      lang: "tsx",
      lines: [
        { t: "comment", s: "// Desktop shell — three-pane workbench" },
        { t: "kw", s: "export default function App() {" },
        { t: "plain", s: "  const [dock, setDock] = useState<DockState>('files')" },
        { t: "plain", s: "  const [openFile, setOpenFile] = useState<File | null>(null)" },
        { t: "blank", s: "" },
        { t: "kw", s: "  return (" },
        { t: "tag", s: "    <Window>" },
        { t: "tag", s: "      <Sidebar collapsible />" },
        { t: "tag", s: "      <Chat>" },
        { t: "tag", s: "        <Transcript />" },
        { t: "tag", s: "        <Composer />" },
        { t: "tag", s: "      </Chat>" },
        { t: "tag", s: "      <Dock split={!!openFile} onOpen={setOpenFile} />" },
        { t: "tag", s: "    </Window>" },
        { t: "kw", s: "  )" },
        { t: "plain", s: "}" },
      ],
    },
  },

  // Dock "changes" tab.
  changes: [
    { file: "desktop/frontend/src/App.tsx", status: "modified", add: 142, del: 38 },
    { file: "desktop/frontend/src/styles.css", status: "modified", add: 311, del: 96 },
    { file: "designs/reasonix-redesign/", status: "added", add: 6, del: 0 },
  ],

  // Dock "context" tab metrics.
  context: [
    { label: "上下文占用", value: "33%", tone: "accent" },
    { label: "最近缓存命中", value: "99.12%", tone: "ok" },
    { label: "平均缓存命中", value: "91.04%", tone: "muted" },
    { label: "后台任务", value: "1", tone: "blue" },
  ],

  commandItems: [
    { icon: "pen", label: "新建会话", meta: "开启一个新的 Reasonix 话题", kbd: "⌘N" },
    { icon: "git", label: "查看本次改动", meta: "3 个文件 · +459 −134", kbd: "⌘⇧G" },
    { icon: "folder", label: "打开文件…", meta: "在工作区中检索并打开", kbd: "⌘P" },
    { icon: "brain", label: "切换模型", meta: "DeepSeek-R1 · effort auto", kbd: "" },
    { icon: "sliders", label: "切换模式", meta: "auto / plan / yolo", kbd: "⌘." },
    { icon: "gear", label: "打开设置", meta: "主题 · 语言 · MCP 与技能", kbd: "⌘," },
  ],

  // ---- History page ----
  history: [
    { label: "今天", items: [
      { id: "h1", title: "重新设计桌面端 UI", scope: "desktop/frontend", time: "进行中", msgs: 12, cost: "¥0.00", tone: "accent", status: "running" },
      { id: "h2", title: "refine mac sidebar 交互", scope: "desktop/frontend", time: "14:20", msgs: 28, cost: "¥3.20", tone: "blue", status: "done" },
      { id: "h3", title: "bot channel settings UI", scope: "internal/bot", time: "09:05", msgs: 17, cost: "¥1.86", tone: "green", status: "done" },
    ] },
    { label: "昨天", items: [
      { id: "h4", title: "review provider cache stability", scope: "internal/provider", time: "21:14", msgs: 41, cost: "¥6.04", tone: "blue", status: "done" },
      { id: "h5", title: "fix workspace preview overflow", scope: "desktop/frontend", time: "16:38", msgs: 9, cost: "¥0.92", tone: "muted", status: "done" },
    ] },
    { label: "本周更早", items: [
      { id: "h6", title: "summarize MCP transport changes", scope: "internal/mcp", time: "6月7日", msgs: 23, cost: "¥2.71", tone: "muted", status: "done" },
      { id: "h7", title: "环境 uv 与 Python 升级", scope: "scripts", time: "6月5日", msgs: 14, cost: "¥1.40", tone: "muted", status: "done" },
    ] },
  ],

  // ---- Memory page ----
  memoryFiles: [
    { name: "REASONIX.md", scope: "项目共享 · 进入系统提示词前缀", tag: "已提交", icon: "book" },
    { name: "REASONIX.local.md", scope: "个人 · git 忽略", tag: "本地", icon: "fileText" },
    { name: "~/.config/reasonix/REASONIX.md", scope: "用户级 · 所有项目", tag: "全局", icon: "gear" },
  ],
  memories: [
    { type: "user", label: "用户", items: [
      { title: "资深工程师 · 偏好简体中文", desc: "界面文案与解释优先简体中文;熟悉 Go 内核与 React 前端。" },
    ] },
    { type: "feedback", label: "反馈", items: [
      { title: "缓存前缀必须字节稳定", desc: "系统提示词前缀不可在会话中途变更,否则 DeepSeek 前缀缓存失效。" },
      { title: "行为下沉到 Controller", desc: "TUI / serve / Wails 三端共享 control.Controller,新行为加在控制器而非单个前端。" },
    ] },
    { type: "project", label: "项目", items: [
      { title: "桌面端 UI 重设计 · 4 个主题方向", desc: "Slate / Carbon / Nocturne / Amber,接入设置 → 主题列表。" },
    ] },
    { type: "reference", label: "参考", items: [
      { title: "缓存与成本看底部状态栏", desc: "上下文占用、缓存命中、本次成本、余额都在 StatusBar 实时显示。" },
    ] },
  ],
  skills: [
    { name: "baoyu-design", desc: "设计原型生成", on: true },
    { name: "code-review", desc: "改动审查与修复", on: true },
    { name: "deep-research", desc: "多源研究报告", on: false },
  ],

  // ---- Trash page ----
  trash: [
    { id: "d1", title: "试验性命令面板布局", scope: "desktop/frontend", deleted: "2 天前", msgs: 6 },
    { id: "d2", title: "旧版 TabBar 动效实验", scope: "desktop/frontend", deleted: "5 天前", msgs: 11 },
    { id: "d3", title: "provider 重试草稿", scope: "internal/provider", deleted: "6月3日", msgs: 4 },
  ],

  // ---- Settings page ----
  settings: {
    general: [
      { label: "界面语言", value: "简体中文", icon: "book" },
      { label: "启动时", value: "恢复上次会话", icon: "history" },
      { label: "发送快捷键", value: "↵ 发送 · ⇧↵ 换行", icon: "enter" },
    ],
    model: [
      { label: "默认模型", value: "DeepSeek-R1", icon: "brain" },
      { label: "推理强度", value: "effort auto", icon: "cpu" },
      { label: "上下文压缩", value: "开启 · 80% 阈值", icon: "activity" },
    ],
  },

  // The scripted assistant turn that streams on load / replay.
  script: {
    user: "重新设计 Reasonix 的桌面端 UI、动画和交互逻辑。先给我四个方向对比。",
    reasoning:
      "先界定边界：这一轮只动 desktop/frontend 的体验层，不碰 provider 请求序列化、系统提示词前缀和 tool schema —— 缓存前缀必须保持字节稳定。\n再定信息架构：左侧会话与项目入口、中间任务对话、右侧工作区上下文；右侧 dock 在打开文件时分裂为「文件树 + 预览」双栏。\n最后在现有暗色基础上演进出四种气质：Slate 冷调原生、Carbon 暖调深邃、Nocturne 柔和夜色、Amber 暖阳琥珀，布局与交互一致，便于并排对比。",
    reasoningMs: 4200,
    tools: [
      {
        id: "tl-read",
        name: "read",
        zh: "读取",
        target: "desktop/frontend/src/App.tsx",
        runMs: 1100,
        body: "解析三栏骨架：Sidebar / Chat / Dock。\n确认 Composer 的模式位（auto·plan·yolo）与模型/算力切换器需保留。",
      },
      {
        id: "tl-inspect",
        name: "inspect",
        zh: "检查",
        target: "styles.css · 设计 token",
        runMs: 1300,
        body: "提取现有 token：--accent、--radius、z-index 层级。\n建立 data-direction × data-theme 的四组合主题表。",
      },
      {
        id: "tl-edit",
        name: "edit",
        zh: "编辑",
        target: "components/Composer.tsx",
        runMs: 1500,
        diff: [
          { type: "ctx", s: "   <div className=\"composer\">" },
          { type: "del", s: "-    <ModeBar inline />" },
          { type: "add", s: "+    <ModeBar segmented animated />" },
          { type: "add", s: "+    <EffortSwitcher spring />" },
          { type: "ctx", s: "     <textarea … />" },
        ],
        body: "把模式位换成带滑动指示的分段控件，输入框聚焦时柔性放大。",
      },
    ],
    answer:
      "四个方向都在现有暗色工作台基础上演进，用顶部切换器即可即时对比，⌘K 打开命令面板，点右侧文件树里的文件看 dock 分裂双栏：\n\n· **Slate 精炼** —— 冷调近黑配品牌蓝，克制专业的原生工作台，层级与发丝边框更利落。\n· **Carbon 深邃** —— 暖炭黑配青绿强调，质感更厚、对比更足，适合长时间专注。\n· **Nocturne 柔和** —— 柔紫夜色配更大圆角留白，弹性呼吸式动效，偏平静阅读。\n· **Amber 琥珀** —— 浅色暖调配清爽橙色与白字按钮，干净明亮、亲和力强。\n\n四者布局与交互一致：左右侧栏可伸缩、思考流式展开、工具卡片运行→完成、dock 打开文件分裂双栏。每个方向都含明暗主题。",
  },

  status: {
    model: "DeepSeek-R1",
    context: "33%",
    cacheNow: "99.12%",
    cacheAvg: "91.04%",
    cost: "¥0.0000",
    jobs: 1,
    balance: "¥128.50",
  },
};
