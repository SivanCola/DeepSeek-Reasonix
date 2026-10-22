#!/usr/bin/env python3
"""Measure repetition in Reasonix runs from the --trajectory JSONL stream.

Thresholds are fixed in README.md before any real trajectory was analysed;
judgement reads structure (equality, set overlap), never wording.
"""

import argparse
import hashlib
import json
import statistics
import sys
import unicodedata

SCHEMA_VERSION = 1

SHINGLE = 8
MIN_BLOCK = 40
JACCARD_FLAGS = (0.7, 0.8, 0.9)
WITHIN_FLAGS = (0.4, 0.5, 0.6)
LOOKAHEAD = (5, 10, 20)
REPLY_CAP = 8
MIN_RUNS = 5


class TrajectoryError(Exception):
    """The file cannot be read as a trajectory stream."""


def load(path):
    out = []
    try:
        with open(path, encoding="utf-8") as fh:
            for n, line in enumerate(fh, 1):
                line = line.strip()
                if not line:
                    continue
                try:
                    row = json.loads(line)
                except ValueError as e:
                    raise TrajectoryError(f"{path}:{n}: {e}") from e
                if isinstance(row, dict) and isinstance(row.get("event"), dict):
                    out.append((row.get("seq", n), row["event"]))
                elif isinstance(row, dict) and "kind" in row:
                    out.append((row.get("seq", n), row))
    except OSError as e:
        raise TrajectoryError(str(e)) from e
    if not any(ev.get("kind") == "turn_started" for _, ev in out):
        raise TrajectoryError(f"{path}: no turn_started event")
    return out


def turns(events):
    cur, out = [], []
    for item in events:
        if item[1].get("kind") == "turn_started":
            if cur:
                out.append(cur)
            cur = []
        cur.append(item)
    if cur:
        out.append(cur)
    return [t for t in out if t[0][1].get("kind") == "turn_started"]


def normalise(text):
    return "".join(c.lower() for c in text if unicodedata.category(c)[0] in "LNM")


def canonical(args, ignore):
    try:
        doc = json.loads(args)
    except (TypeError, ValueError):
        return args if isinstance(args, str) else ""
    if isinstance(doc, dict) and ignore:
        doc = {k: v for k, v in doc.items() if k not in ignore}
    return json.dumps(doc, sort_keys=True, ensure_ascii=False, separators=(",", ":"))


def resolved_calls(turn):
    seen, calls, dispatched = set(), [], set()
    for seq, ev in turn:
        tool = ev.get("tool") or {}
        if ev.get("kind") == "tool_dispatch" and tool.get("id"):
            dispatched.add(tool["id"])
        if ev.get("kind") == "tool_result" and tool.get("id") and tool["id"] not in seen:
            seen.add(tool["id"])
            calls.append({
                "id": tool["id"],
                "name": tool.get("resolvedName") or tool.get("name") or "",
                "args": tool.get("args", ""),
                "result": hashlib.sha256(
                    (str(tool.get("output", "")) + "\x00" + str(tool.get("err", ""))).encode()
                ).hexdigest(),
                "ok": not tool.get("err"),
                "read_only": bool(tool.get("readOnly")),
            })
    return calls, len(dispatched - seen)


def redundancy(calls, ignore=()):
    history, redundant, run, best, prev = {}, 0, 0, 0, None
    for c in calls:
        key = (c["name"], canonical(c["args"], ignore))
        if not c["read_only"]:
            history.clear()
        elif key in history and history[key][1] and c["ok"] and history[key][0] == c["result"]:
            redundant += 1
        history[key] = (c["result"], c["ok"])
        ident = (key, c["result"]) if c["ok"] and c["read_only"] else None
        run = run + 1 if ident is not None and ident == prev else (1 if ident is not None else 0)
        best = max(best, run)
        prev = ident
    return redundant, best


def shingles(norm):
    return [norm[i:i + SHINGLE] for i in range(len(norm) - SHINGLE + 1)]


def reasoning(turn):
    blocks, excluded, chars, nblocks = [], 0, 0, 0
    texts = [ev.get("text") or "" for _, ev in turn if ev.get("kind") == "reasoning"]
    source = "block"
    if not texts:
        texts = [ev.get("reasoning") or "" for _, ev in turn if ev.get("kind") == "message" and ev.get("reasoning")]
        source = "message"
    for text in texts:
        chars += len(text)
        nblocks += 1
        norm = normalise(text)
        if len(norm) < MIN_BLOCK:
            excluded += 1
            continue
        blocks.append(shingles(norm))
    within = [1 - len(set(b)) / len(b) for b in blocks]
    cross, sets = [], []
    for b in blocks:
        s = set(b)
        if sets:
            cross.append(max(len(s & o) / len(s | o) for o in sets))
        sets.append(s)
    return within, cross, excluded, chars, nblocks, source


def replies(events):
    best = run = 0
    prev = None
    for _, ev in events:
        if ev.get("kind") != "text":
            continue
        norm = normalise(ev.get("text") or "")
        if 0 < len(norm) <= REPLY_CAP and norm == prev:
            run += 1
        elif 0 < len(norm) <= REPLY_CAP:
            run = 1
        else:
            run = 0
        prev = norm
        best = max(best, run)
    return best


def usage(events):
    seen, tot = set(), {"requests": 0, "prompt": 0, "completion": 0, "cache_hit": 0, "cost": 0.0}
    for seq, ev in events:
        u = ev.get("usage")
        if ev.get("kind") != "usage" or not u:
            continue
        key = u.get("attemptId") or ("seq", seq)
        if key in seen:
            continue
        seen.add(key)
        tot["requests"] += 1
        tot["prompt"] += u.get("promptTokens", 0)
        tot["completion"] += u.get("completionTokens", 0)
        tot["cache_hit"] += u.get("cacheHitTokens", 0)
        tot["cost"] += u.get("cost", 0) or 0
    return tot


