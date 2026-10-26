package team

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"reasonix/internal/base/secrets"
)

func TestAnAgentAsksForASecretInItsOwnConversationOnly(t *testing.T) {
	h := newHarness(t, "Chief", "Ada")
	ctx := context.Background()
	out, err := h.svc.RequestSecret(ctx, Caller{Agent: h.id("Ada")}, "shop key", "to call the shop API")
	if err != nil || !strings.Contains(out, "$REASONIX_SECRET_SHOP_KEY") {
		t.Fatalf("request = %q, %v", out, err)
	}
	book, _ := h.store.Secrets()
	if len(book.Requests) != 1 || book.Requests[0].Name != "SHOP_KEY" || book.Requests[0].Agent != h.id("Ada") {
		t.Fatalf("requests = %+v", book.Requests)
	}
	g := groupOf(t, h, h.id("Chief"), h.id("Chief"), h.id("Ada"))
	if _, err := h.svc.RequestSecret(ctx, Caller{Agent: h.id("Ada"), Group: g}, "x", ""); !errors.Is(err, ErrPrivateOnly) {
		t.Fatalf("a request from a group seat = %v, want ErrPrivateOnly", err)
	}
}

// The user saves a secret: the request closes, the value is masked wherever
// this process writes for a model, the asking agent is told how to use it and
// never what it is, and only the agents it was given to see it in a shell.
func TestASavedSecretReachesOnlyTheAgentsItWasGivenTo(t *testing.T) {
	h := newHarness(t, "Chief", "Ada", "Ben")
	ctx := context.Background()
	if _, err := h.svc.RequestSecret(ctx, Caller{Agent: h.id("Ada")}, "SHOP_KEY", "the shop"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.UserSaveSecret(ctx, "SHOP_KEY", "sk-shop-0123456789", "the shop", []string{h.id("Ada")}, h.id("Ada")); err != nil {
		t.Fatal(err)
	}
	defer secrets.UnregisterValue("sk-shop-0123456789")
	book, _ := h.store.Secrets()
	if len(book.Requests) != 0 || len(book.Secrets) != 1 {
		t.Fatalf("book = %+v", book)
	}
	if got := secrets.MaskRegistered("key sk-shop-0123456789"); got != "key [secret:SHOP_KEY]" {
		t.Fatalf("the saved value is not masked: %q", got)
	}
	told := h.panes.pane(SeatRef{Agent: h.id("Ada")}).deliveries()
	if len(told) != 1 || told[0].Kind != DeliverSecret || !strings.Contains(told[0].Text, "$REASONIX_SECRET_SHOP_KEY") || strings.Contains(told[0].Text, "sk-shop") {
		t.Fatalf("Ada was told %+v", told)
	}
	if env := h.svc.SecretEnv(h.id("Ada")); !slices.Contains(env, "REASONIX_SECRET_SHOP_KEY=sk-shop-0123456789") {
		t.Fatalf("Ada's env = %v", env)
	}
	if env := h.svc.SecretEnv(h.id("Ben")); len(env) != 0 {
		t.Fatalf("Ben, who was not given it, has %v", env)
	}

	if err := h.svc.UserDeleteSecret(ctx, "SHOP_KEY"); err != nil {
		t.Fatal(err)
	}
	if got := secrets.MaskRegistered("sk-shop-0123456789"); got != "sk-shop-0123456789" {
		t.Fatalf("a deleted secret is still masked: %q", got)
	}
	if env := h.svc.SecretEnv(h.id("Ada")); len(env) != 0 {
		t.Fatalf("after delete Ada has %v", env)
	}
}

func TestADeclinedRequestTellsTheAgent(t *testing.T) {
	h := newHarness(t, "Chief", "Ada")
	ctx := context.Background()
	if _, err := h.svc.RequestSecret(ctx, Caller{Agent: h.id("Ada")}, "SHOP_KEY", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.UserCancelSecret(ctx, "SHOP_KEY", h.id("Ada")); err != nil {
		t.Fatal(err)
	}
	book, _ := h.store.Secrets()
	told := h.panes.pane(SeatRef{Agent: h.id("Ada")}).deliveries()
	if len(book.Requests) != 0 || len(told) != 1 || !strings.Contains(told[0].Text, `status="declined"`) {
		t.Fatalf("book %+v, told %+v", book, told)
	}
}

func TestStoredSecretsAreMaskedFromTheStart(t *testing.T) {
	h := newHarness(t, "Chief")
	if err := h.svc.UserSaveSecret(context.Background(), "TOKEN", "tok-9876543210", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	secrets.UnregisterValue("tok-9876543210")
	defer secrets.UnregisterValue("tok-9876543210")
	if err := RegisterSecrets(h.store); err != nil {
		t.Fatal(err)
	}
	if got := secrets.MaskRegistered("tok-9876543210"); got != "[secret:TOKEN]" {
		t.Fatalf("a stored secret was not registered: %q", got)
	}
}
