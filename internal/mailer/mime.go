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
<title>{{.Subject}}</title>
</head>
<body>
<p>{{.Subject}}</p>
{{if .Period}}<p>Период: {{.Period}}</p>{{end}}
<pre>{{.Text}}</pre>
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
