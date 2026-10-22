import json
import os
import random
import tempfile
import unittest

import loopmetrics as lm

WORDS = ("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa "
         "quebec romeo sierra tango uniform victor whiskey xray yankee zulu").split()


def varied(seed, words=60):
    rng = random.Random(seed)
    return " ".join(rng.choice(WORDS) + str(rng.randrange(1000)) for _ in range(words))


class Stream:
    def __init__(self):
        self.rows, self.seq = [], 0

    def add(self, kind, **kw):
        self.seq += 1
        self.rows.append({"schema_version": 1, "seq": self.seq, "event": {"kind": kind, **kw}})
        return self

    def turn(self, text="go"):
        return self.add("turn_started", text=text)

    def call(self, cid, name="read_file", args=None, output="x", err="", read_only=True, dispatches=2):
        for _ in range(dispatches):
            self.add("tool_dispatch", tool={"id": cid, "name": name})
        return self.add("tool_result", tool={
            "id": cid, "name": name, "args": json.dumps(args if args is not None else {"path": "a.go"}),
            "output": output, "err": err, "readOnly": read_only})

    def think(self, text):
        return self.add("reasoning", text=text)

    def say(self, text):
        return self.add("text", text=text)

    def usage(self, attempt, prompt=10, completion=5, hit=8, cost=0.01):
        return self.add("usage", usage={"attemptId": attempt, "promptTokens": prompt, "completionTokens": completion,
                                        "cacheHitTokens": hit, "cost": cost})

    def events(self):
        return [(r["seq"], r["event"]) for r in self.rows]

    def write(self, directory, name):
        path = os.path.join(directory, name)
        with open(path, "w", encoding="utf-8") as fh:
            for r in self.rows:
                fh.write(json.dumps(r, ensure_ascii=False) + "\n")
        return path


def metrics(stream, **kw):
    return lm.run_metrics(stream.events(), **kw)


class ToolCalls(unittest.TestCase):
    def test_an_injected_repeat_loop_is_caught(self):
        s = Stream().turn()
        for i in range(4):
            s.call(f"c{i}")
        m = metrics(s)["m1"]
        self.assertEqual((m["unique_calls"], m["redundant"], m["max_run"]), (4, 3, 4))

    def test_a_clean_run_of_distinct_calls_is_not_flagged(self):
        s = Stream().turn()
        for i in range(6):
            s.call(f"c{i}", args={"path": f"f{i}.go"}, output=f"body {i}")
        m = metrics(s)["m1"]
        self.assertEqual((m["redundant"], m["max_run"]), (0, 1))

    def test_a_dispatch_emitted_twice_counts_one_call(self):
        s = Stream().turn().call("c1", dispatches=2)
        self.assertEqual(metrics(s)["m1"]["unique_calls"], 1)

    def test_a_result_event_emitted_twice_counts_one_call(self):
        s = Stream().turn().call("c1")
        s.rows.append(dict(s.rows[-1], seq=s.seq + 1))
        m = metrics(s)["m1"]
        self.assertEqual((m["unique_calls"], m["redundant"]), (1, 0))

    def test_polling_that_returns_new_output_is_not_a_repeat(self):
        s = Stream().turn()
        for i in range(4):
            s.call(f"p{i}", name="bash_output", args={"id": "1"}, output=f"line {i}")
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)

    def test_a_retry_after_a_failure_is_not_a_repeat(self):
        s = Stream().turn().call("c1", err="exit 1", output="boom").call("c2", output="boom")
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)

    def test_the_same_failure_twice_is_a_retry_not_a_repeat(self):
        s = Stream().turn().call("c1", err="exit 1", output="boom").call("c2", err="exit 1", output="boom")
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)

    def test_a_write_between_two_identical_reads_clears_the_history(self):
        s = Stream().turn().call("c1").call("w", name="edit_file", read_only=False, output="ok").call("c2")
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)

    def test_a_repeat_in_another_turn_is_not_a_repeat(self):
        s = Stream().turn().call("c1").turn().call("c2")
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)

    def test_a_dispatch_without_a_result_is_unresolved_and_counts_nowhere(self):
        s = Stream().turn()
        s.add("tool_dispatch", tool={"id": "lost", "name": "bash"})
        m = metrics(s)["m1"]
        self.assertEqual((m["unique_calls"], m["unresolved"]), (0, 1))

    def test_a_rationale_that_differs_hides_a_repeat_unless_ignored(self):
        s = Stream().turn()
        s.call("c1", name="bash", args={"command": "ls", "description": "list"}, read_only=True)
        s.call("c2", name="bash", args={"command": "ls", "description": "list again"}, read_only=True)
        self.assertEqual(metrics(s)["m1"]["redundant"], 0)
        m = metrics(s, ignore=("description",))["m1"]
        self.assertEqual((m["redundant"], m["redundant_without_ignore"]), (1, 0))


