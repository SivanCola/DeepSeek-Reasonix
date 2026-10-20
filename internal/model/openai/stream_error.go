package openai

import "reasonix/internal/base/flexstr"

type streamWireError struct {
	Message string         `json:"message"`
	Code    flexstr.String `json:"code"`
	Type    string         `json:"type"`
	Param   string         `json:"param"`
}
