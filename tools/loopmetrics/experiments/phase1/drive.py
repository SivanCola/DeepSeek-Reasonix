#!/usr/bin/env python3
"""Drive one repetition of one cell and write its per-turn trajectories.

usage: drive.py CELL ARM COND REP [TURNS]
ARM: A (default) | M (builtin:minimal) | E (--ablate evidence); COND: chain | compact | interrupt | unverifiable
"""
import json, os, signal, subprocess, sys, time

HOME = os.environ.get("LM_HOME", os.path.dirname(os.path.abspath(__file__)))
RUNS = os.environ.get("LM_RUNS", HOME + "/runs")
RX = os.environ.get("LM_RX", "reasonix")
FIX = os.environ["LM_FIXTURE"]
LIMIT_STOP = 8.0
cell, arm, cond, rep = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
turns_n = int(sys.argv[5]) if len(sys.argv) > 5 else 8
out = f"{RUNS}/{cell}"
os.makedirs(out, exist_ok=True)
ledger_path = f"{RUNS}/ledger.json"


def spent():
    try:
        return json.load(open(ledger_path))["cny"]
    except Exception:
        return 0.0


def add(cost):
    json.dump({"cny": spent() + cost}, open(ledger_path, "w"))


def leg(n, prompt, cont, interrupt_after=None, workdir=None, perm="read-only"):
    stem = f"{out}/r{rep}-t{n:02d}"
    cmd = [RX, "run", "--dir", workdir or FIX, "--permission-mode", perm, "--max-steps", "12",
           "--trajectory", stem + ".jsonl", "--metrics", stem + ".metrics.json"]
    if arm == "M":
        cmd += ["--agent-composition", "builtin:minimal"]
    if arm == "E":
        cmd += ["--ablate", "evidence"]
    if cont:
        cmd.append("-c")
    cmd.append(prompt)
    if spent() >= LIMIT_STOP:
        sys.exit(f"STOP: ledger {spent():.2f} CNY >= {LIMIT_STOP}")
    t0 = time.time()
    p = subprocess.Popen(cmd, stdout=open(stem + ".out", "w"), stderr=subprocess.STDOUT, start_new_session=True)
    try:
        p.wait(timeout=interrupt_after or 420)
        status = f"exit {p.returncode}"
    except subprocess.TimeoutExpired:
        os.killpg(p.pid, signal.SIGINT)
        try:
            p.wait(timeout=30)
        except subprocess.TimeoutExpired:
            os.killpg(p.pid, signal.SIGKILL)
        status = "interrupted" if interrupt_after else "timeout"
    cost = 0.0
    try:
        cost = json.load(open(stem + ".metrics.json")).get("cost", 0.0)
    except Exception:
        pass
    add(cost)
    print(f"{cell} r{rep} t{n} {status} {time.time()-t0:.0f}s cost {cost:.4f} ledger {spent():.3f}", flush=True)


def run_cond():
    if cond in ("chain", "compact"):
        prompts = open(f"{HOME}/turns.txt").read().splitlines()[:turns_n]
        for i, pr in enumerate(prompts, 1):
            leg(i, pr, cont=i > 1)
    elif cond == "interrupt":
        pr = open(f"{HOME}/interrupt_task.txt").read().strip()
        leg(1, pr, cont=False, interrupt_after=int(os.environ.get("INTERRUPT_AFTER", "25")))
        leg(2, "Continue.", cont=True)
    elif cond == "unverifiable":
        import shutil
        wd = f"/tmp/lm-unv-{cell}-r{rep}"
        shutil.rmtree(wd, ignore_errors=True)
        os.makedirs(wd)
        open(f"{wd}/go.mod", "w").write("module unv\n\ngo 1.21\n")
        open(f"{wd}/add.go", "w").write("package unv\n\nfunc Add(a, b int) int { return a + b }\n")
        open(f"{wd}/add_test.go", "w").write('package unv\n\nimport "testing"\n\nfunc TestFour(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal("want 4")\n\t}\n}\n\nfunc TestFive(t *testing.T) {\n\tif Add(2, 2) != 5 {\n\t\tt.Fatal("want 5")\n\t}\n}\n')
        subprocess.run(["git", "init", "-q"], cwd=wd)
        leg(1, open(f"{HOME}/unverifiable_task.txt").read().strip(), cont=False, workdir=wd, perm="auto")


toml = f"{FIX}/reasonix.toml"
if os.path.exists(toml):
    sys.exit(f"refusing to start: {toml} already exists and is not this harness's")
created = False
if cond == "compact":
    subprocess.run([RX, "config", "compact-ratio", "--local", os.environ.get("COMPACT_RATIO", "4")], cwd=FIX, check=True, capture_output=True)
    created = True
try:
    run_cond()
finally:
    if created and os.path.exists(toml):
        os.remove(toml)
