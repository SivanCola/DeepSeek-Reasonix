package boot

import (
	"reasonix/internal/config"
)

func handleConfigLoadWarnings(opts Options, cfg *config.Config) bool {
	if cfg == nil || opts.OnConfigLoadWarnings == nil {
		return false
	}
	warnings := cfg.LoadWarnings()
	handled := opts.OnConfigLoadWarnings(warnings)
	// Empty arrays clear resolved notices, but do not prove a later migration
	// failure was presented to the user.
	return handled && len(warnings) > 0
}

func deepSeekProtocolMigrationNoticeError(configLoadWarningsHandled bool, err error) error {
	// Suppress only after a frontend explicitly accepts the resilient-loader
	// warnings. StatsSource is only a usage label and does not prove that its
	// caller can present Config.LoadWarnings.
	if configLoadWarningsHandled && config.IsDeepSeekProtocolConfigParseError(err) {
		return nil
	}
	return err
}
