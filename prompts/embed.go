// Package prompts embeds the prompt template files next to this file.
// The embed directive cannot refer to parent directories, so the templates
// live here and internal/prompts loads FS.
package prompts

import "embed"

// FS contains every *.tmpl file in this directory.
//
//go:embed *.tmpl
var FS embed.FS
