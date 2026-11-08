package topic

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	"mono/internal/domain"
)

// Match is one dictionary hit. Weak terms do not prove the topic by themselves.
type Match struct {
	Term  string
	Group string
	Weak  bool
}

const (
	groupGeneral = "общая тематика"
	groupAttack  = "атаки и вредоносное ПО"
	groupVuln    = "уязвимости и компрометация"
	groupData    = "данные и доступ"
	groupDefense = "средства и процессы защиты"
	groupReg     = "регулирование и инфраструктура"
	groupAI      = "безопасность ИИ"
)

// ruEndings are inflection suffixes, longest first. A stem of at least 3 runes stays.
var ruEndings = []string{
	"иями", "ями", "ами",
	"ого", "ему", "ому", "ыми", "ими",
	"ая", "яя", "ое", "ее", "ые", "ие", "ой", "ей", "ий", "ый", "ую", "юю", "ою", "ею",
	"ых", "их", "ым", "им",
	"ах", "ях", "ов", "ев", "ом", "ем", "ам", "ям",
	"ия", "ие", "ии", "ию",
	"а", "я", "ы", "и", "е", "у", "ю", "о", "ь",
}

type tok struct {
	exact string
	stem  string
}

type entry struct {
	name    string
	group   string
	weak    bool
	phrases [][]tok
}

var (
	dictOnce sync.Once
	dict     []entry
)

func entries() []entry {
	dictOnce.Do(func() {
		sort.SliceStable(ruEndings, func(i, j int) bool {
			return len([]rune(ruEndings[i])) > len([]rune(ruEndings[j]))
		})
		dict = buildDictionary()
	})
	return dict
}

// Prefilter returns dictionary terms found in the title or the text.
// Order follows the first hit, title before text. Each term appears once.
func Prefilter(a domain.Article) []string {
	hits := Scan(a.Title, a.Text)
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, len(hits))
	for i, hit := range hits {
		out[i] = hit.Term
	}
	return out
}

