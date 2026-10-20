// tariff.go — which rate a vendor's own schedule charges right now.
package pricing

import "time"

// Tariff is the vendor's schedule as it stands at one instant: whether it is in
// its peak window and, when it will flip, when.
type Tariff struct {
	Peak      bool
	ChangesAt time.Time
	Changes   bool
}

// LiveTariff reports the window a model's rates are billed in at the given
// instant. It answers only for a rate confirmed as the vendor's own table and
// only where that table has a peak rate: a custom price is the user's own
// arrangement, and projecting someone else's schedule onto it would announce a
// tariff nobody charges.
func LiveTariff(providerKind, model string, rates RateCard, at time.Time) (Tariff, bool) {
	entry, ok := MatchesCatalog(providerKind, model, rates)
	if !ok {
		return Tariff{}, false
	}
	if entry.Peak == nil || entry.Window == nil {
		return Tariff{}, false
	}
	next, changes := entry.Window.NextChange(at)
	return Tariff{Peak: entry.Window.IsPeak(at), ChangesAt: next, Changes: changes}, true
}
