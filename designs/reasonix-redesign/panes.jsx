/* Presentational components. State flows in via props; callbacks flow out.
   Each <script type="text/babel"> has its own scope, so shared pieces are
   exported to window at the end of the file. */
const { useState, useEffect, useLayoutEffect, useRef } = React;

/* ---- generic sliding segmented control (used by switcher + composer) ---- */
function Segmented({ items, value, onChange, className = "", dep }) {
  const ref = useRef(null);
  const [thumb, setThumb] = useState({ left: 0, width: 0 });
  const measure = () => {
    const el = ref.current?.querySelector('[data-on="true"]');
    if (el) setThumb({ left: el.offsetLeft, width: el.offsetWidth });
  };
  useLayoutEffect(() => {
    measure();
    const t = setTimeout(measure, 60);
    return () => clearTimeout(t);
  }, [value, dep]);
  useEffect(() => {
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, []);
  return (
    <div className={className} ref={ref}>
      <span className={className + "__thumb"} style={{ transform: `translateX(${thumb.left}px)`, width: thumb.width }}></span>
      {items.map((it) => (
        <button
          key={it.value}
          type="button"
          data-on={value === it.value}
          className={`${it.cls || ""} ${value === it.value ? "is-active" : ""}`}
          onClick={() => onChange(it.value)}
        >
          {it.icon && <Icon name={it.icon} />}
          <span>{it.label}</span>
          {it.em && <em>{it.em}</em>}
        </button>
      ))}
    </div>
  );
}

/* ---- floating direction + theme switcher ---- */
function Switcher({ directions, direction, onDirection, theme, onTheme, onReplay }) {
  const items = directions.map((d) => ({ value: d.id, label: `${d.name} ${d.zh}`, em: d.note, cls: "seg__opt" }));
  return (
    <div className="switcher" data-screen-label="Direction Switcher">
      <span className="switcher__hint">设计方向</span>
      <Segmented className="seg" items={items} value={direction} onChange={onDirection} dep={direction} />
      <button className="switcher__theme" type="button" onClick={onTheme} title="切换明暗">
        <Icon name={theme === "dark" ? "sun" : "moon"} size={16} />
      </button>
      {onReplay && (
        <button className="switcher__theme" type="button" onClick={onReplay} title="重播演示">
          <Icon name="activity" size={16} />
        </button>
      )}
    </div>
  );
}

/* ---- window tab bar ---- */
function TabBar({ tabs, activeTab, onTab, sidebarRail, onToggleSidebar, dockOpen, onToggleDock, onOpenPalette }) {
  return (
    <header className="tabbar" data-screen-label="Tab Bar">
      <div className="traffic" aria-hidden="true"><span></span><span></span><span></span></div>
      <button className={`icon-btn ${!sidebarRail ? "is-on" : ""}`} type="button" onClick={onToggleSidebar} title="侧栏">
        <Icon name="panelLeft" size={17} />
      </button>
      <div className="tabbar__tabs">
        {tabs.map((tab) => (
          <button key={tab.id} type="button" className={`tab ${activeTab === tab.id ? "is-active" : ""}`} onClick={() => onTab(tab.id)}>
            <span className={`tab__dot ${tab.dot}`}></span>
            <span className="tab__title">{tab.title}</span>
            {tab.mode === "plan" && <span className="tab__mode">plan</span>}
            <span className="tab__close" onClick={(e) => e.stopPropagation()}><Icon name="x" size={12} /></span>
          </button>
        ))}
        <button className="icon-btn tab-add" type="button" title="新建标签"><Icon name="plus" size={16} /></button>
      </div>
      <div className="tabbar__spacer"></div>
      <button className="cmd-trigger" type="button" onClick={onOpenPalette}>
        <Icon name="search" size={15} />
        <span>搜索 · 命令 · 打开文件</span>
        <kbd>⌘K</kbd>
      </button>
      <div className="tabbar__tools">
        <button className={`icon-btn ${dockOpen ? "is-on" : ""}`} type="button" onClick={onToggleDock} title="工作区">
          <Icon name="panelRight" size={17} />
        </button>
      </div>
    </header>
  );
}

/* ---- left sidebar (rail / expanded) ---- */
function Sidebar({ rail, projects, openProjects, onToggleProject, activeSession, onSession, onNew, onOpenPalette, view, onNav }) {
  return (
    <aside className={`sidebar ${rail ? "is-rail" : ""}`} data-screen-label="Sidebar">
      <div className="sidebar__pad">
        <div className="brand">
          <span className="brand__mark"><img src="./logo-symbol.svg" alt="" /></span>
          <span className="brand__text"><strong>Reasonix</strong><span>DeepSeek-R1</span></span>
        </div>
        <button className="btn-new" type="button" onClick={onNew}>
          <Icon name="pen" size={15} /><span className="lbl">新建会话</span>
        </button>
        <button className="side-search" type="button" onClick={onOpenPalette}>
          <Icon name="search" size={15} /><span className="lbl">搜索项目或会话</span><kbd className="lbl">⌘K</kbd>
        </button>
        <div className="side-section"><span>项目工作区</span><button type="button">全部</button></div>
        <div className="side-scroll">
          {projects.map((p) => (
            <div key={p.id} className={`proj ${openProjects[p.id] ? "is-open" : ""}`}>
              <button className="proj__row" type="button" onClick={() => onToggleProject(p.id)}>
                <Icon name="chevronRight" size={13} className="proj__chev" />
                <span className={`proj__dot t-${p.tone}`}></span>
                <span>{p.name}</span>
                <span className="proj__add"><Icon name="plus" size={13} /></span>
              </button>
              <div className="proj__sessions">
                <div>
                  {p.sessions.map((s) => (
                    <button key={s.id} type="button" className={`sess ${activeSession === s.id ? "is-active" : ""}`} onClick={() => onSession(s.id)}>
                      <span className={`sess__mark t-${s.tone}`}></span>
                      <span className="sess__body">
                        <strong>{s.title}</strong>
                        <small className="sess__meta">{s.meta}</small>
                      </span>
                      {s.unread > 0 && <span className="sess__count">{s.unread}</span>}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          ))}
        </div>
        <nav className="sidebar__nav">
          <button className={`nav-item ${view === "history" ? "is-active" : ""}`} type="button" onClick={() => onNav("history")}><Icon name="history" size={15} /><span className="lbl">历史</span></button>
          <button className={`nav-item ${view === "memory" ? "is-active" : ""}`} type="button" onClick={() => onNav("memory")}><Icon name="brain" size={15} /><span className="lbl">记忆与技能</span></button>
          <button className={`nav-item ${view === "trash" ? "is-active" : ""}`} type="button" onClick={() => onNav("trash")}><Icon name="trash" size={15} /><span className="lbl">回收站</span></button>
          <button className={`nav-item ${view === "settings" ? "is-active" : ""}`} type="button" onClick={() => onNav("settings")}><Icon name="gear" size={15} /><span className="lbl">设置</span></button>
        </nav>
      </div>
    </aside>
  );
}

/* ---- topic bar ---- */
function TopicBar({ changesCount, onOpenChanges, onOpenPalette }) {
  return (
    <div className="topicbar" data-screen-label="Topic Bar">
      <span className="scope"><Icon name="git" size={13} />DeepSeek-Reasonix</span>
      <div className="topicbar__title">
        <h1>重新设计桌面端 UI</h1>
        <small>sivancola_20260608_update_ui</small>
        <button className="icon-btn edit" type="button"><Icon name="pen" size={13} /></button>
      </div>
      <div className="topicbar__spacer"></div>
      <div className="topicbar__actions">
        <button className="chip" type="button" onClick={onOpenChanges}><Icon name="gitCompare" size={14} />{changesCount} 改动</button>
        <button className="chip is-accent" type="button" onClick={onOpenPalette}><Icon name="command" size={14} />命令</button>
      </div>
    </div>
  );
}

/* ---- update notice ---- */
function Notice({ onClose }) {
  return (
    <div className="notice" data-screen-label="Update Notice">
      <div className="notice__body"><strong>发现新版本 v1.1.0</strong><span>更新将在当前任务结束后安装</span></div>
      <div className="notice__spacer"></div>
      <button className="btn-primary" type="button">立即更新</button>
      <button className="btn-ghost" type="button" onClick={onClose}>稍后</button>
    </div>
  );
}

/* ---- empty welcome state ---- */
function EmptyState({ prompts, onPrompt }) {
  return (
    <div className="empty" data-screen-label="Empty State">
      <span className="empty__logo"><img src="./logo.svg" alt="Reasonix" /></span>
      <h2>一个编码智能体</h2>
      <p>描述任务，或随便问点什么。</p>
      <div className="empty__hints">
        <span><kbd>/</kbd>命令</span><span><kbd>@</kbd>引用文件</span><span><kbd>↵</kbd>发送</span>
      </div>
      <div className="empty__grid">
        {prompts.map((p) => (
          <button key={p.text} className="suggest" type="button" onClick={() => onPrompt(p.text)}>
            <Icon name={p.icon} size={16} /><span>{p.text}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

/* ---- thinking / reasoning card ---- */
function ThinkCard({ text, running, durationMs }) {
  const [open, setOpen] = useState(true);
  useEffect(() => { if (!running) setOpen(false); }, [running]);
  return (
    <div className={`think ${open ? "is-open" : ""} ${running ? "is-running" : ""}`}>
      <button className="think__head" type="button" onClick={() => setOpen(!open)}>
        <span className="think__spark"><Icon name="sparkles" size={11} /></span>
        <span className="think__label">{running ? "正在思考" : "思考"}</span>
        <span className="think__time">{running ? "…" : `${(durationMs / 1000).toFixed(1)}s`}</span>
        <Icon name="chevronDown" size={14} className="think__chev" />
      </button>
      <div className="think__wrap"><div><div className="think__body">{text}{running && <span className="answer"><span className="cursor"></span></span>}</div></div></div>
    </div>
  );
}

/* ---- tool execution card ---- */
function ToolCard({ tool, status }) {
  const running = status === "running";
  const [open, setOpen] = useState(false);
  useEffect(() => { if (running) setOpen(true); else setOpen(false); }, [running]);
  return (
    <div className={`tool ${open ? "is-open" : ""} ${running ? "is-running" : ""}`}>
      <button className="tool__head" type="button" onClick={() => setOpen(!open)}>
        <span className="tool__status">
          {running ? <span className="tool__ring"></span> : <span className="tool__ok"><Icon name="check" size={16} /></span>}
        </span>
        <span className="tool__name">{tool.name}</span>
        <span className="tool__zh">{tool.zh}</span>
        <span className="tool__target">{tool.target}</span>
        <span className="tool__state">{running ? "运行中" : <span className="done">完成</span>}<Icon name="chevronDown" size={14} className="tool__chev" /></span>
      </button>
      <div className="tool__wrap"><div>
        <div className="tool__body">
          {tool.body}
          {tool.diff && <DiffCard target={tool.target} diff={tool.diff} reveal={!running} />}
        </div>
      </div></div>
    </div>
  );
}

function DiffCard({ target, diff, reveal }) {
  const adds = diff.filter((d) => d.type === "add").length;
  const dels = diff.filter((d) => d.type === "del").length;
  return (
    <div className="diff">
      <div className="diff__file">
        <Icon name="fileText" size={13} />
        <span>{target}</span>
        <span style={{ marginLeft: "auto" }}><span className="add">+{adds}</span> <span className="del">−{dels}</span></span>
      </div>
      {diff.map((row, i) => (
        <div key={i} className={`diff__row ${row.type}`} style={reveal ? { animationDelay: `${i * 55}ms` } : { animation: "none" }}>
          <span className="sign">{row.type === "add" ? "+" : row.type === "del" ? "−" : " "}</span>
          <span>{row.s.replace(/^[+\-]/, "")}</span>
        </div>
      ))}
    </div>
  );
}

/* ---- a full assistant / user message ---- */
function Message({ msg }) {
  if (msg.role === "user") {
    return <article className="msg user"><div className="bubble" lang="zh">{msg.text}</div></article>;
  }
  return (
    <article className="msg assistant">
      {msg.reasoning != null && <ThinkCard text={msg.reasoning} running={msg.reasoningRunning} durationMs={msg.reasoningMs} />}
      {(msg.tools || []).map((t) => <ToolCard key={t.id} tool={t} status={t.status} />)}
      {msg.answer != null && (
        <div className="answer" lang="zh">
          {renderRich(msg.answer)}
          {msg.answerRunning && <span className="cursor"></span>}
        </div>
      )}
    </article>
  );
}

/* tiny markdown-ish renderer: **bold**, paragraphs */
function renderRich(text) {
  return text.split("\n\n").map((para, pi) => (
    <p key={pi}>
      {para.split(/(\*\*[^*]+\*\*)/g).map((chunk, ci) =>
        chunk.startsWith("**") ? <strong key={ci}>{chunk.slice(2, -2)}</strong> : <span key={ci}>{chunk}</span>
      )}
    </p>
  ));
}

/* ---- composer ---- */
function Composer({ value, onChange, onSend, mode, onMode, running, onStop, model }) {
  const [focus, setFocus] = useState(false);
  const taRef = useRef(null);
  const grow = (el) => { if (!el) return; el.style.height = "auto"; el.style.height = Math.min(el.scrollHeight, 160) + "px"; };
  useEffect(() => { grow(taRef.current); }, [value]);
  const modeItems = [
    { value: "auto", label: "auto", icon: "bolt", cls: "mode m-auto" },
    { value: "plan", label: "plan", icon: "list", cls: "mode m-plan" },
    { value: "yolo", label: "yolo", icon: "warn", cls: "mode m-yolo" },
  ];
  return (
    <div className="composer" data-screen-label="Composer">
      <div className={`composer__card ${focus ? "is-focus" : ""}`}>
        <div className="composer__field">
          <textarea
            ref={taRef}
            className="composer__input"
            value={value}
            placeholder="给 Reasonix 发消息…（/ 命令 · @ 文件）"
            rows={1}
            onFocus={() => setFocus(true)}
            onBlur={() => setFocus(false)}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); onSend(); } }}
          ></textarea>
          <button className="send" type="button" onClick={running ? onStop : onSend} disabled={!running && !value.trim()} title={running ? "停止" : "发送"}>
            <Icon name={running ? "stop" : "arrowUp"} size={16} />
          </button>
        </div>
        <div className="composer__meta">
          <Segmented className="modes" items={modeItems} value={mode} onChange={onMode} dep={mode} />
          <button className="meta-chip" type="button"><Icon name="at" size={13} />reasonix</button>
          <button className="meta-chip" type="button"><Icon name="brain" size={13} />{model}<Icon name="chevronDown" size={12} className="caret" /></button>
          <button className="meta-chip" type="button"><Icon name="cpu" size={13} />effort auto<Icon name="chevronDown" size={12} className="caret" /></button>
          <span className="meta-spacer"></span>
          <button className="meta-chip" type="button"><Icon name="terminal" size={13} />shell</button>
        </div>
      </div>
    </div>
  );
}

/* ---- status bar ---- */
function StatusBar({ status, running }) {
  return (
    <div className="statusbar" data-screen-label="Status Bar">
      <span className="stat"><span className={`stat__dot ${running ? "run" : ""}`}></span>{status.model}</span>
      <span className="stat">上下文 <b>{status.context}</b></span>
      <span className="stat">缓存 <b>{status.cacheNow}</b></span>
      <span className="stat">平均 <b>{status.cacheAvg}</b></span>
      <span className="spacer"></span>
      <span className="stat">本次 <b>{status.cost}</b></span>
      <span className="stat">任务 <b>{status.jobs}</b></span>
      <span className="stat balance">余额 <b>{status.balance}</b></span>
    </div>
  );
}

/* ---- right workspace dock (single → split) ---- */
function Dock({ open, split, tab, onTab, files, activeFileId, onOpenFile, onCloseFile, fileContent, changes, context }) {
  const tabs = [
    { id: "files", label: "文件", icon: "folder" },
    { id: "changes", label: "改动", icon: "gitCompare" },
    { id: "context", label: "上下文", icon: "activity" },
  ];
  return (
    <aside className={`dock ${open ? "is-open" : ""} ${split ? "is-split" : ""}`} data-screen-label="Workspace Dock">
      <div className="dock__inner">
        <div className="dock__main">
          <div className="dock__head">
            <div><h3>工作区</h3></div>
            <div className="spacer"></div>
            <small>desktop/frontend</small>
          </div>
          <div className="dock__tabs">
            {tabs.map((t) => (
              <button key={t.id} type="button" className={`dock__tab ${tab === t.id ? "is-active" : ""}`} onClick={() => onTab(t.id)}>
                <Icon name={t.icon} size={14} />{t.label}
              </button>
            ))}
          </div>
          <div className="dock__divider"></div>
          <div className="dock__scroll">
            {tab === "files" && (
              <>
                <label className="filter"><Icon name="search" size={14} /><input placeholder="筛选文件…" /></label>
                {files.map((f) => (
                  <button
                    key={f.id}
                    type="button"
                    className={`row ${f.type} ${activeFileId === f.id ? "is-active" : ""}`}
                    style={{ paddingLeft: 9 + f.depth * 14 }}
                    onClick={() => (f.type === "file" ? onOpenFile(f.id) : null)}
                  >
                    <Icon name={f.type === "folder" ? (f.open ? "folderOpen" : "folder") : "fileText"} size={15} />
                    <span className="name">{f.name}</span>
                    {f.badge && <em className={`badge ${f.badge === "A" ? "added" : ""}`}>{f.badge}</em>}
                  </button>
                ))}
              </>
            )}
            {tab === "changes" && changes.map((c) => (
              <button key={c.file} type="button" className="change">
                <span className={`change__dot ${c.status}`}></span>
                <span className="change__body">
                  <strong>{c.file}</strong>
                  <span className="change__lines"><span className="add">+{c.add}</span> <span className="del">−{c.del}</span></span>
                </span>
              </button>
            ))}
            {tab === "context" && (
              <div className="metrics">
                {context.map((m) => (
                  <div key={m.label} className="metric"><div className={`v ${m.tone}`}>{m.value}</div><div className="l">{m.label}</div></div>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="dock__viewer">
          {fileContent && (
            <>
              <div className="viewer__head">
                <Icon name="fileText" size={15} />
                <div style={{ minWidth: 0, flex: "1 1 auto", overflow: "hidden" }}>
                  <div className="viewer__name">{fileContent.name}</div>
                  <div className="viewer__path">{fileContent.path}</div>
                </div>
                <div className="spacer"></div>
                <button className="icon-btn" type="button" title="复制"><Icon name="copy" size={15} /></button>
                <button className="icon-btn" type="button" title="关闭" onClick={onCloseFile}><Icon name="x" size={15} /></button>
              </div>
              <div className="viewer__code">
                {fileContent.lines.map((ln, i) => (
                  <div className="cline" key={i}>
                    <span className="ln">{ln.t === "blank" ? "" : i + 1}</span>
                    <span className={`tk-${ln.t}`}>{ln.s || " "}</span>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </aside>
  );
}

/* ---- command palette ---- */
function CommandPalette({ open, onClose, items }) {
  const [active, setActive] = useState(0);
  const [q, setQ] = useState("");
  useEffect(() => { if (open) { setActive(0); setQ(""); } }, [open]);
  if (!open) return null;
  const filtered = items.filter((it) => it.label.includes(q) || it.meta.includes(q));
  return (
    <div className="palette__backdrop" onMouseDown={onClose}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette__search">
          <Icon name="search" size={18} />
          <input autoFocus placeholder="搜索命令、文件、会话…" value={q} onChange={(e) => { setQ(e.target.value); setActive(0); }} />
          <kbd>esc</kbd>
        </div>
        <div className="palette__list">
          <div className="pal-group">建议操作</div>
          {filtered.map((it, i) => (
            <button key={it.label} type="button" className={`pal-item ${i === active ? "is-active" : ""}`} onMouseEnter={() => setActive(i)} onClick={onClose}>
              <span className="pal-item__icon"><Icon name={it.icon} size={16} /></span>
              <span className="pal-item__body"><strong>{it.label}</strong><small>{it.meta}</small></span>
              {it.kbd && <kbd>{it.kbd}</kbd>}
            </button>
          ))}
          {filtered.length === 0 && <div className="pal-group" style={{ textAlign: "center", padding: 24 }}>没有匹配项</div>}
        </div>
      </div>
    </div>
  );
}

Object.assign(window, {
  Segmented, Switcher, TabBar, Sidebar, TopicBar, Notice, EmptyState,
  ThinkCard, ToolCard, DiffCard, Message, Composer, StatusBar, Dock, CommandPalette,
});
