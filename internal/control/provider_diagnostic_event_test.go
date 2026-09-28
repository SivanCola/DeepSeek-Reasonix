package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
	"reasonix/internal/session"
	"reasonix/internal/tool"
)

type diagnosticFailureTransport struct{ calls int }

func (r *diagnosticFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, http2.ConnectionError(http2.ErrCodeProtocol)
}

func TestProviderFailureSurvivesCleanupAndColdExport(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "synchronous", true: "desktop-send"}[async], func(t *testing.T) {
			testProviderFailureColdExport(t, async)
		})
	}
}

func testProviderFailureColdExport(t *testing.T, async bool) {
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "provider-diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	transport := &diagnosticFailureTransport{}
	p, err := openai.New(provider.Config{Name: "test", Protocol: "openai", Model: "test-model", APIKey: "private-key", BaseURL: "https://provider.test/v1", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(p, tool.NewRegistry(), agent.NewSession("stable system"), agent.Options{}, event.Discard)
	done := make(chan event.Event, 1)
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	})
	c := newOwnedTestController(t, Options{Runner: exec, Executor: exec, Sink: sink, SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	if async {
		c.Send("hello")
		if terminal := waitTurnDoneEvent(t, done); terminal.Err == nil {
			t.Fatal("expected transport failure")
		}
		waitIdleAdmission(t, c)
	} else if err := c.RunTurn(t.Context(), "hello"); err == nil {
		t.Fatal("expected transport failure")
	}
	if transport.calls != 1 {
		t.Fatalf("request count=%d", transport.calls)
	}
	var recovery *provider.InterruptedTurnRecovery
	for _, m := range c.sessionEventStore().Snapshot().Projection.Messages {
		if m.InterruptedTurn != nil {
			recovery = m.InterruptedTurn
		}
	}
	if recovery == nil || recovery.TerminalStatus != "failed" || recovery.FailureDiagnostic == nil || recovery.FailureDiagnostic.TransportCode != "PROTOCOL_ERROR" {
		t.Fatalf("cleanup lost failure: %+v", recovery)
	}
	commits := terminationCommitHistory(t, c)
	count := 0
	for _, commit := range commits {
		for _, e := range commit.Events {
			if e.Kind != "diagnostic/provider" {
				continue
			}
			count++
			if !e.Optional || commit.Events[len(commit.Events)-1].Kind != "turn/end" {
				t.Fatal("diagnostic is not optional and atomic with termination")
			}
			var payload struct {
				Failure        *provider.FailureDiagnostic
				Requests       []providerDiagnostic
				TransportError string
			}
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Failure == nil || payload.Failure.TransportCode != "PROTOCOL_ERROR" || len(payload.Requests) != 1 || !strings.Contains(payload.TransportError, "connection error: PROTOCOL_ERROR") {
				t.Fatalf("incomplete durable evidence: %s", e.Payload)
			}
			r := payload.Requests[0]
			if r.Host != "provider.test" || r.RequestPath != "/v1/chat/completions" || r.Phase != "request_error" || r.TransportCode != "PROTOCOL_ERROR" || r.RequestBytes <= 0 {
				t.Fatalf("request evidence: %+v", r)
			}
		}
	}
	if count != 1 {
		t.Fatalf("diagnostic count=%d", count)
	}
	ref := runtime.Ref()
	c.Close()
	<-c.closeFinalized
	if err := service.CloseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Query().CaptureDiagnosticSnapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteColdSessionDiagnostics(t.Context(), &out, service.Query(), snapshot, GoalDiagnosticMetadata{}, nil); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "PROTOCOL_ERROR") || !strings.Contains(out.String(), "diagnostic/provider") || strings.Contains(out.String(), "private-key") {
		t.Fatalf("invalid cold export: %s", out.String())
	}
}

func TestProviderDiagnosticEventIsBoundedAndTurnScoped(t *testing.T) {
	c := &Controller{}
	for id := uint64(1); id <= 130; id++ {
		c.recordProviderRequest("current", provider.RequestObservation{ID: id, Phase: "request_started"})
	}
	c.recordProviderRequest("other", provider.RequestObservation{ID: 131, Phase: "request_started"})
	e, err := c.providerDiagnosticEvent(event.Event{TurnID: "current", Err: context.Canceled})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Failure  *provider.FailureDiagnostic
		Requests []providerDiagnostic
	}
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Requests) != 127 || payload.Failure.Kind != provider.FailureKindCancelled {
		t.Fatalf("bounded evidence: %+v", payload)
	}
	for _, request := range payload.Requests {
		if request.TurnID != "current" {
			t.Fatal("cross-turn evidence")
		}
	}
	if e, err := c.providerDiagnosticEvent(event.Event{TurnID: "unused"}); err != nil || e != nil {
		t.Fatal("invented evidence for an unobserved turn")
	}
}

