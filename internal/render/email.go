package render

import (
	"html/template"
	"strings"

	"mono/internal/domain"
)

// Subject is the email subject with the Moscow period.
func Subject(m domain.DigestModel) string {
	return "Дайджест по кибербезопасности: " + periodPhrase(m.Period)
}

// EmailText is the plain-text letter: subject and the full digest.
func EmailText(m domain.DigestModel) string {
	return "Тема: " + Subject(m) + "\n\n" + Digest(m)
}

// EmailHTML is the HTML letter built with html/template from the same model.
func EmailHTML(m domain.DigestModel) string {
	prepared := prepare(m)
	data := emailData{
		Subject: Subject(prepared),
		Period:  periodPhrase(prepared.Period),
		Control: controlPhrase(prepared),
		Count:   countLine(prepared),
		Empty:   len(prepared.Items) == 0,
	}
	if data.Empty {
		data.EmptyText = emptyMessage(prepared.Shortfall)
	}
	for _, item := range prepared.Items {
		view := emailItem{
			N:            item.N,
			Title:        item.Title,
			Essence:      item.Essence,
			Consequences: item.Consequences,
			Rationale:    item.Rationale,
		}
		for _, source := range item.Sources {
			line := formatSource(source)
			view.Sources = append(view.Sources, emailSource{Line: line, URL: source.URL})
		}
		if len(view.Sources) == 0 {
			view.Sources = []emailSource{{Line: noSources}}
		}
		data.Items = append(data.Items, view)
	}
	var b strings.Builder
	if err := emailTpl.Execute(&b, data); err != nil {
		return "<!DOCTYPE html><html lang=\"ru\"><body><p>" + template.HTMLEscapeString(data.Subject) + "</p><p>" + template.HTMLEscapeString(data.Count) + "</p></body></html>\n"
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

type emailData struct {
	Subject   string
	Period    string
	Control   string
	Count     string
	Empty     bool
	EmptyText string
	Items     []emailItem
}

type emailItem struct {
	N            int
	Title        string
	Essence      string
	Consequences string
	Rationale    string
	Sources      []emailSource
}

type emailSource struct {
	Line string
	URL  string
}

var emailTpl = template.Must(template.New("email").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>{{.Subject}}</title>
</head>
<body>
<p>{{.Subject}}</p>
<p>Период: {{.Period}}</p>
<p>Контрольное время: {{.Control}}</p>
<p>{{.Count}}</p>
{{if .Empty}}<p>{{.EmptyText}}</p>
{{else}}{{range .Items}}<h2>{{.N}}. {{.Title}}</h2>
<p>Суть: {{.Essence}}</p>
<p>Последствия: {{.Consequences}}</p>
<p>Источники: {{range $i, $s := .Sources}}{{if $i}}; {{end}}{{if $s.URL}}<a href="{{$s.URL}}">{{$s.Line}}</a>{{else}}{{$s.Line}}{{end}}{{end}}</p>
<p>Обоснование: {{.Rationale}}</p>
{{end}}{{end}}</body>
</html>
`))
