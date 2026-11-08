package mandatory

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"mono/internal/llm"
)

// scriptClient is the deterministic model used by test-mandatory without --live.
// Answers are keyed by article id so the real runner can satisfy T1-T8.
// It does not open the network and does not read secrets.
type scriptClient struct{}

func newScriptClient() llm.Client {
	return scriptClient{}
}

type factJSON struct {
	Text  string `json:"text"`
	Quote string `json:"quote"`
}

type mentionJSON struct {
	TitleDraft         string     `json:"title_draft"`
	Facts              []factJSON `json:"facts"`
	Affected           string     `json:"affected"`
	Consequences       string     `json:"consequences"`
	Hedges             []string   `json:"hedges"`
	Entities           []string   `json:"entities"`
	IsCompanyStatement bool       `json:"is_company_statement"`
	IsReprint          bool       `json:"is_reprint"`
	Reporter           string     `json:"reporter"`
	ReferencesEarlier  bool       `json:"references_earlier"`
	NewFacts           []string   `json:"new_facts"`
	SufficientText     bool       `json:"sufficient_text"`
}

type card struct {
	title      string
	entity     string
	group      string
	quotes     []string
	scoreQuote string
	earlier    bool
	score      int
}

// Quotes are contiguous slices of the fixture articles.
var cards = map[string]card{
	"t1-sl": {
		title: "Северная гавань", entity: "Северная гавань", group: "gavan", score: 2,
		scoreQuote: "640",
		quotes:     []string{"Компания сообщила, что недоступны 640 контейнерных партий, краны простояли 6 часов, затронуты 3 причала."},
	},
	"t1-xakep": {
		title: "Северная гавань", entity: "Северная гавань", group: "gavan", score: 2,
		scoreQuote: "640",
		quotes:     []string{"Недоступны те же 640 контейнерных партий, простой кранов составил 6 часов, затронуты 3 причала."},
	},
	"t1-kom": {
		title: "Северная гавань", entity: "Северная гавань", group: "gavan", score: 2,
		scoreQuote: "640",
		quotes:     []string{"В заметке снова 640 контейнерных партий, 6 часов простоя и 3 причала."},
	},
	"t2-phish": {
		title: "Ласточка-Банк", entity: "Ласточка-Банк", group: "phish", score: 2,
		scoreQuote: "Ласточка-Банк",
		quotes:     []string{"6 ноября 2026 года с 09:20 до 11:05 сотрудникам «Ласточка-Банк» пришло 210 писем с темой «акт сверки»."},
	},
	"t2-ddos": {
		title: "Городской портал-Н", entity: "Городской портал-Н", group: "ddos", score: 2,
		scoreQuote: "Городской портал-Н",
		quotes:     []string{"Пик составил 90 Гбит/с, простой длился 40 минут."},
	},
	"t2-cve": {
		title: "Склад-Учет", entity: "Склад-Учет", group: "cve", score: 2,
		scoreQuote: "CVE-2026-44110",
		quotes:     []string{"7 ноября 2026 года Positive Technologies выпустила бюллетень по CVE-2026-44110 в продукте «Склад-Учет» версии ниже 3.4.2."},
	},
	"t4-new": {
		title: "Мост-Связь", entity: "Мост-Связь", group: "most", score: 2,
		scoreQuote: "Мост-Связь",
		quotes: []string{
			"Недоступны кабинеты 1 800 корпоративных абонентов.",
			"Злоумышленники потребовали 6,2 млн рублей.",
		},
	},
	"t4-stale": {
		title: "Мост-Связь", entity: "Мост-Связь", group: "most", earlier: true, score: 2,
		scoreQuote: "Мост-Связь",
		quotes:     []string{"7 ноября 2026 года «Газета.Ru» снова написала, что личный кабинет «Мост-Связь» недоступен из-за технического сбоя."},
	},
	"t5-full": {
		title: "Клиника-Север", entity: "Клиника-Север", group: "clinic", score: 2,
		scoreQuote: "Клиника-Север",
		quotes:     []string{"7 ноября 2026 года в 08:15 МСК «Клиника-Север» сообщила об утечке 2 300 карт пациентов."},
	},
	"t6-co": {
		title: "МедАрхив", entity: "МедАрхив", group: "med", score: 2,
		scoreQuote: "МедАрхив",
		quotes: []string{
			"6 ноября 2026 года в 15:40 компания «МедАрхив» сообщила об утечке 15 400 карт пациентов.",
			"Датой события компания называет 5 ноября 2026 года.",
		},
	},
	"t6-news": {
		title: "МедАрхив", entity: "МедАрхив", group: "med", score: 2,
		scoreQuote: "МедАрхив",
		quotes: []string{
			"6 ноября 2026 года «Коммерсантъ» написал об утечке 48 000 карт у «МедАрхив».",
			"Датой события газета называет 3 ноября 2026 года, а не 5 ноября.",
		},
	},
	"t7-ok": {
		title: "Порт-Кама", entity: "Порт-Кама", group: "port", score: 2,
		scoreQuote: "Порт-Кама",
		quotes:     []string{"Охрана насчитала 420 невыданных пропусков и простой 3 часа."},
	},
	"t8-base": {
		title: "Волга-Водоканал", entity: "Волга-Водоканал", group: "volga", score: 3,
		scoreQuote: "Волга-Водоканал",
		quotes:     []string{"Без воды остались 28 000 адресов, подачу обещали вернуть к 21:00."},
	},
	"t8-inject": {
		title: "Регион-Сеть", entity: "Регион-Сеть", group: "region", score: 1,
		scoreQuote: "Регион-Сеть",
		quotes:     []string{"7 ноября 2026 года в 09:40 оператор «Регион-Сеть» подтвердил утечку 900 договоров, подписанных с 1 по 6 ноября."},
	},
}

