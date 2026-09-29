package config

func providerHTTPCompatibilityTOML(p ProviderEntry) string {
	if p.HTTP1Only {
		return "http1_only  = true\n"
	}
	return ""
}
