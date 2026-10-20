// Command benchctl prints what the pricing and ablation contracts answer for
// one model or one benchmark arm, so a reviewer can check a rate or an arm name
// without writing a test.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"reasonix/internal/contract/ablation"
	"reasonix/internal/contract/pricing"
)

var errUsage = errors.New("usage: benchctl rate|cost|tariff|arm [flags]")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "benchctl:", err)
		os.Exit(2)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "rate":
		return runRate(args[1:], out)
	case "cost":
		return runCost(args[1:], out)
	case "tariff":
		return runTariff(args[1:], out)
	case "arm":
		return runArm(args[1:], out)
	default:
		return fmt.Errorf("%w: unknown subcommand %q", errUsage, args[0])
	}
}

func runRate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rate", flag.ContinueOnError)
	fs.SetOutput(out)
	provider := fs.String("provider", "deepseek", "vendor")
	model := fs.String("model", "deepseek-flash", "model id")
	currency := fs.String("currency", "", "currency; empty reads the vendor's default")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cur := *currency
	if cur == "" {
		cur = pricing.DefaultCurrency(*provider)
	}
	card := pricing.CurrentRate(*provider, *model, cur)
	if card == nil {
		return fmt.Errorf("no official rate for %s/%s in %s", *provider, *model, cur)
	}
	fmt.Fprintf(out, "%s/%s %s: cache-hit %g input %g output %g per 1M\n",
		*provider, *model, card.Currency, card.CacheHit, card.Input, card.Output)
	if entry, ok := pricing.LookupCatalog(*provider, *model, cur, ""); ok && entry.DocURL != "" {
		fmt.Fprintf(out, "source: %s\n", entry.DocURL)
	}
	fmt.Fprintf(out, "superseded generations: %d\n", len(pricing.SupersededRates(*provider, *model, cur)))
	return nil
}

func runCost(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("cost", flag.ContinueOnError)
	fs.SetOutput(out)
	provider := fs.String("provider", "deepseek", "vendor")
	model := fs.String("model", "deepseek-flash", "model id")
	currency := fs.String("currency", "", "currency; empty reads the vendor's default")
	hit := fs.Int("hit", 0, "cached prompt tokens")
	miss := fs.Int("miss", 0, "uncached prompt tokens")
	completion := fs.Int("completion", 0, "completion tokens")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cur := *currency
	if cur == "" {
		cur = pricing.DefaultCurrency(*provider)
	}
	card := pricing.CurrentRate(*provider, *model, cur)
	if card == nil {
		return fmt.Errorf("no official rate for %s/%s in %s", *provider, *model, cur)
	}
	usage := pricing.UsageTokens{CacheHitTokens: *hit, CacheMissTokens: *miss, CompletionTokens: *completion}
	amount := pricing.OriginalCostAmount(*card, usage)
	fmt.Fprintf(out, "%s %s\n", amount, pricing.NormalizeCurrency(card.Currency))
	return nil
}

func runArm(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("arm", flag.ContinueOnError)
	fs.SetOutput(out)
	spec := fs.String("ablate", "", "comma-separated modules to switch off: "+ablation.ModuleList())
	foldIndex := fs.String("fold-index", "", "fold-index scale: default, half, quarter or off")
	list := fs.Bool("list", false, "list the switchable modules")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *list {
		fmt.Fprintln(out, ablation.ModuleHelp())
		return nil
	}
	set, err := ablation.ParseArm(*spec, *foldIndex)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, set.Arm())
	return nil
}
