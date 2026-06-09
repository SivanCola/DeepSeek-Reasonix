/* Orchestrator: owns all state, the streaming engine, and the
   sidebar/dock motion coordination. Mounts to #root. */
const { useState, useEffect, useRef } = React;
const data = window.reasonixData;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const LS = (k, d) => { try { const v = localStorage.getItem("rdx_" + k); return v == null ? d : JSON.parse(v); } catch (e) { return d; } };
const save = (k, v) => { try { localStorage.setItem("rdx_" + k, JSON.stringify(v)); } catch (e) {} };

function genericContent(file) {
  return {
    name: file ? file.name : "file",
    path: "desktop/frontend/" + (file ? file.name : ""),
    lines: [
      { t: "comment", s: "// " + (file ? file.name : "") },
      { t: "plain", s: "（此文件未包含在原型预览数据中）" },
    ],
  };
}

function buildReply(text) {
  return {
    reasoningMs: 2100,
    reasoning:
      "确认边界：仅前端体验层，保持系统提示词前缀与 tool schema 字节稳定，缓存不失效。\n据此把请求落到最小改动路径。",
    tools: [
      { id: "rp-" + Date.now(), name: "plan", zh: "规划", target: "desktop/frontend", runMs: 1000, body: "拆解任务，选择不触碰内核的实现路径。" },
    ],
    answer: "收到 —— 「" + text + "」。我会在不改动 provider 序列化与缓存前缀的前提下推进这一项，并把结果反映到当前方向上。",
  };
}

