package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxToolResultImageBytes = 4 << 20
	maxToolResultImages     = 5
)

var toolResultImageMimes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func parseToolResult(res json.RawMessage) (string, []string, error) {
	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
		StructuredError   bool            `json:"structuredContentError,omitempty"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", nil, fmt.Errorf("decode tool result: %w", err)
	}
	var sb strings.Builder
	var images []string
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			sb.WriteString(c.Text)
		case "image":
			placeholder, url := toolResultImage(c.MimeType, c.Data, len(images))
			sb.WriteString(placeholder)
			if url != "" {
				images = append(images, url)
			}
		}
	}
	text := mergeStructuredContent(sb.String(), out.StructuredContent)
	if out.IsError {
		return text, images, fmt.Errorf("plugin tool reported error: %s", text)
	}
	return text, images, nil
}

func mergeStructuredContent(text string, structured json.RawMessage) string {
	structured = bytes.TrimSpace(structured)
	if len(structured) == 0 || bytes.Equal(structured, []byte("null")) {
		return text
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return string(structured)
	}
	if jsonEqual(trimmed, structured) {
		return trimmed
	}
	return trimmed + "\n\nstructuredContent:\n" + string(structured)
}

func jsonEqual(text string, structured json.RawMessage) bool {
	var left, right any
	if json.Unmarshal([]byte(text), &left) != nil {
		return false
	}
	if json.Unmarshal(structured, &right) != nil {
		return false
	}
	lb, err1 := json.Marshal(left)
	rb, err2 := json.Marshal(right)
	return err1 == nil && err2 == nil && bytes.Equal(lb, rb)
}

func toolResultImage(mime, data string, kept int) (placeholder, url string) {
	if kept >= maxToolResultImages {
		return "[image omitted: per-result image limit reached]", ""
	}
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "" {
		mime = "image/png"
	}
	if !toolResultImageMimes[mime] {
		return "[image omitted: unsupported type " + mime + "]", ""
	}
	data = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		}
		return r
	}, data)
	if data == "" {
		return "[image omitted: no data]", ""
	}
	if len(data) > maxToolResultImageBytes {
		return fmt.Sprintf("[image omitted: %d bytes exceeds the %d-byte limit]", len(data), maxToolResultImageBytes), ""
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return "[image omitted: invalid base64]", ""
	}
	return "[image: " + mime + "]", "data:" + mime + ";base64," + data
}
