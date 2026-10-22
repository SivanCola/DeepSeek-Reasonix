package config

func (r Roots) defaultConfig() *Config {
	cfg := Default()
	cfg.roots = r
	mode, err := r.installationCollaboration(false)
	applyInstallationCollaboration(cfg, mode, err)
	return cfg
}

func (r Roots) runtimeConfigBase(root string, persist, withProject bool) (*Config, map[string]string) {
	mode, err := r.installationCollaboration(persist)
	var expansionEnv map[string]string
	if withProject {
		expansionEnv = r.loadDotEnvForRoot(root)
	} else {
		r.loadCredentialStoreForScope(root, false)
	}
	cfg := r.defaultConfig()
	applyInstallationCollaboration(cfg, mode, err)
	cfg.setExpansionEnv(expansionEnv)
	cfg.CredentialsStore = r.credentialsStoreMode()
	return cfg, expansionEnv
}
