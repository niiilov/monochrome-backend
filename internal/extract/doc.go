// Package extract pulls event mentions out of one article.
// Quotes are kept only when they are substrings of the article after space normalization.
// The model cannot mark a short text as sufficient: EnoughText has the last word.
package extract
