package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"reasonix/internal/base/i18n"
)

// retrySafeCode mirrors RETRY_SAFE in desktop/frontend-next/src/ui/Queue.tsx:
// the one held code the kernel knows never reached the model. Every other code
// on an uncertain entry may have run, so sending it again could repeat a turn.
const retrySafeCode = "steer_unapplied"

// mayHaveRun mirrors retryable() in the same desktop file: a blocked entry is
// retried, but among uncertain entries only the retry-safe code is.
func mayHaveRun(it InboxItem) bool {
	return it.State == "uncertain" && it.BlockCode != retrySafeCode
}

// retryHeldText words why the queue was left alone, keyed by the code the way
// desktop/frontend-next/src/i18n/queue_why.ts does. A code this build has no
// sentence for keeps the kernel's own reason, as the desktop does.
func retryHeldText(it InboxItem) string {
	why := i18n.M.QueueBlockWhy[it.BlockCode]
	if why == "" {
		why = it.BlockReason
	}
	if why == "" {
		why = i18n.M.QueueRetryNoWhy
	}
	return fmt.Sprintf(i18n.M.QueueRetryHeldFmt, shortID(it.ID), why)
}

// queueRetryHeld answers the retry key on an entry that may already have run:
// nothing is sent, the reason is said, and the selection stays so the entry can
// still be edited or deleted.
func (m *model) queueRetryHeld(it InboxItem) tea.Cmd {
	return func() tea.Msg {
		return queueDoneMsg{level: "warn", text: retryHeldText(it)}
	}
}
