package control

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/base/i18n"
	"reasonix/internal/contract/config"
	"reasonix/internal/state/teamroster"
)

// /team reads the team from any session, the terminal's included, and /msg
// leaves a message in the team's outbox for the window holding the team.
func TestTheTeamCommandsWorkFromAnySession(t *testing.T) {
	c, notices := settingsController(t)
	i18n.DetectLanguage("en")
	if c.managementNotice("/team"); !strings.Contains(lastNotice(t, notices()), "no agents yet") {
		t.Fatal("an empty team did not say so")
	}
	store := teamroster.Store{Dir: config.RootsForHome("").TeamDir()}
	var ada string
	if _, err := store.Update(context.Background(), func(r *teamroster.Roster) error {
		r.AddAgent(teamroster.Agent{Name: "Chief", Main: true, CreatedBy: teamroster.ByUser})
		ada = r.AddAgent(teamroster.Agent{Name: "Ada Lovelace", Role: "writer", CreatedBy: teamroster.ByUser}).ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c.managementNotice("/team")
	listed := lastNotice(t, notices())
	for _, want := range []string{"Chief", "main", "Ada Lovelace", "writer", "/msg"} {
		if !strings.Contains(listed, want) {
			t.Fatalf("/team lacks %q:\n%s", want, listed)
		}
	}

	c.managementNotice("/msg ada lovelace write the weekly report")
	if got := lastNotice(t, notices()); !strings.Contains(got, "sent to Ada Lovelace") {
		t.Fatalf("/msg = %q", got)
	}
	queued, err := store.PendingUserMessages(context.Background())
	if err != nil || len(queued) != 1 || queued[0].To != ada || queued[0].Text != "write the weekly report" {
		t.Fatalf("outbox = %+v, %v", queued, err)
	}
	c.managementNotice("/msg Nobody hello")
	if got := lastNotice(t, notices()); !strings.Contains(got, `"Nobody"`) {
		t.Fatalf("an unknown agent = %q", got)
	}
	c.managementNotice("/msg")
	if got := lastNotice(t, notices()); !strings.Contains(got, "usage") {
		t.Fatalf("no arguments = %q", got)
	}
}