// Short fixtures must stay on topic so the pipeline can reject them for length.
var shortOnTopic = map[string]bool{
	"t5-title": true,
	"t5-short": true,
}

var (
	articleIDRE = regexp.MustCompile(`article id="([^"]+)"`)
	sideIDRE    = regexp.MustCompile(`ID статьи:\s*(\S+)`)
	freshIDRE   = regexp.MustCompile(`Свежая статья:\s*(\S+)`)
	postHeadRE  = regexp.MustCompile(`^(\d+)\. (.+)$`)
)

func (scriptClient) Complete(ctx context.Context, r llm.Request) (llm.Response, error) {
	if err := ctx.Err(); err != nil {
		return llm.Response{}, err
	}
	prompt := r.System + "\n" + r.User
	var text string
	switch r.Task {
	case "classify_topic":
		text = classifyReply(prompt)
	case "extract_events":
		text = extractReply(prompt)
	case "cluster_confirm":
		text = confirmReply(prompt)
	case "update_check":
		text = updateReply(prompt)
	case "score_event":
		text = scoreReply(prompt)
	case "rationale":
		text = `{"rationale":""}`
	case "write_digest_item":
		text = itemReply(prompt)
	case "write_post":
		text = postReply(prompt)
	default:
		return llm.Response{}, errNoTask(r.Task)
	}
	return llm.Response{
		Text:  text,
		Model: "mock",
		Usage: llm.Usage{InputTokens: 24, OutputTokens: 16},
	}, nil
}

func errNoTask(task string) error {
	return &taskError{task: task}
}

type taskError struct{ task string }

func (e *taskError) Error() string {
	return "mock: нет ответа для задачи " + e.task
}

func classifyReply(prompt string) string {
	on := topicOn(firstArticleID(prompt))
	reason := "есть признаки инцидента"
	if !on {
		reason = "нет признаков инцидента"
	}
	return mustJSON(map[string]any{
		"is_on_topic":    on,
		"is_advertising": false,
		"confidence":     0.9,
		"reason":         reason,
	})
}

func topicOn(id string) bool {
	if shortOnTopic[id] {
		return true
	}
	_, ok := cards[id]
	return ok
}

func extractReply(prompt string) string {
	id := firstArticleID(prompt)
	card, ok := cards[id]
	if !ok {
		return mustJSON(map[string]any{"mentions": []any{}})
	}
	facts := make([]factJSON, len(card.quotes))
	for i, quote := range card.quotes {
		facts[i] = factJSON{Text: quote, Quote: quote}
	}
	entities := []string{}
	if card.entity != "" {
		entities = []string{card.entity}
	}
	mention := mentionJSON{
		TitleDraft:        card.title,
		Facts:             facts,
		Affected:          "",
		Consequences:      "",
		Hedges:            []string{},
		Entities:          entities,
		Reporter:          "",
		ReferencesEarlier: card.earlier,
		NewFacts:          []string{},
		SufficientText:    true,
	}
	return mustJSON(map[string]any{"mentions": []mentionJSON{mention}})
}

