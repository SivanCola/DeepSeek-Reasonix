package cli

import (
	"fmt"
	"os"
	"strings"

	"reasonix/internal/config"
)

func providerCommand(args []string) int {
	if len(args) == 0 {
		providerUsage()
		return 2
	}
	switch args[0] {
	case "import":
		return providerImportCommand(args[1:])
	case "help", "-h", "--help":
		providerUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown provider subcommand %q\n\n", args[0])
		providerUsage()
		return 2
	}
}

func providerImportCommand(args []string) int {
	if len(args) == 0 || args[0] != "cc-switch" {
		providerImportUsage()
		return 2
	}
	replaceKeys := false
	dryRun := false
	for _, arg := range args[1:] {
		switch arg {
		case "--replace-keys":
			replaceKeys = true
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			providerImportUsage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "unknown provider import flag %q\n\n", arg)
			providerImportUsage()
			return 2
		}
	}
	if dryRun {
		return providerImportDryRun()
	}
	result, err := config.ImportCCSwitchProviders(nil, replaceKeys)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("imported %d providers from cc-switch (%d added, %d updated, %d skipped; keys: %d imported, %d skipped)\n",
		result.Imported, result.Added, result.Updated, result.Skipped, result.KeyImported, result.KeySkipped)
	printProviderImportSkipped(result.SkippedCandidates)
	return 0
}

func providerImportDryRun() int {
	candidates, err := config.LoadCCSwitchProviderCandidates()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(candidates) == 0 {
		fmt.Println("cc-switch provider candidates: none")
		return 0
	}
	fmt.Println("cc-switch provider candidates:")
	for _, c := range candidates {
		status := c.Status
		if c.Importable {
			status = "importable"
		}
		host := c.Host
		if host == "" {
			host = "-"
		}
		target := c.TargetName
		if target == "" {
			target = "-"
		}
		key := "missing key"
		if c.KeyPresent {
			key = "key present"
		}
		fmt.Printf("  %-22s %-14s %-10s host=%-24s models=%-3d target=%-24s %s\n",
			clipProviderImportText(c.Name, 22), c.AppType, c.Kind, clipProviderImportText(host, 24), len(c.Models), clipProviderImportText(target, 24), status)
		if key != "" {
			fmt.Printf("    %s", key)
			if len(c.Reasons) > 0 {
				fmt.Printf(" · %s", strings.Join(c.Reasons, ", "))
			}
			fmt.Println()
		}
	}
	return 0
}

func printProviderImportSkipped(skipped []config.ProviderImportSkipped) {
	for _, s := range skipped {
		reason := strings.TrimSpace(s.Reason)
		if reason == "" {
			reason = "not importable"
		}
		fmt.Printf("skipped %s: %s\n", s.Name, reason)
	}
}

func clipProviderImportText(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if s == "" || len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func providerUsage() {
	fmt.Print(`Manage model providers.

Usage:
  reasonix provider import cc-switch [--replace-keys]
  reasonix provider import cc-switch --dry-run
`)
}

func providerImportUsage() {
	fmt.Print(`Usage:
  reasonix provider import cc-switch [--replace-keys]
  reasonix provider import cc-switch --dry-run
`)
}