class Reasoning(unittest.TestCase):
    def test_a_block_that_repeats_itself_is_caught(self):
        s = Stream().turn().think("let me check the file again. " * 12)
        m = metrics(s)["m2"]
        self.assertGreater(m["within_median"], 0.5)
        self.assertEqual(m["within_flag_share"]["0.5"], 1.0)

    def test_a_block_repeated_across_blocks_is_caught_at_every_threshold(self):
        text = varied(1)
        s = Stream().turn().think(text).think(text).think(text)
        m = metrics(s)["m2"]
        self.assertEqual(m["cross_max"], 1.0)
        for flag in ("0.7", "0.8", "0.9"):
            self.assertEqual(m["cross_flag_share"][flag], 1.0)

    def test_clean_distinct_reasoning_is_not_flagged(self):
        s = Stream().turn()
        for seed in range(5):
            s.think(varied(100 + seed))
        m = metrics(s)["m2"]
        self.assertLess(m["within_max"], 0.1)
        self.assertLess(m["cross_max"], 0.1)
        for flag in m["cross_flag_share"].values():
            self.assertEqual(flag, 0.0)

    def test_natural_prose_is_not_flagged(self):
        paras = [
            "The user wants the config loader to stop silently ignoring unknown keys, so I should look at how the decoder is built before touching anything.",
            "Reading the decoder shows it uses a strict mode only for the top level table, which explains why nested typos pass; the fix belongs in the shared options.",
            "Before editing I will list the callers, because two of them build their own decoders and a change to the shared one would not reach them at all.",
        ]
        s = Stream().turn()
        for p in paras:
            s.think(p)
        m = metrics(s)["m2"]
        self.assertEqual(m["cross_flag_share"]["0.7"], 0.0)
        self.assertEqual(m["within_flag_share"]["0.4"], 0.0)

    def test_short_blocks_are_excluded_and_counted(self):
        s = Stream().turn().think("ok").think("ok")
        m = metrics(s)["m2"]
        self.assertEqual((m["blocks"], m["excluded_short"], m["reasoning_blocks"]), (0, 2, 2))

    def test_punctuation_and_case_do_not_hide_a_repeat(self):
        text = varied(2)
        s = Stream().turn().think(text).think(text.upper().replace(" ", ",  "))
        self.assertEqual(metrics(s)["m2"]["cross_max"], 1.0)

    def test_a_stream_without_reasoning_events_falls_back_to_the_message_and_says_so(self):
        s = Stream().turn().add("message", reasoning=varied(4))
        m = metrics(s)["m2"]
        self.assertEqual((m["source"], m["blocks"]), ("message", 1))

    def test_a_flat_envelope_reads_like_the_wrapped_one(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "flat.jsonl")
            with open(path, "w") as fh:
                for row in (
                    {"kind": "turn_started", "seq": 1},
                    {"kind": "tool_result", "seq": 2, "tool": {"id": "a", "name": "r", "args": "{}", "output": "x", "readOnly": True}},
                    {"kind": "tool_result", "seq": 3, "tool": {"id": "b", "name": "r", "args": "{}", "output": "x", "readOnly": True}},
                ):
                    fh.write(json.dumps(row) + "\n")
            self.assertEqual(lm.run_metrics(lm.load(path))["m1"]["redundant"], 1)

    def test_the_message_event_is_not_read_as_a_second_block(self):
        s = Stream().turn().think(varied(3)).add("message", reasoning=varied(3))
        self.assertEqual(metrics(s)["m2"]["reasoning_blocks"], 1)


class Replies(unittest.TestCase):
    def test_a_repeated_short_reply_is_caught(self):
        s = Stream().turn()
        for _ in range(5):
            s.say("好")
        self.assertEqual(metrics(s)["m4"]["max_run"], 5)

    def test_varied_replies_are_not_flagged(self):
        s = Stream().turn()
        for t in ("好", "完成", "好的", "收到"):
            s.say(t)
        self.assertEqual(metrics(s)["m4"]["max_run"], 1)

    def test_a_long_identical_reply_is_outside_the_cap(self):
        s = Stream().turn()
        for _ in range(3):
            s.say("this reply is longer than the cap")
        self.assertEqual(metrics(s)["m4"]["max_run"], 0)

    def test_a_break_resets_the_run(self):
        s = Stream().turn()
        for t in ("好", "好", "ok done", "好", "好"):
            s.say(t)
        self.assertEqual(metrics(s)["m4"]["max_run"], 2)


class NoTextEvents(unittest.TestCase):
    def test_a_stream_without_text_events_has_no_m4_value_not_a_zero(self):
        self.assertIsNone(metrics(Stream().turn().call("c1"))["m4"]["max_run"])

    def test_a_stream_with_text_events_and_no_repeat_is_zero_or_one(self):
        self.assertEqual(metrics(Stream().turn().say("a long enough reply that is over the cap"))["m4"]["max_run"], 0)


