package topic

// MatchesKeywords reports whether any keyword occurs in the title or the text.
// Latin words and abbreviations match as whole tokens. Russian words match by
// stem, so «утечка» finds «утечки». A phrase must occur as consecutive tokens.
// Matching is case-insensitive. Empty keywords never match.
func MatchesKeywords(title, text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	titleTokens := tokenize(title)
	textTokens := tokenize(text)
	for _, keyword := range keywords {
		phrase := compile(keyword)
		if len(phrase) == 0 {
			continue
		}
		if findPhrase(titleTokens, phrase) >= 0 || findPhrase(textTokens, phrase) >= 0 {
			return true
		}
	}
	return false
}
