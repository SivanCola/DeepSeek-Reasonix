package evidence

// TextObservation is a turn-scoped, content-free record of a line window the
// model was shown. Only canonical path, line position, and SHA-256 line
// digests are retained; source text is never stored in the ledger.
type TextObservation struct {
	Sequence   uint64
	Path       string
	StartLine  int
	LineHashes []string
}
