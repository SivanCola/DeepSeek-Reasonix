package config

import (
	"os"

	"github.com/BurntSushi/toml"
)

// skillsTOMLTop mirrors the top-level structure of ~/.reasonix/skills.toml.
type skillsTOMLTop struct {
	Skills SkillsConfig `toml:"skills"`
}

// loadSkillsTOML reads ~/.reasonix/skills.toml and replaces cfg.Skills
// entirely — inline [skills] from config.toml is ignored when the file exists.
// An absent file is not an error.
func loadSkillsTOML(cfg *Config) error {
	path := UserSkillsConfigPath()
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	var top skillsTOMLTop
	if _, err := toml.DecodeFile(path, &top); err != nil {
		return err
	}
	// Replace entirely: skills.toml is the sole authority.
	cfg.Skills = top.Skills
	return nil
}

// SaveSkillsTOML writes the skills config to ~/.reasonix/skills.toml.
func SaveSkillsTOML(cfg *Config) error {
	path := UserSkillsConfigPath()
	if path == "" {
		return nil
	}
	content := RenderSkillsTOML(cfg)
	return writeConfigFile(path, content)
}

// RenderSkillsTOML serializes skills config as annotated TOML.
func RenderSkillsTOML(cfg *Config) string {
	var b string
	b = "# Skills configuration.\n"
	b += "# Paths: extra custom skill roots scanned globally.\n"
	b += "# DisabledSkills: named skills hidden from the prompt and slash invocation.\n\n"
	b += "[skills]\n"
	if len(cfg.Skills.Paths) > 0 {
		b += "paths = " + renderStringArray(cfg.Skills.Paths) + "\n"
	} else {
		b += "# paths = [\"~/my-skills\"]\n"
	}
	if disabled := cfg.DisabledSkillNames(); len(disabled) > 0 {
		b += "disabled_skills = " + renderStringArray(disabled) + "\n"
	} else {
		b += "# disabled_skills = [\"review\"]\n"
	}
	return b
}