func confirmReply(prompt string) string {
	ids := sideIDs(prompt)
	same := false
	if len(ids) >= 2 {
		left, lok := cards[ids[0]]
		right, rok := cards[ids[1]]
		same = lok && rok && left.group != "" && left.group == right.group
	}
	reason := "разные события"
	if same {
		reason = "одно событие"
	}
	return mustJSON(map[string]any{
		"same_event":      same,
		"shared_entities": []string{},
		"reason":          reason,
		"conflicts":       []any{},
	})
}

func updateReply(prompt string) string {
	id := ""
	if m := freshIDRE.FindStringSubmatch(prompt); len(m) == 2 {
		id = m[1]
	}
	facts := []string{"есть новые сведения"}
	switch id {
	case "t4-stale":
		facts = []string{}
	case "t4-new":
		facts = []string{"1 800 корпоративных абонентов"}
	}
	return mustJSON(map[string]any{"new_facts": facts})
}

func scoreReply(prompt string) string {
	ids := articleIDs(prompt)
	value := 2
	quote := ""
	for _, id := range ids {
		card, ok := cards[id]
		if !ok {
			continue
		}
		if quote == "" {
			quote = card.scoreQuote
		}
		if card.score == 3 {
			value = 3
		} else if card.score == 1 && value != 3 {
			value = 1
		}
	}
	type criterion struct {
		Value *int   `json:"value"`
		Quote string `json:"quote"`
		Note  string `json:"note"`
	}
	var mark criterion
	if quote == "" {
		mark = criterion{Note: "нет опоры в тексте"}
	} else {
		v := value
		mark = criterion{Value: &v, Quote: quote, Note: "по тексту источника"}
	}
	return mustJSON(map[string]any{
		"impact":    mark,
		"scale":     mark,
		"urgency":   mark,
		"relevance": mark,
	})
}

func itemReply(prompt string) string {
	ids := articleIDs(prompt)
	title := "Событие"
	var parts []string
	seen := map[string]bool{}
	hasCo, hasNews := false, false
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		card, ok := cards[id]
		if !ok {
			continue
		}
		if title == "Событие" && card.title != "" {
			title = card.title
		}
		parts = append(parts, card.quotes...)
		if id == "t6-co" {
			hasCo = true
		}
		if id == "t6-news" {
			hasNews = true
		}
	}
	if hasCo && hasNews {
		parts = append(parts, "В сообщениях есть расхождение в числе и в дате.")
	}
	essence := strings.Join(parts, " ")
	if strings.TrimSpace(essence) == "" {
		essence = "Источник описывает событие."
	}
	return mustJSON(map[string]any{
		"title":        title,
		"essence":      essence,
		"consequences": "",
		"hedge_note":   "",
	})
}

func postReply(prompt string) string {
	type item struct {
		N     int    `json:"n"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	var items []item
	lines := strings.Split(prompt, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		m := postHeadRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[i+1]), "Суть:") {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		got := item{N: n, Title: strings.TrimSpace(m[2])}
		for _, extra := range lines[i+1:] {
			extra = strings.TrimSpace(extra)
			switch {
			case strings.HasPrefix(extra, "Суть:"):
				got.Text = joinText(got.Text, strings.TrimPrefix(extra, "Суть:"))
			case strings.HasPrefix(extra, "Последствия:"):
				got.Text = joinText(got.Text, strings.TrimPrefix(extra, "Последствия:"))
			case strings.HasPrefix(extra, "Обоснование:"):
				got.Text = joinText(got.Text, strings.TrimPrefix(extra, "Обоснование:"))
			case strings.HasPrefix(extra, "Ссылки"):
				items = append(items, got)
				goto next
			case postHeadRE.MatchString(extra):
				items = append(items, got)
				goto next
			}
		}
		items = append(items, got)
	next:
	}
	if items == nil {
		items = []item{}
	}
	return mustJSON(map[string]any{"items": items})
}

func joinText(left, right string) string {
	right = strings.TrimSpace(right)
	if right == "" {
		return left
	}
	if left == "" {
		return right
	}
	return left + " " + right
}

func firstArticleID(prompt string) string {
	ids := articleIDs(prompt)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func articleIDs(prompt string) []string {
	found := articleIDRE.FindAllStringSubmatch(prompt, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		if len(m) == 2 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

func sideIDs(prompt string) []string {
	found := sideIDRE.FindAllStringSubmatch(prompt, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		if len(m) == 2 {
			out = append(out, m[1])
		}
	}
	return out
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return `{}`
	}
	return string(raw)
}
