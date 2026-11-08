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
<body style="margin:0;padding:0;background:#f8f6f0;color:#2c261a;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">Период: {{.Period}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f8f6f0;margin:0;padding:0;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:640px;margin:0 auto;background:#fffbef;border:1px solid #e8d9af;border-radius:10px;">
<tr><td style="padding:18px 22px 16px;border-bottom:1px solid #ded3b8;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td valign="middle" style="padding:0 10px 0 0;">
<div style="width:28px;height:28px;border-radius:8px;background:#f6df63;color:#26200a;font-size:13px;font-weight:700;line-height:28px;text-align:center;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">M</div>
</td>
<td valign="middle" style="font-size:18px;font-weight:700;letter-spacing:.04em;line-height:1;color:#17170f;">MONO</td>
</tr></table>
<p style="margin:14px 0 0;font-size:11px;font-weight:700;line-height:1.4;color:#5c5341;">Кибердайджест</p>
<h1 style="margin:4px 0 10px;font-size:20px;line-height:1.25;letter-spacing:-.02em;font-weight:700;color:#2c261a;">{{.Subject}}</h1>
<p style="margin:0 0 4px;font-size:12px;line-height:1.5;color:#5c5341;">Период: {{.Period}}</p>
<p style="margin:0;font-size:12px;line-height:1.5;color:#5c5341;">Контрольное время: {{.Control}}</p>
<p style="margin:12px 0 0;padding:8px 10px;background:#fbf4df;border:1px solid #e7d9b4;border-radius:8px;font-size:12px;line-height:1.5;color:#2c261a;">{{.Count}}</p>
</td></tr>
{{if .Empty}}
<tr><td style="padding:16px 22px 22px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<p style="margin:0;padding:13px;background:#fdfbf6;border:1px solid #ded3b8;border-radius:10px;font-size:13px;line-height:1.55;color:#5c5341;">{{.EmptyText}}</p>
</td></tr>
{{else}}{{range .Items}}
<tr><td style="padding:10px 22px 0;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#fbf6e8;border:1px solid #ded3b8;border-radius:10px;">
<tr><td style="padding:13px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr>
<td valign="top" style="width:30px;padding:0 12px 0 0;">
<div style="width:30px;height:30px;border-radius:7px;background:#eee3c4;color:#6f5012;font-size:12px;font-weight:700;line-height:30px;text-align:center;font-family:ui-monospace,Consolas,monospace;">{{.N}}</div>
</td>
<td valign="top">
<div style="margin:3px 0 8px;font-size:15px;line-height:1.35;font-weight:700;color:#2c261a;">{{.Title}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;">
<tr>
<td style="padding:0 8px 6px 0;width:94px;vertical-align:top;font-size:11px;font-weight:700;line-height:1.5;color:#5c5341;">Суть</td>
<td style="padding:0 0 6px;vertical-align:top;font-size:12px;line-height:1.5;color:#2c261a;">{{.Essence}}</td>
</tr>
<tr>
<td style="padding:0 8px 6px 0;width:94px;vertical-align:top;font-size:11px;font-weight:700;line-height:1.5;color:#5c5341;">Последствия</td>
<td style="padding:0 0 6px;vertical-align:top;font-size:12px;line-height:1.5;color:#2c261a;">{{.Consequences}}</td>
</tr>
<tr>
<td style="padding:0 8px 6px 0;width:94px;vertical-align:top;font-size:11px;font-weight:700;line-height:1.5;color:#5c5341;">Обоснование</td>
<td style="padding:0 0 6px;vertical-align:top;font-size:12px;line-height:1.5;color:#2c261a;">{{.Rationale}}</td>
</tr>
<tr>
<td style="padding:4px 8px 0 0;width:94px;vertical-align:top;font-size:11px;font-weight:700;line-height:1.5;color:#5c5341;">Источники</td>
<td style="padding:4px 0 0;vertical-align:top;">
{{range .Sources}}{{if .URL}}<a href="{{.URL}}" style="display:inline-block;margin:0 5px 5px 0;padding:4px 7px;border:1px solid #ded3b8;border-radius:6px;background:#fffbef;color:#654a0d;font-size:11px;line-height:1.35;text-decoration:none;">{{.Line}}</a>{{else}}<span style="display:inline-block;margin:0 5px 5px 0;padding:4px 7px;border:1px solid #ded3b8;border-radius:6px;background:#fffbef;color:#5c5341;font-size:11px;line-height:1.35;">{{.Line}}</span>{{end}}{{end}}
</td>
</tr>
</table>
</td>
</tr></table>
</td></tr>
</table>
</td></tr>
{{end}}
<tr><td style="height:16px;font-size:0;line-height:0;">&nbsp;</td></tr>
{{end}}
<tr><td style="padding:14px 22px;border-top:1px solid #ded3b8;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:11px;line-height:1.5;color:#5c5341;">MONO · дайджест по кибербезопасности</td></tr>
</table>
</td></tr>
</table>
</body>
</html>
`))