function App() {
  const VALID_DIRS = data.directions.map((d) => d.id);
  const [direction, setDirection] = useState(() => {
    const v = LS("dir", "slate");
    return VALID_DIRS.includes(v) ? v : "slate";
  });
  const [theme, setTheme] = useState(() => LS("theme", "dark"));
  const [rail, setRail] = useState(false);
  const railBefore = useRef(false);
  const [dockOpen, setDockOpen] = useState(true);
  const [dockTab, setDockTab] = useState("files");
  const [openFileId, setOpenFileId] = useState(null);
  const [activeTab, setActiveTab] = useState("t1");
  const [activeSession, setActiveSession] = useState("s1");
  const [openProjects, setOpenProjects] = useState({ "p-frontend": true, "p-kernel": true, "p-global": false });
  const [mode, setMode] = useState("plan");
  const [text, setText] = useState("");
  const [palette, setPalette] = useState(false);
  const [modal, setModal] = useState(null);
  const [settingsSection, setSettingsSection] = useState("general");
  const nav = (target) => {
    if (target === "memory") { setSettingsSection("memory"); setModal("settings"); }
    else if (target === "settings") { setSettingsSection("general"); setModal("settings"); }
    else setModal(target);
  };
  const [notice, setNotice] = useState(true);
  const [messages, setMessages] = useState([]);
  const [live, setLive] = useState(null);
  const [running, setRunning] = useState(false);
  const cancel = useRef(0);

  useEffect(() => save("dir", direction), [direction]);
  useEffect(() => save("theme", theme), [theme]);

  /* ---------------- streaming engine ---------------- */
  async function typeField(field, full, speed, token) {
    full = full || "";
    for (let i = 1; i <= full.length; i++) {
      if (cancel.current !== token) return;
      setLive((prev) => (prev ? { ...prev, [field]: full.slice(0, i) } : prev));
      await sleep(speed);
    }
  }

  async function playTurn(script, user) {
    const token = ++cancel.current;
    const alive = () => cancel.current === token;
    setRunning(true);
    const id = "a-" + Date.now();
    if (user) setMessages((m) => [...m, { id: "u-" + Date.now(), role: "user", text: user }]);
    setLive({ id, role: "assistant", reasoning: "", reasoningRunning: true, reasoningMs: script.reasoningMs || 3000, tools: [], answer: null, answerRunning: false });

    await typeField("reasoning", script.reasoning, 19, token);
    if (!alive()) return;
    setLive((prev) => (prev ? { ...prev, reasoningRunning: false } : prev));
    await sleep(200);

    for (let i = 0; i < script.tools.length; i++) {
      if (!alive()) return;
      const t = { ...script.tools[i], status: "running" };
      setLive((prev) => (prev ? { ...prev, tools: [...prev.tools, t] } : prev));
      await sleep(script.tools[i].runMs || 1200);
      if (!alive()) return;
      setLive((prev) => (prev ? { ...prev, tools: prev.tools.map((x, xi) => (xi === i ? { ...x, status: "done" } : x)) } : prev));
      await sleep(240);
    }

    if (!alive()) return;
    setLive((prev) => (prev ? { ...prev, answer: "", answerRunning: true } : prev));
    await typeField("answer", script.answer, 15, token);
    if (!alive()) return;
    setLive((prev) => (prev ? { ...prev, answerRunning: false } : prev));
    await sleep(140);

    if (!alive()) return;
    const finalMsg = {
      id, role: "assistant",
      reasoning: script.reasoning, reasoningRunning: false, reasoningMs: script.reasoningMs || 3000,
      tools: script.tools.map((t) => ({ ...t, status: "done" })),
      answer: script.answer, answerRunning: false,
    };
    setMessages((m) => [...m, finalMsg]);
    setLive(null);
    setRunning(false);
  }

  function playMain() {
    cancel.current++;
    setRunning(false);
    setLive(null);
    setMessages([]);
    setActiveSession("s1");
    setTimeout(() => playTurn(data.script, data.script.user), 40);
  }

  function send(raw) {
    const val = (typeof raw === "string" ? raw : text).trim();
    if (!val || running) return;
    setText("");
    setActiveSession("s1");
    setModal(null);
    playTurn(buildReply(val), val);
  }

  function stop() {
    cancel.current++;
    setRunning(false);
    setLive((prev) => (prev ? { ...prev, reasoningRunning: false, answerRunning: false } : prev));
  }

  /* ---------------- file open / dock split coordination ---------------- */
  function openFile(id) {
    railBefore.current = rail;
    setOpenFileId(id);
    setDockOpen(true);
    setDockTab("files");
    setRail(true);
  }
  function closeFile() {
    setOpenFileId(null);
    setRail(railBefore.current);
  }
  function toggleDock() {
    if (dockOpen) { setDockOpen(false); setOpenFileId(null); }
    else setDockOpen(true);
  }

  /* ---------------- keyboard ---------------- */
  useEffect(() => {
    const onKey = (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPalette((p) => !p); }
      else if (e.key === "Escape") { if (palette) setPalette(false); else if (modal) setModal(null); else if (openFileId) closeFile(); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [palette, openFileId, modal]);

  /* ---------------- autoplay demo on mount ---------------- */
  useEffect(() => {
    const t = setTimeout(() => playTurn(data.script, data.script.user), 520);
    return () => clearTimeout(t);
  }, []);

  const fileMeta = openFileId ? data.files.find((f) => f.id === openFileId) : null;
  const fileContent = openFileId ? (data.fileContents[openFileId] || genericContent(fileMeta)) : null;
  const showConversation = activeSession === "s1";

  return (
    <div className="stage" data-direction={direction} data-theme={theme}>
      <Switcher
        directions={data.directions}
        direction={direction}
        onDirection={setDirection}
        theme={theme}
        onTheme={() => setTheme((t) => (t === "dark" ? "light" : "dark"))}
        onReplay={playMain}
      />
      <div className="win" data-direction={direction} data-theme={theme} data-screen-label="Reasonix Desktop">
        <TabBar
          tabs={data.tabs}
          activeTab={activeTab}
          onTab={setActiveTab}
          sidebarRail={rail}
          onToggleSidebar={() => setRail((r) => !r)}
          dockOpen={dockOpen}
          onToggleDock={toggleDock}
          onOpenPalette={() => setPalette(true)}
        />
        <div className="body">
          <Sidebar
            rail={rail}
            projects={data.projects}
            openProjects={openProjects}
            onToggleProject={(id) => setOpenProjects((p) => ({ ...p, [id]: !p[id] }))}
            activeSession={activeSession}
            onSession={(id) => { setActiveSession(id); setModal(null); }}
            onNew={() => { setActiveSession("s9-new"); setModal(null); }}
            onOpenPalette={() => setPalette(true)}
            view={modal === "history" ? "history" : modal === "trash" ? "trash" : modal === "settings" ? (settingsSection === "memory" ? "memory" : "settings") : null}
            onNav={nav}
          />
          <main className="chat">
            <TopicBar
              changesCount={data.changes.length}
              onOpenChanges={() => { setDockOpen(true); setDockTab("changes"); }}
              onOpenPalette={() => setPalette(true)}
            />
            {notice && <Notice onClose={() => setNotice(false)} />}
            <div className="transcript">
              {showConversation ? (
                <div className="thread">
                  {messages.map((m) => <Message key={m.id} msg={m} />)}
                  {live && <Message key={live.id} msg={live} />}
                </div>
              ) : (
                <EmptyState prompts={data.quickPrompts} onPrompt={(p) => send(p)} />
              )}
            </div>
            <Composer
              value={text}
              onChange={setText}
              onSend={() => send()}
              mode={mode}
              onMode={setMode}
              running={running}
              onStop={stop}
              model={data.status.model}
            />
          </main>
          <Dock
            open={dockOpen}
            split={!!openFileId}
            tab={dockTab}
            onTab={setDockTab}
            files={data.files}
            activeFileId={openFileId}
            onOpenFile={openFile}
            onCloseFile={closeFile}
            fileContent={fileContent}
            changes={data.changes}
            context={data.context}
          />
        </div>
        <StatusBar status={data.status} running={running} />
        <CommandPalette open={palette} onClose={() => setPalette(false)} items={data.commandItems} />
        <SettingsModal
          open={modal === "settings"}
          onClose={() => setModal(null)}
          section={settingsSection}
          setSection={setSettingsSection}
          directions={data.directions}
          direction={direction}
          onDirection={setDirection}
          theme={theme}
          onTheme={setTheme}
          settings={data.settings}
          status={data.status}
          skills={data.skills}
          memoryFiles={data.memoryFiles}
          memories={data.memories}
        />
        <HistoryModal open={modal === "history"} onClose={() => setModal(null)} history={data.history} onOpen={() => { setActiveSession("s1"); setModal(null); }} />
        <TrashModal open={modal === "trash"} onClose={() => setModal(null)} trash={data.trash} />
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
