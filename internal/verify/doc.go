// Package verify is the deterministic gate before a digest is published.
// It checks facts, limits, format and cross-artifact consistency, retries
// broken events, then falls back to a template retelling of Fact.Text.
package verify
