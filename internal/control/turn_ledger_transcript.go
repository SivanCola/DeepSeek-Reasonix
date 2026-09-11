package control

import (
	"log/slog"
	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/turnevent"
)

func (c *Controller) updateTurnLedgerTranscript(ledger *turnevent.Ledger) *provider.ReadCompletion {
	if c.executor != nil && c.executor.Session() != nil {
		session := c.executor.Session()
		messages, _, rewrite := session.DisplayBaseline()
		ledger.SetTranscriptRewriteEpoch(rewrite)
		digest, digestErr := session.ContentDigest()
		if digestErr != nil {
			slog.Warn("controller: compute terminal transcript digest", "err", digestErr)
		} else {
			ledger.SetTranscriptSnapshot(int64(session.TranscriptVersion()), digest)
		}
		if ref, ok := session.Head(); ok {
			ledger.SetTranscriptHead(ref.HeadID, session.LeafID())
		} else {
			ledger.SetTranscriptHead("", "")
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].ReadCompletion != nil {
				return messages[i].ReadCompletion
			}
			if agent.IsUserAuthoredTurnMessage(messages[i]) {
				break
			}
		}
	}
	return nil
}
