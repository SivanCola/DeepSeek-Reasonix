package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// UserRootDir returns the Reasonix user config root: ~/.reasonix on macOS/Linux,
// %USERPROFILE%\.reasonix on Windows.
func UserRootDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".reasonix")
}

// UserConfigPath returns the global config file: ~/.reasonix/config.toml.
func UserConfigPath() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "config.toml")
}

// UserMCPConfigPath returns the global MCP config file: ~/.reasonix/mcp.toml.
func UserMCPConfigPath() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "mcp.toml")
}

// UserSkillsConfigPath returns the global skills config file: ~/.reasonix/skills.toml.
func UserSkillsConfigPath() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "skills.toml")
}

// UserCredentialsPath returns the global credentials file: ~/.reasonix/credentials,
// falling back to the XDG-based path for compatibility.
func UserCredentialsPath() string {
	root := UserRootDir()
	if root != "" {
		return filepath.Join(root, "credentials")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "reasonix", "credentials")
}

// ArchiveDir returns the archive directory: ~/.reasonix/archive.
func ArchiveDir() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "archive")
}

// SessionDir returns the session directory: ~/.reasonix/sessions.
func SessionDir() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "sessions")
}

// CacheDir returns the cache directory: ~/.reasonix/cache.
func CacheDir() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "cache")
}

// MemoryUserDir returns the user config root for memory: ~/.reasonix.
func MemoryUserDir() string {
	return UserRootDir()
}

// MigrationPath returns the migration record path: ~/.reasonix/migration.json.
func MigrationPath() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "migration.json")
}

// BackupDir returns the config backup directory: ~/.reasonix/backups/config.
func BackupDir() string {
	root := UserRootDir()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "backups", "config")
}

// oldLegacyPaths returns known historical Reasonix config directories for migration.
func oldLegacyPaths() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "reasonix"))
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			paths = append(paths, filepath.Join(appData, "reasonix"))
		}
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			paths = append(paths, filepath.Join(xdg, "reasonix"))
		} else {
			paths = append(paths, filepath.Join(home, ".config", "reasonix"))
		}
	}
	paths = append(paths, filepath.Join(home, ".reasonix"))
	return paths
}
