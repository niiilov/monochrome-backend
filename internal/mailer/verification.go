package mailer

import (
	"context"
	"errors"
	"html"
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
	msg := Message{
		Subject: "Код подтверждения | Monochrome Digital",
		Text:    "Код подтверждения:\n\n" + code + "\n\nКод действует 15 минут и используется один раз. Если вы не регистрировались, проигнорируйте письмо.",
		HTML:    `<!doctype html><html lang="ru"><body><h1>Код подтверждения</h1><p>Для завершения регистрации введите код:</p><p style="font-size:24px;letter-spacing:4px">` + html.EscapeString(code) + `</p><p>Код действует 15 минут и используется один раз. Если вы не регистрировались, проигнорируйте письмо.</p></body></html>`,
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
