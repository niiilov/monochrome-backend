package mailer

import (
	"context"
	"errors"
	"html/template"
	"strings"
)

// SendVerification sends one registration code through the same SMTP sender as digest mail.
// The digest allowlist and dry-run do not apply: the code must reach the address being registered.
func (m *Mailer) SendVerification(ctx context.Context, email, code string) error {
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return errors.New("некорректный код подтверждения")
	}
	to, err := parseAddress(email)
	if err != nil {
		return err
	}
	from, err := m.senderAddress(false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(m.cfg.Host) == "" {
		return errors.New("не задан SMTP_HOST")
	}
	var body strings.Builder
	if err = verificationTpl.Execute(&body, struct{ Code string }{code}); err != nil {
		return errors.New("не удалось собрать письмо подтверждения")
	}
	msg := Message{
		Subject: "Код подтверждения | Monochrome Digital",
		Text:    "Код подтверждения:\n\n" + code + "\n\nКод действует 15 минут и используется один раз. Если вы не регистрировались, проигнорируйте письмо.",
		HTML:    body.String(),
	}
	raw, err := buildMIME(from, []string{to}, msg, m.now())
	if err != nil {
		return errors.New("не удалось собрать письмо подтверждения")
	}
	if err = m.send.Send(ctx, from, []string{to}, raw); err != nil {
		return errors.New("SMTP: " + m.safe(err))
	}
	return nil
}

// verificationTpl follows the site: cream page, dark masthead, gold MONO mark,
// and the code on a dark plate like the registration form.
var verificationTpl = template.Must(template.New("code").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Код подтверждения</title>
</head>
<body style="margin:0;padding:0;background:#f3f3f1;color:#29251b;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f3f3f1;margin:0;padding:0;">
<tr><td align="center" style="padding:28px 16px;">
<div style="max-width:560px;margin:0 auto;background:#fffaf0;border:1px solid #dfd5ba;border-radius:28px;overflow:hidden;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#fffaf0;">
<tr><td style="background:#17170f;padding:26px 32px 22px;">
<div style="font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:22px;font-weight:700;letter-spacing:.12em;line-height:1;color:#f6df63;">MONO</div>
<div style="margin-top:8px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:11px;font-weight:700;letter-spacing:.16em;text-transform:uppercase;color:#ceccbf;">Monochrome Digital</div>
</td></tr>
<tr><td style="padding:32px 32px 12px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<div style="margin:0 0 10px;font-size:12px;font-weight:700;letter-spacing:.14em;text-transform:uppercase;color:#70540b;">Регистрация</div>
<h1 style="margin:0 0 12px;font-size:32px;line-height:1.15;letter-spacing:-.035em;font-weight:650;color:#29251b;">Код подтверждения</h1>
<p style="margin:0 0 22px;font-size:16px;line-height:1.5;color:#59513e;">Для завершения регистрации введите код:</p>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0">
<tr><td align="center" style="background:#17170f;border-radius:16px;padding:22px 16px;">
<div style="font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;font-size:36px;font-weight:700;letter-spacing:.42em;line-height:1;color:#f6df63;padding-left:.42em;">{{.Code}}</div>
</td></tr>
</table>
<p style="margin:22px 0 0;font-size:14px;line-height:1.55;color:#59513e;">Код действует 15 минут и используется один раз.</p>
</td></tr>
<tr><td style="padding:8px 32px 28px;font-family:Manrope,Segoe UI,Helvetica,Arial,sans-serif;">
<div style="border-top:1px solid #dfd5ba;padding-top:16px;font-size:13px;line-height:1.5;color:#8a8172;">Если вы не регистрировались, проигнорируйте письмо.</div>
</td></tr>
</table>
</div>
</td></tr>
</table>
</body>
</html>
`))
