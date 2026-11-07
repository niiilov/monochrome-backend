package verify

import "strings"

// StopPhrases are canned openings the digest must not use.
// The match is case-insensitive and looks for the whole phrase.
var StopPhrases = []string{
	"стоит отметить",
	"важно понимать",
	"нельзя не отметить",
	"следует отметить",
	"необходимо отметить",
	"хочется отметить",
	"не секрет",
	"в современном мире",
	"как известно",
	"играет важную роль",
	"имеет большое значение",
	"крайне важно",
	"вне всякого сомнения",
	"уникальная возможность",
	"беспрецедентн",
	"революционн",
}

func stopPhrase(text string) string {
	low := strings.ToLower(text)
	for _, phrase := range StopPhrases {
		if strings.Contains(low, phrase) {
			return phrase
		}
	}
	return ""
}
