/* Modal dialogs: Settings (left section-nav) + independent History / Trash.
   All token-based, so each adapts across the 4 directions x light/dark. */
const { useState: usePageState } = React;

function ModalShell({ open, onClose, size, label, children }) {
  if (!open) return null;
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className={`modal ${size || ""}`} onMouseDown={(e) => e.stopPropagation()} data-screen-label={label}>
        {children}
      </div>
    </div>
  );
}

/* ---- 3-swatch theme card (renders swatches in its OWN direction) ---- */
function ThemeCard({ d, theme, active, onPick }) {
  return (
    <button type="button" className={`tcard ${active ? "is-selected" : ""}`} onClick={onPick}>
      <div className="tcard__head">
        <strong>{d.name} <span>{d.zh}</span></strong>
        <span className="tcard__tag">{d.note}</span>
      </div>
      <div className="tcard__swatches" data-direction={d.id} data-theme={theme}>
        <span style={{ background: "var(--bg)" }}></span>
        <span style={{ background: "var(--surface-3)" }}></span>
        <span style={{ background: "var(--accent)" }}></span>
      </div>
      <p className="tcard__desc">{d.desc}</p>
    </button>
  );
}

function OptRow({ title, desc, children, span }) {
  return (
    <div className={`opt-row ${span ? "is-span" : ""}`}>
      <div className="opt-row__label"><strong>{title}</strong>{desc && <span>{desc}</span>}</div>
      <div className="opt-row__control">{children}</div>
    </div>
  );
}

function GeneralSection({ directions, direction, onDirection, theme, onTheme }) {
  const [fontSize, setFontSize] = usePageState("m");
  const [font, setFont] = usePageState("sans");
  const [lang, setLang] = usePageState("zh");
  const seg = (items, value, onChange) => <Segmented className="seg" items={items.map((i) => ({ ...i, cls: "seg__opt" }))} value={value} onChange={onChange} dep={value} />;
  return (
    <>
      <div className="opt-section-title">外观</div>
      <OptRow title="主题" desc="仅本机,不与 TUI 同步。">
        {seg([{ value: "dark", label: "深色" }, { value: "light", label: "浅色" }], theme, onTheme)}
      </OptRow>
      <OptRow title="风格" desc="为当前浅色或深色模式选择具体视觉配色。" span>
        <div className="theme-row">
          {directions.map((d) => (
            <ThemeCard key={d.id} d={d} theme={theme} active={direction === d.id} onPick={() => onDirection(d.id)} />
          ))}
        </div>
      </OptRow>
      <OptRow title="字体大小" desc="整体缩放,高分屏可选大档。">
        {seg([{ value: "s", label: "小" }, { value: "m", label: "中" }, { value: "l", label: "大" }], fontSize, setFontSize)}
      </OptRow>
      <OptRow title="字体" desc="正文字体;代码块始终用等宽。">
        {seg([{ value: "sans", label: "无衬线" }, { value: "system", label: "系统" }, { value: "serif", label: "衬线" }], font, setFont)}
      </OptRow>
      <OptRow title="语言" desc="仅影响界面,不影响模型本身。">
        {seg([{ value: "en", label: "English" }, { value: "zh", label: "简体中文" }, { value: "de", label: "Deutsch" }], lang, setLang)}
      </OptRow>
    </>
  );
}

function RowList({ rows }) {
  return rows.map((r) => (
    <div className="set-row" key={r.label}>
      <div className="set-row__main">
        <span className="set-row__icon"><Icon name={r.icon} size={16} /></span>
        <div><strong>{r.label}</strong>{r.sub && <span>{r.sub}</span>}</div>
      </div>
      {r.toggle != null ? (
        <span className={`switch ${r.toggle ? "is-on" : ""}`}><span></span></span>
      ) : (
        <button className="meta-chip" type="button">{r.value}<Icon name="chevronDown" size={12} className="caret" /></button>
      )}
    </div>
  ));
}