func TestTerminationPreservesFailureWithoutChangingModelContext(t *testing.T) {
	for _, status := range []string{"failed", "interrupted", ""} {
		t.Run(status, func(t *testing.T) {
			recovery := &provider.InterruptedTurnRecovery{Pending: true, TerminalStatus: status}
			if status == "failed" {
				recovery.FailureDiagnostic = &provider.FailureDiagnostic{Kind: "transport_protocol", TransportCode: "PROTOCOL_ERROR"}
			}
			user := provider.Message{ID: "user", Role: provider.RoleUser, Content: "hello"}
			local := provider.Message{ID: "local", Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: recovery}
			before := []provider.Message{user, local}
			got := planCancelledMessages(before, 0, user, time.Time{}, func(provider.Message) bool { return true }, nil)
			oldBytes, _ := json.Marshal(provider.ModelMessages(before))
			newBytes, _ := json.Marshal(provider.ModelMessages(got))
			if !bytes.Equal(oldBytes, newBytes) {
				t.Fatal("diagnostics changed model-visible bytes")
			}
			after := got[len(got)-1].InterruptedTurn
			if after.TerminalStatus != status || (after.FailureDiagnostic == nil) != (recovery.FailureDiagnostic == nil) {
				t.Fatalf("lost terminal metadata: %+v", after)
			}
			if after.FailureDiagnostic != nil {
				after.FailureDiagnostic.Kind = "changed"
				if recovery.FailureDiagnostic.Kind == "changed" {
					t.Fatal("aliased original diagnostic")
				}
			}
		})
	}
}

func TestDiagnosticTransportErrorRedactsBeforePersistence(t *testing.T) {
	err := &provider.RequestFailure{Operation: "request failed", Err: &url.Error{Op: "Post", URL: "https://user:password@provider.test/v1/chat?arbitrary=private-query#private-fragment", Err: errors.New("connection error: PROTOCOL_ERROR")}}
	got := diagnosticTransportError(err)
	for _, private := range []string{"user:", "password", "private-query", "private-fragment", "arbitrary="} {
		if strings.Contains(got, private) {
			t.Fatalf("leaked %q: %s", private, got)
		}
	}
	if !strings.Contains(got, "PROTOCOL_ERROR") || !strings.Contains(got, "provider.test/v1/chat") {
		t.Fatal(got)
	}
	proxy := &provider.RequestFailure{Err: errors.New("proxy socks5://user:password@proxy.test:1080/?opaque=private-query failed")}
	if got := diagnosticTransportError(proxy); strings.Contains(got, "password") || strings.Contains(got, "private-query") {
		t.Fatal("proxy URL credentials or query persisted")
	}
	if got := diagnosticTransportError(&provider.APIError{Status: 400, Body: "private request body"}); got != "" {
		t.Fatal("persisted provider response body")
	}
	if got := diagnosticTransportError(&provider.RequestFailure{Err: &provider.APIError{Status: 400, Body: "private request body"}}); got != "" {
		t.Fatal("persisted a wrapped provider response body")
	}
}
