package boot

import (
	"reasonix/internal/config"
)

func deepSeekProtocolMigrationNoticeError(opts Options, cfg *config.Config, err error) error {
	// Suppress only after a frontend explicitly accepts the resilient-loader
	// warnings. StatsSource is only a usage label and does not prove that its
	// caller can present Config.LoadWarnings.
	if cfg.HasLoadWarnings() && config.IsDeepSeekProtocolConfigParseError(err) &&
		opts.OnConfigLoadWarnings != nil {
		if opts.OnConfigLoadWarnings(cfg.LoadWarnings()) {
			return nil
		}
	}
	return err
}
