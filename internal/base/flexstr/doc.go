// Package flexstr reads a JSON field whose type the producer decides.
//
// Compatible servers disagree about the type of a small text field — an error
// `code` is `"invalid_request"` from OpenAI and DeepSeek, and `400` from a
// gateway in front of them. Insisting on one type fails the whole message, and
// the person sees a decode error instead of the reason their request was
// refused. String is the tolerant reading: a JSON string is taken as itself, a
// number or other scalar is taken as its text, and null or an absent field is
// the empty string.
//
// It is a leaf on purpose: every model package needs it, and a shared type that
// imports the models back would be an import cycle waiting to happen.
package flexstr
