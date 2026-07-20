package protocol

import "errors"

// Stable error codes returned in ErrorBody.Code. Keep values short and stable;
// UI maps them to localized copy.
const (
	CodeProtocolMismatch   = "protocol_mismatch"
	CodeCapabilityMismatch = "capability_mismatch"
	CodeUnauthorized       = "unauthorized"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodeInvalidRequest     = "invalid_request"
	CodeSessionBusy        = "session_busy"
	CodeSessionInUse       = "session_in_use"
	CodeBrokerUnavailable  = "broker_unavailable"
	CodeBrokerDenied       = "broker_denied"
	CodeInternal           = "internal"
	CodeUnavailable        = "unavailable"
	CodeVersionRequired    = "version_required"
)

// Sentinel errors used by clients for branching (errors.Is).
var (
	ErrProtocolMismatch   = errors.New(CodeProtocolMismatch)
	ErrCapabilityMismatch = errors.New(CodeCapabilityMismatch)
	ErrUnauthorized       = errors.New(CodeUnauthorized)
	ErrNotFound           = errors.New(CodeNotFound)
	ErrConflict           = errors.New(CodeConflict)
	ErrInvalidResponse    = errors.New("invalid_response")
	ErrBrokerUnavailable  = errors.New(CodeBrokerUnavailable)
	ErrBrokerDenied       = errors.New(CodeBrokerDenied)
)
