package boot

import (
	"strings"

	"reasonix/internal/config"
)

func deepSeekProtocolMigrationNoticeError(opts Options, cfg *config.Config, err error) error {
	// Desktop renders resilient-loader warnings with open/reload controls.
	// Suppress only the duplicate migration notice; headless frontends still
	// need the boot event because they do not expose Config.LoadWarnings.
	if strings.TrimSpace(opts.StatsSource) == "desktop" &&
		cfg.HasLoadWarnings() && config.IsDeepSeekProtocolConfigParseError(err) {
		return nil
	}
	return err
}
