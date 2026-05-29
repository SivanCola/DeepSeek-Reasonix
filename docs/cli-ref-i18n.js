/* CLI-reference page translations + scrollspy. Layered on top of i18n.js. */

(function () {
  "use strict";

  var R = window.Reasonix;
  if (!R) return;

  var en = {
    "cli.badge": "Shell · Slash Commands · Keyboard · Mouse",
    "cli.title.line1": "CLI Reference",
    "cli.title.line2": "every command, key, and flag",
    "cli.sub":
      "Every shell subcommand, every TUI slash command, every keybinding. The in-app <code>/help</code> and <code>/keys</code> panels are the live source of truth — this page is the printable companion.",

    "cli.toc.title": "On this page",
    "cli.toc.shell": "Shell subcommands",
    "cli.toc.slash": "Slash commands",
    "cli.toc.keyboard": "Keyboard",
    "cli.toc.mouse": "Mouse",
    "cli.toc.copypaste": "Copy / paste",

    "th.cmd": "Command",
    "th.what": "What it does",
    "th.key": "Key",
    "th.action": "Action",

    "sh.title": "Shell subcommands",
    "sh.body":
      "Run <code>reasonix --help</code> (or any subcommand with <code>--help</code>) for the full flag list. Headline subcommands:",
    "sh.flags.title": "Notable runtime flags (chat / code)",

    "sl.title": "Slash commands",
    "sl.body":
      "Type <code>/</code> mid-chat to open the picker. Aliases shown in parentheses. Code-mode-only commands marked <strong>(code)</strong>.",
    "sl.h.chatops": "Chat ops",
    "sl.h.setup": "Setup",
    "sl.h.info": "Info",
    "sl.h.extend": "Extend",
    "sl.h.session": "Session",
    "sl.h.code": "Code mode",
    "sl.h.jobs": "Jobs (code mode)",
    "sl.h.advanced": "Advanced",

    "kb.title": "Keyboard",
    "kb.h.editgate": "Edit-gate (code mode)",

    "ms.title": "Mouse",
    "ms.body":
      "In auto/app scroll mode, Reasonix captures mouse-wheel reports so chat history can scroll inside terminals whose native scrollback does not move TUI content. Pass <code>--no-mouse</code> to opt out entirely.",

    "cp.title": "Copy / paste",
    "cp.body":
      "The default path is <strong>terminal-native</strong>. Drag to select, then use your terminal's normal copy keys:",
    "cp.h.drag": "When drag-select doesn't work",
    "cp.body.drag":
      "In SSH / mosh / tmux, the alt-screen buffer prevents the terminal from extending the selection past the visible viewport — there is no scrollback above the alt-screen to drag into. Two fixes:",
    "cp.fix1":
      "<strong><code>/copy</code></strong> — copy the latest assistant response through OSC 52. Use <code>/copy all</code> for the full chat or <code>/copy N</code> for the last N serialized chat items. Oversized content falls back to a temp file path.",
    "cp.fix2":
      "<strong><code>--no-alt-screen</code></strong> — render to shell scrollback instead. Drag-select then works terminal-natively (the chat content is real lines in the scrollback above your cursor). Trade-off: redraw can ghost on resize.",
    "cp.h.copymode": "<code>/copy</code>",
    "cp.body.osc": "<code>/copy</code> uses OSC 52 first; large content falls back to a temp file.",
  };

  var zh = {
    "cli.badge": "Shell · 斜杠命令 · 快捷键 · 鼠标",
    "cli.title.line1": "CLI 参考",
    "cli.title.line2": "所有命令、快捷键和 flag",
    "cli.sub":
      "所有 shell 子命令、所有 TUI 斜杠命令、所有快捷键一览。应用内 <code>/help</code> 与 <code>/keys</code> 面板是权威来源——本页是可检索的离线副本。",

    "cli.toc.title": "本页目录",
    "cli.toc.shell": "Shell 子命令",
    "cli.toc.slash": "斜杠命令",
    "cli.toc.keyboard": "快捷键",
    "cli.toc.mouse": "鼠标",
    "cli.toc.copypaste": "复制 / 粘贴",

    "th.cmd": "命令",
    "th.what": "作用",
    "th.key": "按键",
    "th.action": "效果",

    "sh.title": "Shell 子命令",
    "sh.body":
      "任意子命令加 <code>--help</code> 可查完整 flag 列表。主要子命令：",
    "sh.flags.title": "常用运行时 flag（chat / code）",

    "sl.title": "斜杠命令",
    "sl.body":
      "输入 <code>/</code> 在聊天中打开选择器。括号内为别名。仅 code 模式可用的命令标注 <strong>（code）</strong>。",
    "sl.h.chatops": "聊天操作",
    "sl.h.setup": "设置",
    "sl.h.info": "信息",
    "sl.h.extend": "扩展",
    "sl.h.session": "会话",
    "sl.h.code": "Code 模式",
    "sl.h.jobs": "后台任务（code 模式）",
    "sl.h.advanced": "高级",

    "kb.title": "快捷键",
    "kb.h.editgate": "编辑门控（code 模式）",

    "ms.title": "鼠标",
    "ms.body":
      "在 auto/app 滚动模式下，Reasonix 会捕获鼠标滚轮事件，让原生 scrollback 无法滚动 TUI 内容的终端也能滚聊天。加 <code>--no-mouse</code> 可完全关闭。",

    "cp.title": "复制 / 粘贴",
    "cp.body":
      "默认走<strong>终端原生</strong>路径。拖拽选中文本，再用终端本身的复制快捷键：",
    "cp.h.drag": "拖拽选择不生效时",
    "cp.body.drag":
      "SSH / mosh / tmux 下，alt-screen 缓冲区会阻止终端把选区延伸到可视视口以外——alt-screen 上方根本没有 scrollback 可拖入。两种解决方式：",
    "cp.fix1":
      "<strong><code>/copy</code></strong> — 通过 OSC 52 复制最近一条 assistant 回复。用 <code>/copy all</code> 复制完整聊天，或 <code>/copy N</code> 复制最近 N 个序列化聊天项；内容过大时退到临时文件路径。",
    "cp.fix2":
      "<strong><code>--no-alt-screen</code></strong> — 改为渲染到 shell scrollback。拖拽选择恢复终端原生（聊天内容就是光标上方的真实行）。代价：窗口大小改变时可能出现重绘残影。",
    "cp.h.copymode": "<code>/copy</code>",
    "cp.body.osc": "<code>/copy</code> 优先使用 OSC 52；大内容会退到临时文件。",
  };

  var DICT = { en: en, zh: zh };

  function applyCli(lang) {
    var dict = DICT[lang] || DICT.en;
    document.querySelectorAll("[data-i18n]").forEach(function (el) {
      var key = el.getAttribute("data-i18n");
      if (dict[key] !== undefined) el.innerHTML = dict[key];
    });
  }

  applyCli(R.lang());
  R.onLangChange(applyCli);

  var sections = Array.prototype.slice.call(
    document.querySelectorAll(".guide-body section[id]"),
  );
  var tocLinks = Array.prototype.slice.call(
    document.querySelectorAll(".guide-toc a"),
  );
  if (sections.length && tocLinks.length && "IntersectionObserver" in window) {
    var byId = {};
    tocLinks.forEach(function (a) {
      var href = a.getAttribute("href") || "";
      var id = href.replace(/^#/, "");
      if (id) byId[id] = a;
    });
    var io = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (e) {
          var link = byId[e.target.id];
          if (!link) return;
          if (e.isIntersecting) {
            tocLinks.forEach(function (l) {
              l.classList.remove("is-active");
            });
            link.classList.add("is-active");
          }
        });
      },
      { rootMargin: "-30% 0px -60% 0px", threshold: 0 },
    );
    sections.forEach(function (s) {
      io.observe(s);
    });
  }
})();
