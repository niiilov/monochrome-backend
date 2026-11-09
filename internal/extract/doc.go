// Package extract pulls event mentions out of one article.
// Quotes are kept only when they are substrings of the article after space normalization.
// Text below the soft length threshold receives an additional semantic review.
// Every accepted mention requires an event and a verified verbatim fact quote.
package extract