def share(values, flag):
    return sum(1 for v in values if v >= flag) / len(values) if values else None


def med(values):
    return statistics.median(values) if values else None


def run_metrics(events, ignore=()):
    uniq = red = best = unresolved = 0
    within, cross, excluded, chars, nblocks = [], [], 0, 0, 0
    all_calls, sources = [], set()
    for t in turns(events):
        calls, lost = resolved_calls(t)
        all_calls.extend(calls)
        r, b = redundancy(calls, ignore)
        uniq, red, best, unresolved = uniq + len(calls), red + r, max(best, b), unresolved + lost
        w, c, ex, ch, nb, src = reasoning(t)
        sources.add(src)
        within += w
        cross += c
        excluded, chars, nblocks = excluded + ex, chars + ch, nblocks + nb
    out = {
        "schema_version": SCHEMA_VERSION,
        "turns": len(turns(events)),
        "m1": {"unique_calls": uniq, "redundant": red, "rate": red / uniq if uniq else None, "max_run": best, "unresolved": unresolved},
        "m2": {
            "source": "/".join(sorted(sources)) or None, "blocks": len(within), "excluded_short": excluded, "reasoning_chars": chars, "reasoning_blocks": nblocks,
            "within_median": med(within), "within_max": max(within, default=None),
            "cross_median": med(cross), "cross_max": max(cross, default=None),
            "within_flag_share": {str(f): share(within, f) for f in WITHIN_FLAGS},
            "cross_flag_share": {str(f): share(cross, f) for f in JACCARD_FLAGS},
        },
        "m4": {"max_run": replies(events)},
        "m5": usage(events),
        "_calls": all_calls,
    }
    if ignore:
        t_red = sum(redundancy(resolved_calls(t)[0], ())[0] for t in turns(events))
        out["m1"]["redundant_without_ignore"] = t_red
    return out


def resume_metrics(first, second):
    done = {(c["name"], canonical(c["args"], ()), c["result"]) for t in turns(first) for c in resolved_calls(t)[0] if c["ok"]}
    first_turn = turns(second)[0] if turns(second) else []
    calls = resolved_calls(first_turn)[0]
    out = {}
    for k in LOOKAHEAD:
        head = calls[:k]
        redo = sum(1 for c in head if (c["name"], canonical(c["args"], ()), c["result"]) in done)
        out[str(k)] = {"calls": len(head), "redone": redo, "share": redo / len(head) if head else None}
    return out


SERIES = (
    ("m1.rate", lambda r: r["m1"]["rate"]),
    ("m1.max_run", lambda r: r["m1"]["max_run"]),
    ("m2.within_median", lambda r: r["m2"]["within_median"]),
    ("m2.cross_max", lambda r: r["m2"]["cross_max"]),
    ("m2.cross_share@0.8", lambda r: r["m2"]["cross_flag_share"]["0.8"]),
    ("m2.within_share@0.5", lambda r: r["m2"]["within_flag_share"]["0.5"]),
    ("m4.max_run", lambda r: r["m4"]["max_run"]),
    ("reasoning_chars", lambda r: r["m2"]["reasoning_chars"]),
    ("requests", lambda r: r["m5"]["requests"]),
    ("completion_tokens", lambda r: r["m5"]["completion"]),
    ("cost", lambda r: r["m5"]["cost"]),
)


def cell_summary(name, paths, ignore=()):
    runs, failed = [], []
    for p in paths:
        try:
            runs.append(run_metrics(load(p), ignore))
        except TrajectoryError as e:
            failed.append(f"{p}: {e}")
    out = {
        "cell": name, "runs": len(runs), "failed": failed,
        "verdict": "ok" if len(runs) >= MIN_RUNS else "not a result",
        "series": {},
    }
    for label, fn in SERIES:
        vals = [v for v in (fn(r) for r in runs) if v is not None]
        out["series"][label] = {"median": med(vals), "min": min(vals, default=None), "max": max(vals, default=None), "n": len(vals)}
    return out


def fmt(v):
    return "-" if v is None else (f"{v:.3f}" if isinstance(v, float) else str(v))


def print_cell(s):
    print(f"cell {s['cell']}: {s['runs']} runs, {len(s['failed'])} failed -> {s['verdict']}")
    for f in s["failed"]:
        print(f"  failed: {f}")
    for label, v in s["series"].items():
        print(f"  {label:<22} median {fmt(v['median'])}  min {fmt(v['min'])}  max {fmt(v['max'])}  n={v['n']}")


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("file")
    r.add_argument("--json", action="store_true")
    r.add_argument("--ignore-arg", action="append", default=[])
    c = sub.add_parser("cell")
    c.add_argument("--name", required=True)
    c.add_argument("--json", action="store_true")
    c.add_argument("--ignore-arg", action="append", default=[])
    c.add_argument("files", nargs="+")
    s = sub.add_parser("resume")
    s.add_argument("first")
    s.add_argument("second")
    a = ap.parse_args(argv)
    try:
        if a.cmd == "run":
            m = run_metrics(load(a.file), tuple(a.ignore_arg))
            m.pop("_calls")
            print(json.dumps(m, indent=2, ensure_ascii=False) if a.json else json.dumps(m, ensure_ascii=False))
        elif a.cmd == "cell":
            summary = cell_summary(a.name, a.files, tuple(a.ignore_arg))
            print(json.dumps(summary, indent=2, ensure_ascii=False)) if a.json else print_cell(summary)
        else:
            print(json.dumps(resume_metrics(load(a.first), load(a.second)), indent=2))
    except TrajectoryError as e:
        print(f"loopmetrics: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
