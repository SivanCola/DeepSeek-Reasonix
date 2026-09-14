package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// SVG sanitizing for every surface that shows model-authored markup: remote
// Markdown images and chat code blocks. The renderer only ever receives the
// sanitized bytes as an <img> source, never as markup it injects into the app
// DOM.
//
// The pass is an allowlist by exclusion: a strict XML parse that must see a
// single `svg` root, with scripting, embedded documents, animation, external
// references and event attributes dropped. Document-internal references
// (`#id`, embedded raster data) survive so gradients, clip paths and text
// keep working.

// MarkdownSVGView is the renderer-safe result of sanitizing a chat code block.
// SVG is the sanitized markup; the caller turns it into an image source.
type MarkdownSVGView struct {
	OK     bool   `json:"ok"`
	SVG    string `json:"svg,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// The automatic preview ceiling keeps one pasted diagram from stalling the
// transcript. Past it the code block stays source with a short explanation.
const (
	markdownSVGPreviewMaxBytes    = 1 << 20
	markdownSVGPreviewMaxElements = 10000
	markdownSVGPreviewMaxDepth    = 128
)

// The SVG document namespace, and the attribute namespace `xmlns` itself is
// reported under by encoding/xml.
const markdownSVGNamespace = "http://www.w3.org/2000/svg"

func isNamespaceDeclaration(name xml.Name) bool {
	return name.Space == "xmlns" || (name.Space == "" && name.Local == "xmlns")
}

type svgSanitizeLimits struct {
	maxBytes    int
	maxElements int
	maxDepth    int
}

// SanitizeMarkdownSVG converts a model-authored SVG document into renderer-safe
// markup. It performs no file read and no network access; the content is the
// only input.
func (a *App) SanitizeMarkdownSVG(content string) MarkdownSVGView {
	sanitized, ok := sanitizeMarkdownSVG([]byte(content), svgSanitizeLimits{
		maxBytes:    markdownSVGPreviewMaxBytes,
		maxElements: markdownSVGPreviewMaxElements,
		maxDepth:    markdownSVGPreviewMaxDepth,
	})
	if !ok {
		return MarkdownSVGView{Reason: markdownSVGFailureReason(len(content))}
	}
	return MarkdownSVGView{OK: true, SVG: string(sanitized)}
}

// markdownSVGFailureReason distinguishes the one refusal the reader can act on
// (a document too large to preview) from malformed markup.
func markdownSVGFailureReason(size int) string {
	if size > markdownSVGPreviewMaxBytes {
		return "too-large"
	}
	return "invalid"
}

var markdownSVGForbiddenElements = map[string]bool{
	"animate":          true,
	"animatemotion":    true,
	"animatetransform": true,
	"audio":            true,
	"embed":            true,
	"foreignobject":    true,
	"iframe":           true,
	"object":           true,
	"script":           true,
	"set":              true,
	"style":            true,
	"video":            true,
}

func sanitizeMarkdownSVG(body []byte, limits svgSanitizeLimits) ([]byte, bool) {
	if limits.maxBytes > 0 && len(body) > limits.maxBytes {
		return nil, false
	}
	trimmed := bytes.TrimSpace(body)
	trimmed = bytes.TrimPrefix(trimmed, []byte{0xef, 0xbb, 0xbf})
	trimmed = bytes.TrimSpace(trimmed)
	if len(trimmed) == 0 {
		return nil, false
	}

	decoder := xml.NewDecoder(bytes.NewReader(trimmed))
	decoder.Strict = true
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	rootSeen := false
	rootDepth := 0
	skipDepth := 0
	elements := 0

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		switch value := token.(type) {
		case xml.StartElement:
			// Every element counts, including one dropped with its subtree: the
			// cost being bounded is parsing the document the model emitted.
			elements++
			if limits.maxElements > 0 && elements > limits.maxElements {
				return nil, false
			}
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			name := strings.ToLower(value.Name.Local)
			if !rootSeen {
				if name != "svg" || (value.Name.Space != "" && value.Name.Space != markdownSVGNamespace) {
					return nil, false
				}
				rootSeen = true
			} else if rootDepth == 0 {
				return nil, false
			}
			if markdownSVGForbiddenElements[name] {
				skipDepth = 1
				continue
			}
			attrs := value.Attr[:0]
			for _, attr := range value.Attr {
				// The encoder writes the element's namespace itself, so an
				// explicit declaration would come out twice and make the whole
				// document a parse error for the renderer.
				if isNamespaceDeclaration(attr.Name) {
					continue
				}
				attrName := strings.ToLower(attr.Name.Local)
				if strings.HasPrefix(attrName, "on") || attrName == "srcset" ||
					(attr.Name.Space == "http://www.w3.org/XML/1998/namespace" && attrName == "base") {
					continue
				}
				if attrName == "href" || attrName == "src" {
					if !safeMarkdownSVGReference(attr.Value) {
						continue
					}
				} else if !safeMarkdownSVGAttributeValue(attr.Value) {
					continue
				}
				attrs = append(attrs, attr)
			}
			value.Attr = attrs
			// The root always declares the SVG namespace: a source that omitted
			// xmlns would otherwise not be parsed as SVG at all.
			if rootDepth == 0 {
				value.Name.Space = markdownSVGNamespace
			} else if value.Name.Space == markdownSVGNamespace {
				// Children inherit the root's default namespace; re-declaring it
				// on every element is noise, not information.
				value.Name.Space = ""
			}
			rootDepth++
			if limits.maxDepth > 0 && rootDepth > limits.maxDepth {
				return nil, false
			}
			if err := encoder.EncodeToken(value); err != nil {
				return nil, false
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if rootDepth <= 0 {
				return nil, false
			}
			// The end tag must name the same element the start tag did: the
			// encoder rejects a mismatch, so it follows the namespace rewrite
			// applied to the start tag above.
			if rootDepth == 1 {
				value.Name.Space = markdownSVGNamespace
			} else if value.Name.Space == markdownSVGNamespace {
				value.Name.Space = ""
			}
			if err := encoder.EncodeToken(value); err != nil {
				return nil, false
			}
			rootDepth--
		case xml.CharData:
			if skipDepth == 0 && (!rootSeen || rootDepth == 0) {
				if len(bytes.TrimSpace(value)) != 0 {
					return nil, false
				}
				continue
			}
			if skipDepth == 0 {
				if err := encoder.EncodeToken(value); err != nil {
					return nil, false
				}
			}
		case xml.Comment:
			// Comments are not needed for display and can hide suspicious payloads.
		case xml.Directive, xml.ProcInst:
			// Drop DTDs and processing instructions; SVG does not need them here.
		default:
			if skipDepth == 0 {
				if err := encoder.EncodeToken(value); err != nil {
					return nil, false
				}
			}
		}
	}
	if !rootSeen || rootDepth != 0 || skipDepth != 0 || encoder.Flush() != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func safeMarkdownSVGReference(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(value, "#") {
		return true
	}
	for _, prefix := range []string{
		"data:image/png;base64,",
		"data:image/jpeg;base64,",
		"data:image/gif;base64,",
		"data:image/webp;base64,",
		"data:image/bmp;base64,",
		"data:image/x-icon;base64,",
	} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func safeMarkdownSVGAttributeValue(raw string) bool {
	value := strings.ToLower(raw)
	if strings.Contains(value, "javascript:") || strings.Contains(value, "vbscript:") || strings.Contains(value, "data:text/html") {
		return false
	}
	for {
		index := strings.Index(value, "url(")
		if index < 0 {
			return !strings.Contains(value, "@import") && !strings.Contains(value, "expression(")
		}
		value = value[index+4:]
		end := strings.IndexByte(value, ')')
		if end < 0 {
			return false
		}
		target := strings.Trim(strings.TrimSpace(value[:end]), "\"'")
		if !strings.HasPrefix(target, "#") {
			return false
		}
		value = value[end+1:]
	}
}
