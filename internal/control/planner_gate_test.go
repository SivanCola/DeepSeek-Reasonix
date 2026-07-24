package control

import (
	"context"
	"testing"

	"reasonix/internal/agent"
)

func TestTaskWarrantsPlanner(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"", false},
		{"   ", false},
		{"/init", false},
		{"1", false},
		{"2.", false},
		{"A", false},
		{"好的", false},
		{"继续", false},
		{"选 1", false},
		{"what does this function do?", false}, // low-risk question → executor only
		{"why did the test fail", false},
		{"解释一下这段代码", false},
		{reasoningLanguageBlock("zh") + "\n\nwhat does this function do?", false},
		{reasoningLanguageBlock("en") + "\n\n" + PlanModeMarker + "\n\nfix the bug", false},
		{reasoningLanguageBlock("en") + "\n\nfix the bug", true},
		{"fix the bug", true},        // terse, but a work request → still planned
		{"add a login button", true}, // ditto
		{"执行修复", true},
		{"开始迁移", true},
		{"继续重构", true},
		{"continue fixing tests", true},
		{"implement the new caching layer across the backend", true},
		{"who wrote this file?", false},
		{"where is the config file?", false},
		{"when does this run?", false},
		{"which file has the error?", false},
		{"explain this code", false},
		{"describe the architecture", false},
		{"tell me about this function", false},
		{"is this safe?", false},
		{"are we done?", false},
		{"can you help?", false},
		{"can you fix the failing tests across the backend", true},
		{"could you update the README", false}, // explicit single-target edit → executor only
		{"should we remove the stale config option", true},
		{"would you add a regression test", true},
		{"do you fix flaky tests here", true},
		{"does it work?", false},
		{"did the test pass?", false},
		{"should I use mutex here?", false},
		{"would this approach work?", false},
		{"list all the endpoints", false},
		{"summarize the changes", false},
		{"compare these two approaches", false},
		{"what's the status?", false},
		{"介绍一下这个项目", false},
		{"说一下这个函数的作用", false},
		{"帮我看一下这个报错", false},
		{"是什么意思", false},
		{"有没有现成的方案", false},
		{"能不能这样做", false},
		{"请问这个怎么用", false},
		{"how do I implement a new caching layer", true},
		{"what's the best way to refactor this module", true},
		{"explain how to migrate from v1 to v2", true},
		{goalContinueTurn, false},
		{goalSelfCheckTurn, false},
		{"No tool calls in recent turns. Either make progress with tools or signal [goal:blocked:<reason>].", false},
		{"Goal signaled complete but issues remain:\n- the following tasks are still incomplete:\n  - Fix login (in_progress)\nFix or use todo_write/complete_step to mark done, then [goal:complete] again.", false},
		{activeGoalBlock("execute plan: fix the parser", GoalResearchAuto) + "\n\n" + goalContinueTurn, false},
		{activeGoalBlock("execute plan: fix the parser", GoalResearchAuto) + "\n\n" + goalSelfCheckTurn, false},
		{activeGoalBlock("implement the new caching layer", GoalResearchAuto) + "\n\nimplement the new caching layer across the backend", true},
	}
	for _, c := range cases {
		if got := TaskWarrantsPlanner(c.input); got != c.want {
			t.Errorf("TaskWarrantsPlanner(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestNewPlannerGateUsesDeterministicTaskPolicy(t *testing.T) {
	gate := NewPlannerGate()
	if gate == nil {
		t.Fatal("NewPlannerGate returned nil")
	}
	if got := gate(context.Background(), "what is this?"); got {
		t.Error("planner gate should skip low-risk questions")
	}
	if got := gate(context.Background(), "fix the bug"); !got {
		t.Error("planner gate should plan work requests")
	}
}

func TestDecidePlannerRouteMatrix(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		meta   plannerTurnMetadata
		route  agent.PlannerRoute
		depth  agent.PlannerDepth
		reason string
	}{
		{
			name:   "explicit plan mode bypasses dual planner",
			input:  "fix the bug",
			meta:   plannerTurnMetadata{ExplicitPlanMode: true},
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonExplicitPlanMode,
		},
		{
			name:   "trusted synthetic turn bypasses dual planner",
			input:  "perform a brand new implementation",
			meta:   plannerTurnMetadata{Synthetic: true},
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonSynthetic,
		},
		{
			name:   "user asks for plan only",
			input:  "先规划这个认证迁移，不要执行",
			route:  agent.PlannerRoutePlanOnly,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonUserPlanOnly,
		},
		{
			name:   "bare plan first continues to executor",
			input:  "先规划这个认证迁移",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonUserPlanAndExecute,
		},
		{
			name:   "english plan first continues to executor",
			input:  "plan first, then handle the authentication migration",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonUserPlanAndExecute,
		},
		{
			name:   "user asks to approve plan before execution",
			input:  "先规划这个认证迁移，等我确认后再执行",
			route:  agent.PlannerRoutePlanForApproval,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonUserPlanApproval,
		},
		{
			name:   "user asks to plan then execute",
			input:  "先规划再执行这个认证迁移",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonUserPlanAndExecute,
		},
		{
			name:   "user explicitly skips planner",
			input:  "直接改 auth.go，别规划",
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonUserDirect,
		},
		{
			name:   "quoted directive is not an override",
			input:  "解释“直接改”是什么意思",
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonLowRiskQuestion,
		},
		{
			name:   "context dependent fix stays with executor",
			input:  "fix it",
			meta:   plannerTurnMetadata{HasConversationContext: true},
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonContextContinuation,
		},
		{
			name:   "standalone context dependent fix remains ambiguous",
			input:  "fix it",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthLight,
			reason: plannerReasonWorkRequest,
		},
		{
			name:   "atomic readme edit skips planner",
			input:  "fix typo in README",
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonAtomicEdit,
		},
		{
			name:   "atomic nil check skips planner",
			input:  "add a nil check in internal/foo.go",
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonAtomicEdit,
		},
		{
			name:   "bounded anchored work gets light plan",
			input:  "add regression coverage in internal/foo_test.go",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthLight,
			reason: plannerReasonAnchoredWork,
		},
		{
			name:   "ambiguous bug gets full plan",
			input:  "fix the bug",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonAmbiguousWork,
		},
		{
			name:   "high risk overrides single target",
			input:  "fix the token authorization race in auth.go",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonHighRisk,
		},
		{
			name:   "cross surface gets full plan",
			input:  "update frontend and backend for the new profile field",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonCrossSurface,
		},
		{
			name:   "complex guidance gets light plan",
			input:  "how do I implement a local cache?",
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthLight,
			reason: plannerReasonGuidance,
		},
		{
			name:   "delivery upgrades non atomic work",
			input:  "add a login button",
			meta:   plannerTurnMetadata{DeliveryProfile: true},
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonWorkRequest,
		},
		{
			name:   "delivery keeps atomic edit direct",
			input:  "fix typo in README",
			meta:   plannerTurnMetadata{DeliveryProfile: true},
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonAtomicEdit,
		},
		{
			name:   "active goal upgrades non atomic work",
			input:  "add a login button",
			meta:   plannerTurnMetadata{GoalActive: true},
			route:  agent.PlannerRoutePlanAndExecute,
			depth:  agent.PlannerDepthFull,
			reason: plannerReasonGoalActive,
		},
		{
			name:   "active goal alone does not force an atomic edit through planner",
			input:  "fix typo in README",
			meta:   plannerTurnMetadata{GoalActive: true},
			route:  agent.PlannerRouteExecutorOnly,
			depth:  agent.PlannerDepthNone,
			reason: plannerReasonAtomicEdit,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := withPlannerTurnMetadata(context.Background(), tc.meta)
			got := DecidePlannerRoute(ctx, tc.input)
			if got.Route != tc.route || got.Depth != tc.depth || got.Reason != tc.reason {
				t.Fatalf("decision = %+v, want route=%s depth=%s reason=%s", got, tc.route, tc.depth, tc.reason)
			}
			if got.Route != agent.PlannerRouteExecutorOnly && got.MaxResearchRounds <= 0 {
				t.Fatalf("planned decision has no research budget: %+v", got)
			}
		})
	}
}

func TestPlannerPolicyUsesPristineMetadataInsteadOfInjectedContext(t *testing.T) {
	ctx := withPlannerTurnMetadata(context.Background(), plannerTurnMetadata{
		UserText: "fix typo in README",
	})
	input := activeGoalBlock("migrate authentication across the backend", GoalResearchAuto) +
		"\n\n<capability-route>\nhigh risk migration\n</capability-route>\n\nfix typo in README"
	got := DecidePlannerRoute(ctx, input)
	if got.Route != agent.PlannerRouteExecutorOnly || got.Reason != plannerReasonAtomicEdit {
		t.Fatalf("decision used injected context instead of pristine user text: %+v", got)
	}
}
