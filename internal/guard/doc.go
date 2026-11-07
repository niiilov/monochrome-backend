// Package guard wraps untrusted article text and flags prompt-injection signals.
// A finding does not drop the article: callers keep treating the text as data.
package guard
