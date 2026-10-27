package control

import (
	"context"
	"encoding/json"
	"fmt"

	"reasonix/internal/contract/tool"
)

// TeamChangeApprovalTool is the card a model-proposed team change waits on when
// it widens what an agent can reach. Only a person answers it: no posture,
// grant, hook or remembered rule stands in.
const TeamChangeApprovalTool = "team_change"

// confirmTeamChange asks the person before an agent's working folder moves to
// one a model chose. Approving it is the trust decision for that folder.
func (c *Controller) confirmTeamChange(ctx context.Context, agent, folder string) error {
	args, _ := json.Marshal(map[string]string{"agent": agent, "field": "folder", "to": folder})
	text := fmt.Sprintf("Let the agent %s read and write files in %s?", agent, folder)
	reply, err := c.requestApprovalDecision(ctx, approvalRequest{tool: TeamChangeApprovalTool, subject: agent, args: args, reason: text, fresh: true})
	switch {
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		return tool.Refusal{Code: "team.no_approver", Message: "Nobody answered the request to change " + agent + "'s working folder, so it was not changed."}
	case !reply.allow:
		return tool.Refusal{Code: "team.declined", Message: "The user declined giving " + agent + " that folder; leave it as it is."}
	}
	return nil
}
