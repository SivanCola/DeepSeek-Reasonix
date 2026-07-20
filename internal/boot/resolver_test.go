package boot

import (
	"context"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

func TestLocalProviderResolverCatalogOmitsSecrets(t *testing.T) {
	cfg := config.Default()
	if len(cfg.Providers) == 0 {
		t.Fatal("default config has no providers")
	}
	r := NewLocalProviderResolver(cfg, netclient.ProxySpec{Mode: netclient.ModeAuto})
	cat := r.Catalog()
	if len(cat) == 0 {
		t.Fatal("empty catalog")
	}
	for _, d := range cat {
		if d.Ref == "" {
			t.Fatalf("empty ref: %+v", d)
		}
	}
}

func TestResolveProviderUsesInjectedResolver(t *testing.T) {
	called := false
	stub := &provider.StaticResolver{
		Descriptors: []provider.Descriptor{{Ref: "stub/model"}},
		Providers: map[string]provider.Provider{
			"stub/model": stubNamedProvider{name: "stub"},
		},
	}
	opts := Options{ProviderResolver: resolveSpy{inner: stub, called: &called}}
	cfg := config.Default()
	p, err := resolveProvider(opts, cfg, netclient.ProxySpec{}, provider.Selection{Ref: "stub/model"})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("injected resolver was not used")
	}
	if p.Name() != "stub" {
		t.Fatalf("name = %q", p.Name())
	}
}

type stubNamedProvider struct{ name string }

func (s stubNamedProvider) Name() string { return s.name }
func (s stubNamedProvider) Stream(context.Context, provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk)
	close(ch)
	return ch, nil
}

type resolveSpy struct {
	inner  provider.Resolver
	called *bool
}

func (s resolveSpy) Catalog() []provider.Descriptor { return s.inner.Catalog() }
func (s resolveSpy) Resolve(sel provider.Selection) (provider.Provider, error) {
	*s.called = true
	return s.inner.Resolve(sel)
}
