package agent

import (
	"sync"

	"reasonix/internal/tool"
)

// turnLoopState groups per-turn loop-guard maps so parallel tool goroutines
// share one lock instead of unsynchronized maps on turnRuntime.
type turnLoopState struct {
	mu                 sync.Mutex
	schemaErrors       map[string]schemaErrorRecord
	dispatchClasses    map[string]tool.CallClass
	resultFingerprints map[string]string
	acceptedDecisions  map[string]acceptedDecision
	errorCategories    map[string]int
	softBudgetNudged   bool
}

func (s *turnLoopState) snapshotDispatchClasses() map[string]tool.CallClass {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dispatchClasses) == 0 {
		return nil
	}
	out := make(map[string]tool.CallClass, len(s.dispatchClasses))
	for k, v := range s.dispatchClasses {
		out[k] = v
	}
	return out
}

func (s *turnLoopState) setDispatchClasses(classes map[string]tool.CallClass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchClasses = classes
}

func (s *turnLoopState) dispatchClass(id string) (tool.CallClass, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	class, ok := s.dispatchClasses[id]
	return class, ok
}

func (s *turnLoopState) noteSchemaError(sig string, record schemaErrorRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schemaErrors == nil {
		s.schemaErrors = map[string]schemaErrorRecord{}
	}
	s.schemaErrors[sig] = record
}

func (s *turnLoopState) schemaError(sig string) schemaErrorRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schemaErrors[sig]
}

func (s *turnLoopState) clearSchemaErrors(match func(string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sig := range s.schemaErrors {
		if match(sig) {
			delete(s.schemaErrors, sig)
		}
	}
}

func (s *turnLoopState) rememberFingerprint(fp, callID string) (prev string, seen bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resultFingerprints == nil {
		s.resultFingerprints = map[string]string{}
	}
	prev, seen = s.resultFingerprints[fp]
	if !seen {
		s.resultFingerprints[fp] = callID
	}
	return prev, seen
}

func (s *turnLoopState) rememberDecision(id, question, answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acceptedDecisions == nil {
		s.acceptedDecisions = map[string]acceptedDecision{}
	}
	s.acceptedDecisions[id] = acceptedDecision{ID: id, Question: question, Answer: answer}
}

func (s *turnLoopState) decision(id string) (acceptedDecision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dec, ok := s.acceptedDecisions[id]
	return dec, ok
}

func (s *turnLoopState) noteErrorCategory(cat string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errorCategories == nil {
		s.errorCategories = map[string]int{}
	}
	s.errorCategories[cat]++
}

func (s *turnLoopState) errorCategoryCount(cat string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorCategories[cat]
}

func (s *turnLoopState) markSoftBudgetNudged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.softBudgetNudged {
		return false
	}
	s.softBudgetNudged = true
	return true
}

func (s *turnLoopState) softBudgetWasNudged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.softBudgetNudged
}