class Resume(unittest.TestCase):
    def first(self):
        s = Stream().turn()
        for i in range(10):
            s.call(f"a{i}", args={"path": f"f{i}.go"}, output=f"body {i}")
        return s.events()

    def test_a_continued_run_that_redoes_the_work_is_caught(self):
        second = Stream().turn()
        for i in range(10):
            second.call(f"b{i}", args={"path": f"f{i}.go"}, output=f"body {i}")
        out = lm.resume_metrics(self.first(), second.events())
        self.assertEqual(out["10"]["share"], 1.0)
        self.assertEqual(out["5"]["share"], 1.0)

    def test_a_continued_run_that_moves_on_is_not_flagged(self):
        second = Stream().turn()
        for i in range(10, 20):
            second.call(f"b{i}", args={"path": f"f{i}.go"}, output=f"body {i}")
        self.assertEqual(lm.resume_metrics(self.first(), second.events())["10"]["share"], 0.0)

    def test_the_same_call_with_a_new_result_is_new_evidence(self):
        second = Stream().turn().call("b0", args={"path": "f0.go"}, output="changed")
        self.assertEqual(lm.resume_metrics(self.first(), second.events())["10"]["redone"], 0)


class Cost(unittest.TestCase):
    def test_usage_is_deduplicated_by_attempt(self):
        s = Stream().turn().usage("a1").usage("a1").usage("a2")
        m = metrics(s)["m5"]
        self.assertEqual((m["requests"], m["completion"]), (2, 10))


class Inputs(unittest.TestCase):
    def test_two_requests_that_share_an_attempt_id_are_both_counted(self):
        s = Stream().turn().usage("sa-1-1", completion=5).usage("sa-1-1", completion=9)
        m = metrics(s)["m5"]
        self.assertEqual((m["requests"], m["completion"]), (2, 14))

    def test_an_identical_usage_event_emitted_twice_counts_once(self):
        s = Stream().turn().usage("sa-1-1").usage("sa-1-1")
        self.assertEqual(metrics(s)["m5"]["requests"], 1)

    def test_a_wire_log_whose_sidecar_says_truncated_is_reported_as_a_prefix(self):
        with tempfile.TemporaryDirectory() as d:
            log = Stream().turn().write(d, "s.wire.jsonl")
            self.assertIsNone(lm.wire_truncated(log))
            with open(os.path.join(d, "s.wire.meta.json"), "w") as fh:
                json.dump({"truncated": True}, fh)
            self.assertTrue(lm.wire_truncated(log))
            self.assertEqual(lm.cell_summary("x", [log])["truncated_wire_logs"], [log])

    def test_a_complete_wire_log_and_a_trajectory_are_not_reported_truncated(self):
        with tempfile.TemporaryDirectory() as d:
            log = Stream().turn().write(d, "s.wire.jsonl")
            with open(os.path.join(d, "s.wire.meta.json"), "w") as fh:
                json.dump({"truncated": False}, fh)
            self.assertFalse(lm.wire_truncated(log))
            self.assertIsNone(lm.wire_truncated(Stream().turn().write(d, "t.jsonl")))


class Legs(unittest.TestCase):
    def test_usage_in_two_processes_is_not_deduplicated_across_them(self):
        a = Stream().turn().usage("sa-1-1")
        b = Stream().turn().usage("sa-1-1")
        m = lm.run_metrics([a.events(), b.events()])["m5"]
        self.assertEqual(m["requests"], 2)

    def test_legs_sum_calls_and_keep_turns_apart(self):
        a = Stream().turn().call("c1")
        b = Stream().turn().call("c2")
        m = lm.run_metrics([a.events(), b.events()])
        self.assertEqual((m["turns"], m["m1"]["unique_calls"], m["m1"]["redundant"]), (2, 2, 0))

    def test_a_run_given_as_comma_separated_files_is_one_run(self):
        with tempfile.TemporaryDirectory() as d:
            one = Stream().turn().call("c1").usage("a").write(d, "1.jsonl")
            two = Stream().turn().call("c2").usage("a").write(d, "2.jsonl")
            s = lm.cell_summary("x", [one + "," + two])
            self.assertEqual((s["runs"], s["series"]["requests"]["median"]), (1, 2))


class Cells(unittest.TestCase):
    def build(self, n, directory):
        paths = []
        for i in range(n):
            s = Stream().turn().call("c").think(varied(i)).say("done").usage("a")
            paths.append(s.write(directory, f"run{i}.jsonl"))
        return paths

    def test_a_cell_with_fewer_than_five_runs_is_not_a_result(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(lm.cell_summary("x", self.build(4, d))["verdict"], "not a result")

    def test_a_cell_with_five_runs_is_a_result_with_spread(self):
        with tempfile.TemporaryDirectory() as d:
            s = lm.cell_summary("x", self.build(5, d))
            self.assertEqual(s["verdict"], "ok")
            self.assertEqual(s["series"]["requests"], {"median": 1, "min": 1, "max": 1, "n": 5})

    def test_an_unreadable_run_is_counted_as_failed_not_dropped(self):
        with tempfile.TemporaryDirectory() as d:
            paths = self.build(5, d)
            with open(os.path.join(d, "empty.jsonl"), "w"):
                pass
            s = lm.cell_summary("x", paths + [os.path.join(d, "empty.jsonl"), os.path.join(d, "missing.jsonl")])
            self.assertEqual((s["runs"], len(s["failed"])), (5, 2))


if __name__ == "__main__":
    unittest.main()
