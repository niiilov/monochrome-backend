// Package llm calls an OpenAI-compatible chat model.
//
// The API key is read from configuration (environment variable LLM_API_KEY).
// It is sent only in the Authorization header and is never written to logs,
// error strings, or the disk cache.
//
// New builds the production stack, from the outside in:
// disk cache (off when llm.cache_dir is empty), meter, retry, limiter, HTTP.
// A cache hit is not metered and is not retried. A failed call is not metered.
// Retries that end in success count as one metered call.
//
// Tests should use the scripted client in package llm/mock instead of HTTP.
package llm
