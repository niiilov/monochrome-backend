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
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background:#f3f3f1;color:#29251b;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">Период: {{.Period}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f3f1;margin:0;padding:0;">
<tr><td align="center" style="padding:28px 16px;">
<div style="max-width:640px;margin:0 auto;background:#fffaf0;border:1px solid #dfd5ba;border-radius:28px;overflow:hidden;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#fffaf0;">
<tr><td style="background:#17170f;padding:26px 32px 22px;">
<div style="font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:22px;font-weight:700;letter-spacing:.12em;line-height:1;color:#f6df63;">MONO</div>
<div style="margin-top:8px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:11px;font-weight:700;letter-spacing:.16em;text-transform:uppercase;color:#ceccbf;">Кибердайджест</div>
</td></tr>
<tr><td style="padding:32px 32px 8px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<div style="margin:0 0 10px;font-size:12px;font-weight:700;letter-spacing:.14em;text-transform:uppercase;color:#70540b;">Выпуск</div>
<h1 style="margin:0 0 18px;font-size:28px;line-height:1.25;letter-spacing:-.03em;font-weight:650;color:#29251b;">{{.Subject}}</h1>
<p style="margin:0 0 6px;font-size:15px;line-height:1.5;color:#59513e;">Период: {{.Period}}</p>
<p style="margin:0;font-size:15px;line-height:1.5;color:#59513e;">Контрольное время: {{.Control}}</p>
<div style="margin:18px 0 8px;padding:14px 16px;background:#f4ecd5;border-radius:14px;font-size:15px;line-height:1.5;color:#29251b;">{{.Count}}</div>
</td></tr>
{{if .Empty}}
<tr><td style="padding:8px 32px 28px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<p style="margin:0;padding:18px 18px;background:#ffffff;border:1px solid #dfd5ba;border-radius:16px;font-size:15px;line-height:1.55;color:#59513e;">{{.EmptyText}}</p>
</td></tr>
{{else}}{{range .Items}}
<tr><td style="padding:12px 32px 0;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#ffffff;border:1px solid #dfd5ba;border-radius:18px;">
<tr><td style="padding:20px 20px 8px;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td valign="top" style="padding:0 12px 0 0;">
<div style="width:32px;height:32px;border-radius:16px;background:#f6df63;color:#26200a;font-size:14px;font-weight:700;line-height:32px;text-align:center;">{{.N}}</div>
</td>
<td valign="middle" style="font-size:20px;line-height:1.3;font-weight:700;letter-spacing:-.02em;color:#29251b;">{{.Title}}</td>
</tr></table>
</td></tr>
<tr><td style="padding:8px 20px 0;font-size:15px;line-height:1.55;color:#29251b;"><span style="color:#70540b;font-weight:700;">Суть:</span> {{.Essence}}</td></tr>
<tr><td style="padding:8px 20px 0;font-size:15px;line-height:1.55;color:#29251b;"><span style="color:#70540b;font-weight:700;">Последствия:</span> {{.Consequences}}</td></tr>
<tr><td style="padding:8px 20px 0;font-size:15px;line-height:1.55;color:#29251b;"><span style="color:#70540b;font-weight:700;">Источники:</span> {{range $i, $s := .Sources}}{{if $i}}; {{end}}{{if $s.URL}}<a href="{{$s.URL}}" style="color:#70540b;text-decoration:underline;">{{$s.Line}}</a>{{else}}{{$s.Line}}{{end}}{{end}}</td></tr>
<tr><td style="padding:12px 20px 20px;font-size:14px;line-height:1.55;color:#59513e;"><span style="color:#70540b;font-weight:700;">Обоснование:</span> {{.Rationale}}</td></tr>
</table>
</td></tr>
{{end}}
<tr><td style="height:20px;font-size:0;line-height:0;">&nbsp;</td></tr>
{{end}}
<tr><td style="background:#17170f;padding:16px 32px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:12px;line-height:1.5;letter-spacing:.04em;color:#ceccbf;">MONO · дайджест по кибербезопасности</td></tr>
</table>
</div>
</td></tr>
</table>
</body>
</html>
`))
