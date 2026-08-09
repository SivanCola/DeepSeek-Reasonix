package billing

import (
	"fmt"
	"strings"
	"time"
)

// WalletView is a multi-currency wallet presentation. Balances never use model
// regional price tables — only FX conversion. Never infer region from balance
// magnitude.
type WalletView struct {
	Available        bool         `json:"available"`
	Display          Money        `json:"display"` // ≈ target total when complete
	DisplayApprox    bool         `json:"displayApprox"`
	Complete         bool         `json:"complete"`
	IncompleteReason string       `json:"incompleteReason,omitempty"`
	Wallets          []WalletLine `json:"wallets"`
	RateDate         string       `json:"rateDate,omitempty"`
}

// WalletLine is one original-currency wallet with optional converted value.
type WalletLine struct {
	Original  Money         `json:"original"`
	Converted *Money        `json:"converted,omitempty"`
	Rate      *RateSnapshot `json:"rateSnapshot,omitempty"`
	Stale     bool          `json:"stale,omitempty"`
}

// ConvertBalance builds a WalletView for target display currency using FX only.
func ConvertBalance(b *Balance, target string, table *RateTable, at time.Time) WalletView {
	view := WalletView{Complete: true}
	if b == nil {
		view.Complete = false
		view.IncompleteReason = "no_balance"
		return view
	}
	view.Available = b.Available
	target = NormalizeCurrency(target)
	if at.IsZero() {
		at = time.Now().UTC()
	}

	var total Amount
	haveTotal := target != ""
	convertedAny := false
	for _, info := range b.Infos {
		cur := NormalizeCurrency(info.Currency)
		amt, err := NewAmountFromString(strings.TrimSpace(info.TotalBalance))
		if err != nil {
			view.Complete = false
			view.IncompleteReason = "parse_error"
			continue
		}
		line := WalletLine{Original: MoneyOf(amt, cur)}
		if target == "" || cur == target {
			if cur == target {
				total = total.Add(amt)
			}
			if target == "" {
				// No preference: leave converted nil.
				haveTotal = false
			}
		} else if table != nil {
			conv, snap, ok := table.Convert(amt, cur, target, at, DefaultFXMaxAgeDays)
			if ok {
				m := MoneyOf(conv, target)
				line.Converted = &m
				convertedAny = true
				line.Rate = snap
				line.Stale = snap != nil && snap.Stale
				total = total.Add(conv)
				if snap != nil && view.RateDate == "" {
					view.RateDate = snap.AsOf
				}
			} else {
				haveTotal = false
				view.Complete = false
				view.IncompleteReason = "fx_unavailable"
				if snap != nil {
					line.Rate = snap
					line.Stale = snap.Stale
				}
			}
		} else {
			haveTotal = false
			view.Complete = false
			view.IncompleteReason = "no_fx_table"
		}
		view.Wallets = append(view.Wallets, line)
	}
	if target != "" && total != Zero {
		// Prefer the target-currency total even when some foreign wallets
		// could not convert (partial total, complete=false).
		view.Display = MoneyOf(total, target)
		if !haveTotal {
			view.Complete = false
			if view.IncompleteReason == "" {
				view.IncompleteReason = "partial_wallet_total"
			}
		}
	} else if len(view.Wallets) == 1 {
		view.Display = view.Wallets[0].Original
		view.Complete = true
		view.IncompleteReason = ""
	} else {
		view.Complete = false
		if view.IncompleteReason == "" {
			view.IncompleteReason = "multi_wallet_no_total"
		}
		// Prefer a native target wallet line when present.
		if target != "" {
			for _, w := range view.Wallets {
				if NormalizeCurrency(w.Original.Currency) == target {
					view.Display = w.Original
					break
				}
			}
		}
	}
	// Approximate only totals containing an FX-converted wallet. A native target
	// wallet remains exact even when another wallet is unavailable.
	view.DisplayApprox = convertedAny
	return view
}

// DisplayApproxText renders "≈¥12.34" style compact text for the status line.
func (v WalletView) DisplayApproxText() string {
	if v.Display.Currency == "" || v.Display.Amount == "" || v.Display.IsZero() {
		// Fall back to first wallet original.
		if len(v.Wallets) > 0 {
			w := v.Wallets[0].Original
			return CurrencySymbol(w.Currency) + compactBalanceAmount(w.Amount)
		}
		return ""
	}
	sym := CurrencySymbol(v.Display.Currency)
	text := sym + compactBalanceAmount(v.Display.Amount)
	if v.DisplayApprox {
		return "≈" + text
	}
	return text
}

// DetailText lists each original wallet and converted value for tooltips.
func (v WalletView) DetailText() string {
	if len(v.Wallets) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v.Wallets)+1)
	for _, w := range v.Wallets {
		line := CurrencySymbol(w.Original.Currency) + compactBalanceAmount(w.Original.Amount)
		if w.Converted != nil {
			line += " → ≈" + CurrencySymbol(w.Converted.Currency) + compactBalanceAmount(w.Converted.Amount)
			if w.Rate != nil && w.Rate.AsOf != "" {
				line += " (" + w.Rate.AsOf
				if w.Stale {
					line += ", stale"
				}
				line += ")"
			}
		}
		parts = append(parts, line)
	}
	if v.IncompleteReason != "" {
		parts = append(parts, "incomplete:"+v.IncompleteReason)
	}
	return strings.Join(parts, "; ")
}

func compactBalanceAmount(amount string) string {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return "0.00"
	}
	a, err := NewAmountFromString(amount)
	if err != nil {
		return amount
	}
	// Two-decimal wallet UX.
	scaled := int64(a)
	// Round to 1e-2 currency units (amountScale is 1e9).
	const centsScale = amountScale / 100
	neg := scaled < 0
	if neg {
		scaled = -scaled
	}
	cents := (scaled + centsScale/2) / centsScale
	whole := cents / 100
	frac := cents % 100
	s := fmt.Sprintf("%d.%02d", whole, frac)
	if neg && cents != 0 {
		return "-" + s
	}
	return s
}
