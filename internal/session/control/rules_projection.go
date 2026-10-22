// rules_projection.go — what the model has of the project's rule files.
package control

import (
	"strings"

	"reasonix/internal/ext/rules"
	"reasonix/internal/ext/skill"
)

// rulesSet is the model's view of the rules: the unconditional ones and the
// path-scoped ones are two blocks, each delivered once and owed again when what
// the host would render now differs from what the model has.
type rulesSet struct {
	always projectionDebt
	scoped projectionDebt
}

// rulesClearedBlock replaces the rules when they are gone: silence would leave
// the ones the model already holds standing as current.
const rulesClearedBlock = "# Rules\n\nThis project's rules have been removed. Anything you were told under this heading earlier no longer applies."

// rulesScopedNoneBlock replaces the scoped rules when none applies to the files
// this session has touched any more.
const rulesScopedNoneBlock = "# Path-scoped rules\n\nNone of this project's path-scoped rules apply to the files this session has touched. The scoped rules you were sent earlier no longer hold."

// loadRules reads the rule files as they are on disk now. It rediscovers each
// turn, like the standing instructions: an edit made past every writer is still
// an edit. A session with no resolved workspace has no project to read.
func (c *Controller) loadRules() (rules.Result, bool) {
	opts := c.memory.current().LoadOptions()
	if strings.TrimSpace(opts.CWD) == "" {
		return rules.Result{}, false
	}
	return rules.Load(rules.Options{WorkspaceRoot: opts.CWD, UserDir: opts.UserDir}), true
}

func (r *rulesSet) owedAlways(res rules.Result) string {
	block := rules.Block(res.Always)
	if block == "" && r.always.visible(projectRulesTag) {
		block = rulesClearedBlock
	}
	return r.always.owed(projectRulesTag, block)
}

func (r *rulesSet) owedScoped(res rules.Result, hits *skill.PathHits) string {
	var eligible []rules.Rule
	for _, rule := range res.Scoped {
		if hits.EligibleGlobs(rule.Globs, rule.Gated) {
			eligible = append(eligible, rule)
		}
	}
	block := rules.ScopedBlock(eligible)
	if block == "" && r.scoped.visible(scopedRulesTag) {
		block = rulesScopedNoneBlock
	}
	return r.scoped.owed(scopedRulesTag, block)
}
