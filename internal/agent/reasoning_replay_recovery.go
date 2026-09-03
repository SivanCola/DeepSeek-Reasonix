package agent

import (
	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// reasoningReplayRecoveryBudget bounds the thinking-400 catch-and-repair to one
// retry per model round. It is independent from the context-recovery budget so
// one overflow repair and one reasoning repair can never multiply requests.
type reasoningReplayRecoveryBudget struct {
	retries int
}

// recoverReasoningReplay400 applies the vendor-documented self-heal for a
// provider that rejected replayed thinking history with HTTP 400: rebuild the
// frozen request's messages through the strong projection (all assistant
// reasoning stripped, unpaired tool activity dropped) and retry exactly once.
// Everything except Messages stays byte-identical to the rejected request. A
// history the projection cannot change is not worth a blind retry.
func (a *Agent) recoverReasoningReplay400(frozen samplingRequest, err error, budget *reasoningReplayRecoveryBudget) (samplingRequest, bool) {
	if a == nil || budget == nil || budget.retries > 0 {
		return samplingRequest{}, false
	}
	if provider.AsReasoningReplayError(err) == nil {
		return samplingRequest{}, false
	}
	repaired, changed := provider.ProjectReasoningStrippedMessages(a.svc.prov, frozen.req.Messages)
	if !changed {
		return samplingRequest{}, false
	}
	budget.retries++
	next := frozen.req
	next.Messages = repaired
	return samplingRequest{req: next}, true
}

// activateReasoningReplayStrongProjection records that this conversation's
// canonical history carries reasoning the provider rejects. Later rounds and
// turns keep projecting through the strong projection instead of paying
// another 400 plus repair retry per round.
func (a *Agent) activateReasoningReplayStrongProjection() {
	if a == nil {
		return
	}
	a.sess.reasoningReplayStrongProjection = true
	event.RecordProtocolRecovery(a.svc.sink, event.ProtocolRecoveryAudit{Kind: event.ProtocolRecoveryReasoningReplay400Recovered})
	a.emitReasoningReplayRepairNotice()
}

func (a *Agent) emitReasoningReplayRepairNotice() {
	if a == nil || a.svc.sink == nil {
		return
	}
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelWarn,
		Code:   event.NoticeCodeReasoningReplayRepair,
		Text:   i18n.M.ReasoningReplayRepair,
		Detail: "provider rejected replayed thinking blocks (HTTP 400); retried once with reasoning stripped from the projected history",
	})
}
