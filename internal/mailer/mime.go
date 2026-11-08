package mailer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"
)

// fallbackHTML is the single letter shell used when the run has no HTML preview.
var fallbackHTML = template.Must(template.New("mail").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background:#f3f3f1;color:#29251b;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f3f1;">
<tr><td align="center" style="padding:28px 16px;">
<div style="max-width:640px;margin:0 auto;background:#fffaf0;border:1px solid #dfd5ba;border-radius:28px;overflow:hidden;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#fffaf0;">
<tr><td style="background:#17170f;padding:26px 32px 22px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<div style="font-size:22px;font-weight:700;letter-spacing:.12em;line-height:1;color:#f6df63;">MONO</div>
<div style="margin-top:8px;font-size:11px;font-weight:700;letter-spacing:.16em;text-transform:uppercase;color:#ceccbf;">Кибердайджест</div>
</td></tr>
<tr><td style="padding:28px 32px 8px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<h1 style="margin:0 0 12px;font-size:26px;line-height:1.25;letter-spacing:-.03em;font-weight:650;color:#29251b;">{{.Subject}}</h1>
{{if .Period}}<p style="margin:0 0 16px;font-size:15px;line-height:1.5;color:#59513e;">Период: {{.Period}}</p>{{end}}
</td></tr>
<tr><td style="padding:0 32px 28px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<pre style="margin:0;white-space:pre-wrap;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.55;color:#29251b;">{{.Text}}</pre>
</td></tr>
<tr><td style="background:#17170f;padding:16px 32px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:12px;letter-spacing:.04em;color:#ceccbf;">MONO · дайджест по кибербезопасности</td></tr>
</table>
</div>
</td></tr>
</table>
</body>
</html>
`))

func buildMIME(from string, to []string, msg Message, now time.Time) ([]byte, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	boundary := "cd-" + hex.EncodeToString(nonce[:])
	text := composeText(msg)
	htmlBody := strings.TrimSpace(msg.HTML)
	if htmlBody == "" {
		var buf bytes.Buffer
		if err := fallbackHTML.Execute(&buf, struct {
			Subject string
			Period  string
			Text    string
		}{msg.Subject, strings.TrimSpace(msg.Period), text}); err != nil {
			return nil, err
		}
		htmlBody = buf.String()
	}
	var buf bytes.Buffer
	write := func(line string) {
		buf.WriteString(line)
		buf.WriteString("\r\n")
	}
	write("From: " + sanitizeHeader(from))
	write("To: " + sanitizeHeader(strings.Join(to, ", ")))
	write("Subject: " + mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject)))
	write("Date: " + now.UTC().Format(time.RFC1123Z))
	write("MIME-Version: 1.0")
	write("Content-Type: multipart/alternative; boundary=" + boundary)
	write("")
	if err := writePart(&buf, boundary, "text/plain", text); err != nil {
		return nil, err
	}
	if err := writePart(&buf, boundary, "text/html", htmlBody); err != nil {
		return nil, err
	}
	write("--" + boundary + "--")
	return buf.Bytes(), nil
}

func writePart(buf *bytes.Buffer, boundary, kind, body string) error {
	fmt.Fprintf(buf, "--%s\r\n", boundary)
	fmt.Fprintf(buf, "Content-Type: %s; charset=UTF-8\r\n", kind)
	buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(buf)
	if _, err := qp.Write([]byte(body)); err != nil {
		_ = qp.Close()
		return err
	}
	if err := qp.Close(); err != nil {
		return err
	}
	if !bytes.HasSuffix(buf.Bytes(), []byte("\r\n")) {
		buf.WriteString("\r\n")
	}
	return nil
}

func composeText(msg Message) string {
	body := msg.Text
	var head strings.Builder
	if subj := strings.TrimSpace(msg.Subject); subj != "" && !strings.Contains(body, "Тема:") {
		head.WriteString("Тема: ")
		head.WriteString(subj)
		head.WriteByte('\n')
	}
	period := strings.TrimSpace(msg.Period)
	if period != "" && !strings.Contains(body, period) {
		head.WriteString("Период: ")
		head.WriteString(period)
		head.WriteByte('\n')
	}
	if head.Len() == 0 {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return strings.TrimRight(head.String(), "\n") + "\n"
	}
	return head.String() + "\n" + body
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}