function MemorySection({ memoryFiles, memories }) {
  const tone = { user: "blue", feedback: "amber", project: "accent", reference: "green" };
  return (
    <>
      <p className="page-lead">Reasonix 把持久事实写入项目记忆,下次会话载入系统提示词前缀;分层文档按项目 / 个人 / 全局覆盖。</p>
      <div className="opt-section-title">分层文档</div>
      <div className="mem-files">
        {memoryFiles.map((f) => (
          <div className="mem-file" key={f.name}>
            <span className="mem-file__icon"><Icon name={f.icon} size={16} /></span>
            <div className="mem-file__body"><strong>{f.name}</strong><span>{f.scope}</span></div>
            <em className="mem-file__tag">{f.tag}</em>
          </div>
        ))}
      </div>
      <div className="set-block__head between" style={{ marginTop: 20 }}>
        <div className="opt-section-title" style={{ margin: 0 }}>记忆条目</div>
        <button className="meta-chip" type="button"><Icon name="plus" size={13} />添加记忆</button>
      </div>
      {memories.map((g) => (
        <div className="mem-group" key={g.type}>
          <span className={`mem-tag t-${tone[g.type]}`}>{g.label}</span>
          <div className="mem-cards">
            {g.items.map((m) => (
              <div className="mem-card" key={m.title}><strong>{m.title}</strong><p>{m.desc}</p></div>
            ))}
          </div>
        </div>
      ))}
    </>
  );
}

