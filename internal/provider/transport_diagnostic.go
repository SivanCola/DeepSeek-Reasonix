package provider

import (
	"errors"
	"regexp"
)

// net/http's HTTP/2 errors are private types, while x/net/http2 exposes its
// equivalents. Match their narrow wire-error forms after unwrapping instead of
// treating every url.Error (including invalid URLs and headers) as a network reset.
var http2TransportError = regexp.MustCompile(`^(?:connection error: |stream error: stream ID [0-9]+; |http2: connection error: |http2: server sent GOAWAY and closed the connection; LastStreamID=[0-9]+, ErrCode=)(PROTOCOL_ERROR|INTERNAL_ERROR|FLOW_CONTROL_ERROR|SETTINGS_TIMEOUT|STREAM_CLOSED|FRAME_SIZE_ERROR|REFUSED_STREAM|CANCEL|COMPRESSION_ERROR|CONNECT_ERROR|ENHANCE_YOUR_CALM|INADEQUATE_SECURITY|HTTP_1_1_REQUIRED)(?:$|[;: ,])`)

// HTTP2TransportCode reports transport evidence only. It does not authorize
// retries or classify provider response bodies as connection failures.
func HTTP2TransportCode(err error) string {
	var response *APIError
	if errors.As(err, &response) {
		return ""
	}
	for err != nil {
		if match := http2TransportError.FindStringSubmatch(err.Error()); len(match) > 1 {
			return match[1]
		}
		err = errors.Unwrap(err)
	}
	return ""
}
