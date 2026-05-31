package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/command"
	"reasonix/internal/event"
)

type fakeAutoPlanClassifier struct {
	needsPlan bool
	reason    string
	err       error
	calls     int
}

func (f *fakeAutoPlanClassifier) NeedsPlan(ctx context.Context, input string, score int) (bool, string, error) {
	f.calls++
	return f.needsPlan, f.reason, f.err
}

func TestCustomCommandLookup(t *testing.T) {
	c := New(Options{Commands: []command.Command{{Name: "review"}, {Name: "git:commit"}}})

	if _, ok := c.CustomCommand("/review the diff"); !ok {
		t.Error("review should be found")
	}
	if _, ok := c.CustomCommand("/git:commit"); !ok {
		t.Error("git:commit should be found")
	}
	if _, ok := c.CustomCommand("/missing"); ok {
		t.Error("missing should not be found")
	}
}

func TestComposePlanModeMarker(t *testing.T) {
	c := New(Options{}) // no executor — SetPlanMode still tracks the flag

	if got := c.Compose("hi"); got != "hi" {
		t.Errorf("plan off: Compose = %q, want verbatim", got)
	}

	c.SetPlanMode(true)
	got := c.Compose("hi")
	if !strings.HasPrefix(got, PlanModeMarker) || !strings.HasSuffix(got, "hi") {
		t.Errorf("plan on: Compose = %q, want marker-prefixed", got)
	}
}

func TestComposeAutoPlanComplexTask(t *testing.T) {
	var notices []string
	c := New(Options{
		AutoPlan: "ask",
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.Notice {
				notices = append(notices, e.Text)
			}
		}),
	})

	input := "实现 GitHub issue #2395：\n- 新增配置项\n- 自动判断复杂任务\n- 补测试和文档"
	got := c.Compose(input)
	if !strings.HasPrefix(got, PlanModeMarker) {
		t.Fatalf("complex task should auto-enter plan mode, got %q", got)
	}
	if !c.PlanMode() {
		t.Fatal("controller plan mode should be on after auto-plan")
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "auto plan") {
		t.Fatalf("notice = %v, want one auto-plan notice", notices)
	}
}

func TestComposeAutoPlanSkipsSimpleQuestion(t *testing.T) {
	c := New(Options{AutoPlan: "ask"})

	got := c.Compose("解释一下这个函数做什么？")
	if strings.HasPrefix(got, PlanModeMarker) {
		t.Fatalf("simple question should not auto-plan: %q", got)
	}
	if c.PlanMode() {
		t.Fatal("controller plan mode should remain off")
	}
}

func TestComposeAutoPlanOff(t *testing.T) {
	c := New(Options{AutoPlan: "off"})

	input := "实现 GitHub issue #2395：\n- 新增配置项\n- 自动判断复杂任务\n- 补测试和文档"
	got := c.Compose(input)
	if got != input {
		t.Fatalf("auto_plan=off should compose verbatim, got %q", got)
	}
	if c.PlanMode() {
		t.Fatal("controller plan mode should remain off")
	}
}

func TestComposeAutoPlanClassifierBorderlineTrue(t *testing.T) {
	classifier := &fakeAutoPlanClassifier{needsPlan: true, reason: "borderline multi-step"}
	c := New(Options{AutoPlan: "ask", Classifier: classifier})

	got := c.Compose("实现一个小的配置入口")
	if !strings.HasPrefix(got, PlanModeMarker) {
		t.Fatalf("classifier true should auto-plan, got %q", got)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls = %d, want 1", classifier.calls)
	}
}

func TestComposeAutoPlanClassifierBorderlineFalse(t *testing.T) {
	classifier := &fakeAutoPlanClassifier{needsPlan: false, reason: "single obvious edit"}
	c := New(Options{AutoPlan: "ask", Classifier: classifier})

	got := c.Compose("实现一个小的配置入口")
	if strings.HasPrefix(got, PlanModeMarker) {
		t.Fatalf("classifier false should skip auto-plan, got %q", got)
	}
	if c.PlanMode() {
		t.Fatal("controller plan mode should remain off")
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls = %d, want 1", classifier.calls)
	}
}

func TestComposeAutoPlanClassifierFallback(t *testing.T) {
	classifier := &fakeAutoPlanClassifier{err: errors.New("bad json")}
	c := New(Options{AutoPlan: "ask", Classifier: classifier})

	got := c.Compose("实现 README 文档更新")
	if !strings.HasPrefix(got, PlanModeMarker) {
		t.Fatalf("score 2 should fall back to heuristic auto-plan, got %q", got)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls = %d, want 1", classifier.calls)
	}
}