function SettingsModal({ open, onClose, section, setSection, directions, direction, onDirection, theme, onTheme, settings, status, skills, memoryFiles, memories }) {
  const nav = [
    { id: "general", label: "通用", icon: "sun" },
    { id: "model", label: "模型", icon: "brain" },
    { id: "mcp", label: "MCP 服务器", icon: "command" },
    { id: "skills", label: "技能 / Skills", icon: "sparkles" },
    { id: "memory", label: "记忆", icon: "book" },
    { id: "approval", label: "审批规则", icon: "check" },
    { id: "account", label: "账户 & 计费", icon: "cpu" },
    { id: "shortcuts", label: "快捷键", icon: "hash" },
  ];
  const meta = {
    general: { title: "通用", sub: "外观、语言、行为" },
    model: { title: "模型", sub: "默认模型与推理强度" },
    mcp: { title: "MCP 服务器", sub: "已连接的工具与资源" },
    skills: { title: "技能 / Skills", sub: "已安装技能开关" },
    memory: { title: "记忆", sub: "自动记忆与分层文档" },
    approval: { title: "审批规则", sub: "工具调用的放行策略" },
    account: { title: "账户 & 计费", sub: "余额与用量" },
    shortcuts: { title: "快捷键", sub: "键盘操作" },
  }[section];
  return (
    <ModalShell open={open} onClose={onClose} size="lg" label="设置">
      <div className="settings">
        <aside className="settings__nav">
          <div className="settings__title">设置</div>
          {nav.map((n) => (
            <button key={n.id} type="button" className={`snav ${section === n.id ? "is-active" : ""}`} onClick={() => setSection(n.id)}>
              <Icon name={n.icon} size={16} />{n.label}
            </button>
          ))}
        </aside>
        <div className="settings__main">
          <header className="settings__head">
            <div><h2>{meta.title}</h2><span>{meta.sub}</span></div>
            <button className="icon-btn" type="button" onClick={onClose} title="关闭"><Icon name="x" size={18} /></button>
          </header>
          <div className="settings__content">
            {section === "general" && <GeneralSection directions={directions} direction={direction} onDirection={onDirection} theme={theme} onTheme={onTheme} />}
            {section === "model" && <RowList rows={settings.model.map((r) => ({ ...r }))} />}
            {section === "memory" && <MemorySection memoryFiles={memoryFiles} memories={memories} />}
            {section === "skills" && (
              <div className="skill-list">
                {skills.map((s) => (
                  <div className="skill-row" key={s.name}>
                    <span className="skill-row__icon"><Icon name="sparkles" size={15} /></span>
                    <div className="skill-row__body"><strong>{s.name}</strong><span>{s.desc}</span></div>
                    <span className={`switch ${s.on ? "is-on" : ""}`}><span></span></span>
                  </div>
                ))}
              </div>
            )}
            {section === "mcp" && (
              <RowList rows={[
                { icon: "command", label: "context7", sub: "文档检索 · 已连接", toggle: true },
                { icon: "command", label: "chrome-devtools", sub: "浏览器调试 · 已连接", toggle: true },
                { icon: "command", label: "memory", sub: "知识图谱 · 已停用", toggle: false },
              ]} />
            )}
            {section === "approval" && (
              <RowList rows={[
                { icon: "terminal", label: "Shell 命令", sub: "执行前需确认", value: "询问" },
                { icon: "pen", label: "文件写入", sub: "工作区内自动放行", value: "自动" },
                { icon: "git", label: "Git 推送", sub: "始终需确认", value: "询问" },
              ]} />
            )}
            {section === "account" && (
              <>
                <div className="account-card">
                  <div><span className="account-card__l">账户余额</span><div className="account-card__v">{status.balance}</div></div>
                  <button className="btn-primary" type="button">充值</button>
                </div>
                <RowList rows={[
                  { icon: "activity", label: "本次会话", sub: "DeepSeek-R1", value: status.cost },
                  { icon: "cpu", label: "计费方式", sub: "按 token 用量", value: "实时" },
                ]} />
              </>
            )}
            {section === "shortcuts" && (
              <div className="kbd-list">
                {[["新建会话", "⌘ N"], ["命令面板", "⌘ K"], ["切换模式", "⌘ ."], ["打开文件", "⌘ P"], ["查看改动", "⌘ ⇧ G"], ["发送 / 换行", "↵ / ⇧↵"]].map(([k, v]) => (
                  <div className="kbd-row" key={k}><span>{k}</span><kbd>{v}</kbd></div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </ModalShell>
  );
}

function HistoryModal({ open, onClose, history, onOpen }) {
  const [filter, setFilter] = usePageState("all");
  const chips = [{ id: "all", label: "全部" }, { id: "running", label: "进行中" }, { id: "done", label: "已完成" }];
  return (
    <ModalShell open={open} onClose={onClose} size="md" label="历史">
      <header className="modal__head">
        <span className="modal__icon"><Icon name="history" size={17} /></span>
        <div className="modal__titles"><h2>历史</h2><span>所有会话 · 按时间归档</span></div>
        <button className="icon-btn" type="button" onClick={onClose}><Icon name="x" size={18} /></button>
      </header>
      <div className="modal__toolbar">
        <label className="filter wide"><Icon name="search" size={14} /><input placeholder="搜索历史会话…" /></label>
        <div className="chip-row">
          {chips.map((c) => <button key={c.id} type="button" className={`pill ${filter === c.id ? "is-active" : ""}`} onClick={() => setFilter(c.id)}>{c.label}</button>)}
        </div>
      </div>
      <div className="modal__scroll">
        {history.map((g) => {
          const items = g.items.filter((it) => filter === "all" || it.status === filter);
          if (!items.length) return null;
          return (
            <div className="hist-group" key={g.label}>
              <div className="hist-group__label">{g.label}<span>{items.length}</span></div>
              {items.map((it) => (
                <button className="hist-row" type="button" key={it.id} onClick={() => onOpen(it)}>
                  <span className={`hist-row__dot t-${it.tone}`}></span>
                  <div className="hist-row__body"><strong>{it.title}</strong><span>{it.scope} · {it.time}</span></div>
                  <span className="hist-row__meta">{it.msgs} 条 · {it.cost}</span>
                  {it.status === "running" && <span className="badge-run">进行中</span>}
                  <Icon name="chevronRight" size={15} className="hist-row__chev" />
                </button>
              ))}
            </div>
          );
        })}
      </div>
    </ModalShell>
  );
}

function TrashModal({ open, onClose, trash }) {
  return (
    <ModalShell open={open} onClose={onClose} size="md" label="回收站">
      <header className="modal__head">
        <span className="modal__icon"><Icon name="trash" size={17} /></span>
        <div className="modal__titles"><h2>回收站</h2><span>删除的会话 · 30 天后清除</span></div>
        <button className="icon-btn" type="button" onClick={onClose}><Icon name="x" size={18} /></button>
      </header>
      <div className="modal__scroll">
        <div className="trash-note">
          <Icon name="clock" size={15} />
          <span>回收站中的会话将在删除 30 天后自动彻底清除。</span>
          <div className="page__spacer"></div>
          <button className="btn-ghost danger" type="button">清空回收站</button>
        </div>
        {trash.map((t) => (
          <div className="trash-row" key={t.id}>
            <span className="trash-row__icon"><Icon name="fileText" size={16} /></span>
            <div className="trash-row__body"><strong>{t.title}</strong><span>{t.scope} · 删除于 {t.deleted} · {t.msgs} 条消息</span></div>
            <button className="meta-chip" type="button"><Icon name="history" size={13} />恢复</button>
            <button className="icon-btn danger" type="button" title="彻底删除"><Icon name="trash" size={15} /></button>
          </div>
        ))}
      </div>
    </ModalShell>
  );
}

Object.assign(window, { ModalShell, ThemeCard, SettingsModal, HistoryModal, TrashModal });
