package team

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"reasonix/internal/state/memory"
)

// ReviewLine is one line of a conversation a memory review reads. User marks
// what the person said, the only thing that makes a review worth a request.
type ReviewLine struct {
	From string
	Text string
	User bool
}

// Reviewer asks a model once, with no tools, and returns its text.
type Reviewer func(ctx context.Context, system, prompt string) (string, error)

// ReviewReport counts what a review changed.
type ReviewReport struct {
	Added, Edited, Removed int
}

// Bounds on one review, so its cost stays a short request however long the
// conversation was.
const (
	reviewMaxChanges = 8
	reviewUserChars  = 3000
	reviewOtherChars = 800
	reviewBudget     = 16000
	reviewBodyChars  = 200
)

const reviewSystem = `You keep an agent's long-term memory. Read the memory it holds now and the new lines of its conversation, and decide what to change so the memory stays true and useful.
- Add durable facts: who the person is, their preferences, decisions and constraints that the work itself would not show.
- Edit an entry the conversation shows is now wrong or incomplete; remove one only when it is plainly wrong.
- Never save what matters only to this conversation, nor anything that looks like a key, token or password.
Answer with JSON only, in this shape:
{"changes":[{"op":"add","description":"<one-line hook>","type":"user|feedback|project|reference","body":"<the fact>"},{"op":"edit","entry":<number>,"description":"...","body":"..."},{"op":"remove","entry":<number>}]}
At most 8 changes. Answer {"changes":[]} when nothing should change.`

type reviewChange struct {
	Op          string `json:"op"`
	Entry       int    `json:"entry"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Body        string `json:"body"`
}

// ReviewMemory folds what was said into store, through the same remember and
// forget tools an agent uses, so a review names, updates and archives facts
// exactly as the agent would. Lines with nothing the person said ask no model.
func ReviewMemory(ctx context.Context, store memory.Store, lines []ReviewLine, ask Reviewer) (ReviewReport, error) {
	if !slices.ContainsFunc(lines, func(l ReviewLine) bool { return l.User && strings.TrimSpace(l.Text) != "" }) {
		return ReviewReport{}, nil
	}
	facts, err := store.List()
	if err != nil {
		return ReviewReport{}, err
	}
	reply, err := ask(ctx, reviewSystem, reviewPrompt(facts, lines))
	if err != nil {
		return ReviewReport{}, err
	}
	changes, err := parseReview(reply)
	if err != nil {
		return ReviewReport{}, err
	}
	var report ReviewReport
	remember, forget := memory.NewRememberTool(store), memory.NewForgetTool(store)
	for _, ch := range changes[:min(len(changes), reviewMaxChanges)] {
		var target *memory.Memory
		if ch.Entry >= 1 && ch.Entry <= len(facts) {
			target = &facts[ch.Entry-1]
		}
		switch {
		case ch.Op == "add" && strings.TrimSpace(ch.Body) != "":
			if run(ctx, remember, map[string]string{"description": ch.Description, "type": ch.Type, "body": ch.Body}) {
				report.Added++
			}
		case ch.Op == "edit" && target != nil && strings.TrimSpace(ch.Body) != "":
			if run(ctx, remember, map[string]string{"name": target.Name, "description": firstNonEmpty(ch.Description, target.Description), "body": ch.Body}) {
				report.Edited++
			}
		case ch.Op == "remove" && target != nil:
			if run(ctx, forget, map[string]string{"name": target.Name}) {
				report.Removed++
			}
		}
	}
	return report, nil
}

func run(ctx context.Context, t interface {
	Execute(context.Context, json.RawMessage) (string, error)
}, args map[string]string) bool {
	for k, v := range args {
		if v == "" {
			delete(args, k)
		}
	}
	raw, _ := json.Marshal(args)
	_, err := t.Execute(ctx, raw)
	return err == nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// reviewPrompt numbers the memory held and lists the new lines, each cut to
// its budget; when the whole runs over, the oldest lines go first.
func reviewPrompt(facts []memory.Memory, lines []ReviewLine) string {
	var held strings.Builder
	held.WriteString("Memory held now:\n")
	if len(facts) == 0 {
		held.WriteString("(none)\n")
	}
	for i, m := range facts {
		fmt.Fprintf(&held, "%d. %s: %s\n   %s\n", i+1, m.Name, m.Description, clip(oneLine(m.Body), reviewBodyChars))
		if held.Len() > reviewBudget/2 {
			fmt.Fprintf(&held, "… %d more not shown\n", len(facts)-i-1)
			break
		}
	}
	var said []string
	room := reviewBudget - held.Len()
	for i := len(lines) - 1; i >= 0 && room > 0; i-- {
		limit := reviewOtherChars
		if lines[i].User {
			limit = reviewUserChars
		}
		line := fmt.Sprintf("[%s] %s", lines[i].From, clip(lines[i].Text, limit))
		room -= len(line)
		said = append(said, line)
	}
	slices.Reverse(said)
	return held.String() + "\nNew lines of the conversation:\n" + strings.Join(said, "\n")
}

// clip keeps a text's head and tail inside limit runes, which is where what
// someone asks and what they conclude usually sit.
func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	half := (limit - 3) / 2
	return string(r[:half]) + " … " + string(r[len(r)-half:])
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// parseReview reads the changes out of a reply, tolerating prose or a code
// fence around the one JSON object it asked for.
func parseReview(reply string) ([]reviewChange, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return nil, ErrReviewReply
	}
	var out struct {
		Changes []reviewChange `json:"changes"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReviewReply, err)
	}
	return out.Changes, nil
}
