package control

import (
	"encoding/json"
	"errors"
	"net/url"
	"regexp"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/secrets"
	"reasonix/internal/session"
)

// One bounded, optional record shares the terminal commit. Cold exports can
// inspect it without a resident controller; older readers skip it safely.
func (c *Controller) providerDiagnosticEvent(e event.Event) (*session.Event, error) {
	status := e.Status
	if status == "" {
		status = terminalTurnStatus(e)
	}
	if status != event.TurnFailed && status != event.TurnInterrupted && status != event.TurnRecoveryRequired {
		return nil, nil
	}
	failure := e.Diagnostic
	if failure == nil {
		failure = provider.DiagnoseFailure(e.Err)
	}
	requests, dropped, truncated := c.providerDiagnosticTurnSnapshot(e.TurnID)
	if len(requests) == 0 && failure == nil && (dropped == nil || *dropped == 0) {
		return nil, nil
	}
	payload, err := json.Marshal(struct {
		SchemaVersion  int                         `json:"schemaVersion"`
		Failure        *provider.FailureDiagnostic `json:"failure,omitempty"`
		TransportError string                      `json:"transportError,omitempty"`
		RequestLimit   int                         `json:"requestLimit"`
		Requests       []providerDiagnostic        `json:"requests"`
		Dropped        *uint64                     `json:"dropped,omitempty"`
		Truncated      bool                        `json:"truncated"`
	}{1, failure, diagnosticTransportError(e.Err), 128, requests, dropped, truncated})
	if err != nil {
		return nil, err
	}
	payload, err = secrets.RedactJSON(payload)
	if err != nil {
		return nil, err
	}
	return &session.Event{Kind: "diagnostic/provider", Optional: true, Payload: payload}, nil
}

var diagnosticURL = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

func diagnosticTransportError(err error) string {
	var request *provider.RequestFailure
	var response *provider.APIError
	if !errors.As(err, &request) || errors.As(err, &response) {
		return ""
	}
	// Never persist API response bodies. Transport exceptions retain their cause,
	// with URL credentials/query/fragment removed before credential redaction.
	text := diagnosticURL.ReplaceAllStringFunc(request.Error(), func(raw string) string {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "[redacted URL]"
		}
		u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
		u.ForceQuery = false
		return u.String()
	})
	text = secrets.RedactCredentials(text)
	if len(text) > 2048 {
		text = text[:2048]
	}
	return text
}
