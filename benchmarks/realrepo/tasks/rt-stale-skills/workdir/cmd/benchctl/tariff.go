package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"reasonix/internal/contract/pricing"
)

func runTariff(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("tariff", flag.ContinueOnError)
	fs.SetOutput(out)
	provider := fs.String("provider", "deepseek", "vendor")
	model := fs.String("model", "deepseek-flash", "model id")
	currency := fs.String("currency", "", "currency; empty reads the vendor's default")
	at := fs.String("at", "", "instant to read the schedule at, RFC 3339; empty is now")
	if err := fs.Parse(args); err != nil {
		return err
	}
	when := time.Now()
	if *at != "" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return fmt.Errorf("-at: %w", err)
		}
		when = t
	}
	cur := *currency
	if cur == "" {
		cur = pricing.DefaultCurrency(*provider)
	}
	card := pricing.CurrentRate(*provider, *model, cur)
	if card == nil {
		return fmt.Errorf("no official rate for %s/%s in %s", *provider, *model, cur)
	}
	fmt.Fprintf(out, "official card: %v\n", pricing.IsOfficialCard(*provider, *model, *card))
	tariff, ok := pricing.LiveTariff(*provider, *model, *card, when)
	switch {
	case !ok:
		fmt.Fprintln(out, "schedule: one rate around the clock")
	case !tariff.Changes:
		fmt.Fprintf(out, "schedule: peak=%v, no change ahead\n", tariff.Peak)
	default:
		fmt.Fprintf(out, "schedule: peak=%v until %s\n", tariff.Peak, tariff.ChangesAt.UTC().Format(time.RFC3339))
	}
	return nil
}