// Scan finds dictionary hits. A phrase cannot start in the title and finish in the text.
func Scan(title, text string) []Match {
	var out []Match
	seen := map[string]bool{}
	for _, hit := range matchText(title) {
		if seen[hit.Term] {
			continue
		}
		seen[hit.Term] = true
		out = append(out, hit)
	}
	for _, hit := range matchText(text) {
		if seen[hit.Term] {
			continue
		}
		seen[hit.Term] = true
		out = append(out, hit)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HasStrong reports whether any hit is not weak.
func HasStrong(hits []Match) bool {
	for _, hit := range hits {
		if !hit.Weak {
			return true
		}
	}
	return false
}

func matchText(s string) []Match {
	ts := tokenize(s)
	if len(ts) == 0 {
		return nil
	}
	type found struct {
		hit Match
		at  int
	}
	var founds []found
	for i, e := range entries() {
		at := -1
		for _, phrase := range e.phrases {
			pos := findPhrase(ts, phrase)
			if pos >= 0 && (at < 0 || pos < at) {
				at = pos
			}
		}
		if at < 0 {
			continue
		}
		founds = append(founds, found{
			hit: Match{Term: e.name, Group: e.group, Weak: e.weak},
			at:  at*1000 + i,
		})
	}
	sort.SliceStable(founds, func(i, j int) bool { return founds[i].at < founds[j].at })
	out := make([]Match, len(founds))
	for i, f := range founds {
		out[i] = f.hit
	}
	return out
}

func findPhrase(ts []string, phrase []tok) int {
	if len(phrase) == 0 || len(phrase) > len(ts) {
		return -1
	}
	for i := 0; i+len(phrase) <= len(ts); i++ {
		ok := true
		for j, want := range phrase {
			if !tokenMatch(ts[i+j], want) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func tokenMatch(got string, want tok) bool {
	if want.exact != "" {
		return got == want.exact
	}
	return stem(got) == want.stem
}

func tokenize(s string) []string {
	s = fold(s)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func fold(s string) string {
	s = strings.ToLower(s)
	return strings.ReplaceAll(s, "ё", "е")
}

func stem(word string) string {
	for {
		cut := ""
		for _, ending := range ruEndings {
			if !strings.HasSuffix(word, ending) {
				continue
			}
			if len([]rune(word))-len([]rune(ending)) < 3 {
				continue
			}
			cut = ending
			break
		}
		if cut == "" {
			return word
		}
		word = strings.TrimSuffix(word, cut)
	}
}

// compile splits a phrase into tokens.
// A leading "!" on a word forces an exact match (abbreviations).
// Hyphens are word breaks. Latin words are exact. Russian words are stemmed.
func compile(phrase string) []tok {
	phrase = strings.ReplaceAll(fold(phrase), "-", " ")
	fields := strings.Fields(phrase)
	out := make([]tok, 0, len(fields))
	for _, word := range fields {
		exact := false
		if strings.HasPrefix(word, "!") {
			exact = true
			word = strings.TrimPrefix(word, "!")
		}
		if word == "" {
			continue
		}
		if exact || !hasCyrillic(word) {
			out = append(out, tok{exact: word})
			continue
		}
		out = append(out, tok{stem: stem(word)})
	}
	return out
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func add(list *[]entry, group, name string, weak bool, phrases ...string) {
	e := entry{name: name, group: group, weak: weak}
	for _, phrase := range phrases {
		compiled := compile(phrase)
		if len(compiled) == 0 {
			continue
		}
		e.phrases = append(e.phrases, compiled)
	}
	*list = append(*list, e)
}

func buildDictionary() []entry {
	var list []entry
	g := groupGeneral
	add(&list, g, "кибербезопасность", false, "кибербезопасность")
	add(&list, g, "информационная безопасность", false, "информационная безопасность")
	add(&list, g, "ИБ", false, "!иб")
	add(&list, g, "защита информации и данных", false, "защита информации", "защита данных")
	add(&list, g, "киберугроза", false, "киберугроза", "угроза")
	add(&list, g, "кибератака", false, "кибератака", "атака")
	add(&list, g, "киберинцидент", false, "киберинцидент")
	add(&list, g, "компьютерный инцидент", false, "компьютерный инцидент", "инцидент")
	add(&list, g, "цифровая безопасность", false, "цифровая безопасность")
	add(&list, g, "безопасность ИТ", false, "безопасность !ит")
	add(&list, g, "киберустойчивость", false, "киберустойчивость")
	add(&list, g, "цифровая устойчивость", false, "цифровая устойчивость")
	add(&list, g, "киберзащита", false, "киберзащита")
	add(&list, g, "безопасность инфраструктуры", false, "безопасность инфраструктуры")

	g = groupAttack
	add(&list, g, "фишинг", false, "фишинг", "phishing")
	add(&list, g, "spear phishing", false, "spear phishing")
	add(&list, g, "спуфинг", false, "спуфинг", "spoofing", "поддельный сайт")
	add(&list, g, "поддельное письмо", false, "поддельное письмо")
	add(&list, g, "социальная инженерия", false, "социальная инженерия")
	add(&list, g, "DDoS", false, "ddos")
	add(&list, g, "ботнет", false, "ботнет")
	add(&list, g, "ransomware", false, "ransomware")
	add(&list, g, "вирус-шифровальщик", false, "вирус-шифровальщик", "шифровальщик", "шифровальный")
	add(&list, g, "вымогатель", false, "вымогатель", "выкуп")
	add(&list, g, "malware", false, "malware", "вредонос", "антивирус")
	add(&list, g, "троян", false, "троян", "trojan")
	add(&list, g, "бэкдор", false, "бэкдор", "backdoor")
	add(&list, g, "кейлоггер", false, "кейлоггер", "keylogger")
	add(&list, g, "стилер", false, "стилер", "stealer", "скиммер")
	add(&list, g, "криптомайнер", false, "криптомайнер", "cryptominer")
	add(&list, g, "APT", false, "apt")
	add(&list, g, "целевая атака", false, "целевая атака")
	add(&list, g, "supply chain attack", false, "supply chain attack")
	add(&list, g, "атака на цепочку поставок", false, "атака на цепочку поставок")
	add(&list, g, "атака на RDP", false, "атака на rdp")
	add(&list, g, "атака на VPN", false, "атака на vpn")
	add(&list, g, "RDP", false, "rdp")
	add(&list, g, "VPN", false, "vpn")

	g = groupVuln
	add(&list, g, "взлом", false, "взлом")
	add(&list, g, "компрометация учётной записи", false, "компрометация учётной записи")
	add(&list, g, "подбор пароля", false, "подбор пароля")
	add(&list, g, "brute force", false, "brute force")
	add(&list, g, "credential stuffing", false, "credential stuffing")
	add(&list, g, "обход аутентификации", false, "обход аутентификации")
	add(&list, g, "повышение привилегий", false, "повышение привилегий")
	add(&list, g, "RCE", false, "rce")
	add(&list, g, "удалённое выполнение кода", false, "удалённое выполнение кода")
	add(&list, g, "zero-day", false, "zero-day")
	add(&list, g, "0-day", false, "0-day")
	add(&list, g, "exploit", false, "exploit")
	add(&list, g, "эксплойт", false, "эксплойт")
	add(&list, g, "критическая уязвимость", false, "критическая уязвимость", "уязвимость")

	g = groupData
	add(&list, g, "утечка данных", false, "утечка данных", "утечка информации", "утечка")
	add(&list, g, "слив данных", false, "слив данных")
	add(&list, g, "скомпрометированные данные", false, "скомпрометированные данные")
	add(&list, g, "ПДн", false, "!пдн")
	add(&list, g, "персональные данные", false, "персональные данные")
	add(&list, g, "биометрия", false, "биометрия")
	add(&list, g, "утечка базы", false, "утечка базы")
	add(&list, g, "учётные данные", false, "учётные данные")
	add(&list, g, "аутентификация", false, "аутентификация", "authentication")
	add(&list, g, "MFA", false, "mfa")
	add(&list, g, "двухфакторная аутентификация", false, "двухфакторная аутентификация")
	add(&list, g, "IAM", false, "iam")
	add(&list, g, "PAM", false, "pam")
	add(&list, g, "контроль доступа", false, "контроль доступа")
	add(&list, g, "привилегированный доступ", false, "привилегированный доступ")
	add(&list, g, "identity", false, "identity")
	add(&list, g, "пароль", true, "пароль", "password")
	add(&list, g, "токен", true, "токен", "token")
	add(&list, g, "сертификат", true, "сертификат", "certificate")
	add(&list, g, "база данных", true, "база данных")
	add(&list, g, "учётная запись", true, "учётная запись")

	g = groupDefense
	add(&list, g, "SOC", false, "soc")
	add(&list, g, "SOC-as-a-Service", false, "soc-as-a-service")
	add(&list, g, "MSSP", false, "mssp")
	add(&list, g, "MDR", false, "mdr")
	add(&list, g, "XDR", false, "xdr")
	add(&list, g, "EDR", false, "edr")
	add(&list, g, "SIEM", false, "siem")
	add(&list, g, "SOAR", false, "soar")
	add(&list, g, "NGFW", false, "ngfw")
	add(&list, g, "firewall", false, "firewall")
	add(&list, g, "межсетевой экран", false, "межсетевой экран")
	add(&list, g, "WAF", false, "waf")
	add(&list, g, "IDS", false, "ids")
	add(&list, g, "IPS", false, "ips")
	add(&list, g, "DLP", false, "dlp")
	add(&list, g, "NAC", false, "nac")
	add(&list, g, "sandbox", false, "sandbox")
	add(&list, g, "anti-DDoS", false, "anti-ddos", "анти-ddos")
	add(&list, g, "SASE", false, "sase")
	add(&list, g, "ZTNA", false, "ztna")
	add(&list, g, "Zero Trust", false, "zero trust")
	add(&list, g, "DevSecOps", false, "devsecops")
	add(&list, g, "Application Security", false, "application security")
	add(&list, g, "AppSec", false, "appsec")
	add(&list, g, "защита приложений", false, "защита приложений")
	add(&list, g, "backup", true, "backup")
	add(&list, g, "резервное копирование", false, "резервное копирование")
	add(&list, g, "disaster recovery", false, "disaster recovery")
	add(&list, g, "threat intelligence", false, "threat intelligence")
	add(&list, g, "киберразведка", false, "киберразведка")
	add(&list, g, "мониторинг ИБ", false, "мониторинг !иб")
	add(&list, g, "реагирование на инциденты", false, "реагирование на инциденты")

	g = groupReg
	add(&list, g, "ФСТЭК", true, "!фстэк")
	add(&list, g, "ФСБ", true, "!фсб")
	add(&list, g, "Роскомнадзор", true, "роскомнадзор")
	add(&list, g, "Минцифры", true, "минцифры")
	add(&list, g, "НКЦКИ", true, "!нкцки")
	add(&list, g, "ГосСОПКА", true, "госсопка")
	add(&list, g, "КИИ", false, "!кии")
	add(&list, g, "критическая информационная инфраструктура", false, "критическая информационная инфраструктура")
	add(&list, g, "ГИС", false, "!гис")
	add(&list, g, "государственная информационная система", false, "государственная информационная система")
	add(&list, g, "локализация данных", false, "локализация данных")
	add(&list, g, "доверенный ПАК", false, "доверенный !пак")
	add(&list, g, "реестровое ПО", false, "реестровое !по")
	add(&list, g, "импортозамещение", false, "импортозамещение")
	add(&list, g, "сертификация СЗИ", false, "сертификация !сзи")
	add(&list, g, "аттестация", false, "аттестация")
	add(&list, g, "соответствие требованиям", false, "соответствие требованиям")
	add(&list, g, "проверка защищённости", false, "проверка защищённости")
	add(&list, g, "категорирование", false, "категорирование")
	add(&list, g, "субъект КИИ", false, "субъект !кии")
	add(&list, g, "приказ ФСТЭК", false, "приказ !фстэк")
	add(&list, g, "требования регулятора", false, "требования регулятора")
	add(&list, g, "защита ГИС и АСУ ТП", false, "защита !гис", "защита !асу !тп", "!асу !тп")

	g = groupAI
	add(&list, g, "AI security", false, "ai security")
	add(&list, g, "LLM security", false, "llm security")
	add(&list, g, "доверенный ИИ", false, "доверенный !ии")
	add(&list, g, "ИИ-агент", false, "!ии-агент")
	add(&list, g, "AI agent", false, "ai agent")
	add(&list, g, "дипфейк", false, "дипфейк", "deepfake")
	add(&list, g, "prompt injection", false, "prompt injection")
	add(&list, g, "data poisoning", false, "data poisoning")
	add(&list, g, "model leakage", false, "model leakage")
	add(&list, g, "утечка модели", false, "утечка модели")
	add(&list, g, "защита модели", false, "защита модели")
	add(&list, g, "защита ИИ-сервисов", false, "защита !ии сервисов")
	add(&list, g, "AI governance", false, "ai governance")
	add(&list, g, "генеративный ИИ", false, "генеративный !ии")
	add(&list, g, "риски ИИ", false, "риски !ии")
	return list
}
