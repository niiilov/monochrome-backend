package mailer

import (
	"context"
	"errors"
	"html"
	"strings"
)

// SendVerification sends only a fixed account verification template to one address.
// It always uses SMTP, independently of digest dry-run and recipient allowlists.
func (m *Mailer) SendVerification(ctx context.Context, email, link string) error {
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
		Subject: "Подтвердите почту | Monochrome Digital",
		Text:    "Для завершения регистрации подтвердите почту:\n" + link + "\n\nСсылка действует 24 часа и используется один раз. Если вы не регистрировались, проигнорируйте письмо.",
		HTML:    `<!doctype html><html lang="ru"><body><h1>Подтвердите почту</h1><p>Для завершения регистрации перейдите по ссылке:</p><p><a href="` + html.EscapeString(link) + `">Подтвердить почту</a></p><p>Ссылка действует 24 часа и используется один раз. Если вы не регистрировались, проигнорируйте письмо.</p></body></html>`,
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
