package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mono/internal/config"
	"mono/internal/mailer"
	"mono/internal/storage"
)

func TestSMTPSettings(t *testing.T) { exerciseSMTPSettings(t, newMemoryStore()) }

func TestPostgresSMTPSettings(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	db, err := storage.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exerciseSMTPSettings(t, db)
}

func exerciseSMTPSettings(t *testing.T, db storage.Repository) {
	t.Helper()
	ctx := context.Background()
	defaultSender := &captureSender{}
	defaults := mailer.New(config.Mail{Host: "smtp.original.example", Port: 465, User: "codes@example.com", From: "codes@example.com", Password: "original-smtp-secret", Allowlist: []string{"reader@example.com"}}, nil)
	defaults.SetSender(defaultSender)
	service, err := New(&stubRunner{}, defaults, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), db, Options{DatasetRoots: []string{fixture("testdata")}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(service.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = service.Shutdown(ctx) })
	seed := func() (string, string) {
		id, _ := randomToken()
		token, _ := randomToken()
		if _, err := db.Register(ctx, storage.User{ID: id, Email: id[:16] + "@example.com"}, "unused", tokenHash(token), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.VerifyEmail(ctx, tokenHash(token)); err != nil {
			t.Fatal(err)
		}
		if err := db.CreateSession(ctx, id, tokenHash(token), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return id, token
	}
	userID, token := seed()
	_, otherToken := seed()
	call := func(method, path, payload, auth string, want int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, res.StatusCode, want, raw)
		}
		if strings.Contains(string(raw), "original-smtp-secret") || strings.Contains(string(raw), "custom-smtp-secret") || strings.Contains(string(raw), `"password":`) {
			t.Fatal("SMTP password leaked in an API response")
		}
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		return doc
	}
	const path = "/api/v1/settings/smtp"
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		call(method, path, "{}", "", 401)
	}
	doc := call("GET", path, "", token, 200)
	if doc["is_default"] != true || doc["has_password"] != true || doc["settings"].(map[string]any)["from"] != "codes@example.com" {
		t.Fatalf("wrong defaults: %+v", doc)
	}
	call("PUT", path, `{"host":"smtp.changed.example","port":465,"username":"codes@example.com","from":"digest@example.com"}`, token, 400)
	call("PUT", path, `{"host":"smtp.original.example","port":587,"username":"codes@example.com","from":"digest@example.com"}`, token, 400)
	call("PUT", path, `{"host":"smtp.original.example","port":465,"username":"other@example.com","from":"digest@example.com"}`, token, 400)
	call("PUT", path, `{"host":"smtp.original.example","port":465,"username":"codes@example.com","from":"digest@example.com"}`, token, 200)
	settings, err := db.GetSMTPSettings(ctx, userID)
	if err != nil || settings.Password != "original-smtp-secret" {
		t.Fatal("unchanged SMTP connection did not preserve its password")
	}
	for _, bad := range []string{
		`{"host":"smtp.example\r\nMAIL FROM:bad","port":465,"from":"digest@example.com"}`,
		`{"host":"smtp.example","port":0,"from":"digest@example.com"}`,
		`{"host":"smtp.example","port":65536,"from":"digest@example.com"}`,
		`{"host":"smtp.example","port":465,"from":"invalid"}`,
		`{"host":"https://smtp.example","port":465,"from":"digest@example.com"}`,
	} {
		call("PUT", path, bad, token, 400)
	}
	custom := `{"host":"smtp.custom.example","port":587,"username":"digest@example.com","password":"custom-smtp-secret","from":"digest@example.com"}`
	call("PUT", path, custom, token, 200)
	call("PUT", path, `{"host":"smtp.custom.example","port":587,"username":"digest@example.com","from":"updated@example.com"}`, token, 200)
	delivery, err := service.digestMailer(ctx, userID)
	if err != nil || delivery.SMTPConfig().Password != "custom-smtp-secret" {
		t.Fatal("saved custom password was not retained")
	}
	// A new server instance uses persistent settings without changing other accounts.
	restarted, err := New(&stubRunner{}, defaults, t.TempDir(), nil, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx)
	delivery, err = restarted.digestMailer(ctx, userID)
	if err != nil || delivery.SMTPConfig().From != "updated@example.com" {
		t.Fatal("SMTP settings did not survive API reinitialization")
	}
	if call("GET", path, "", otherToken, 200)["is_default"] != true {
		t.Fatal("SMTP settings leaked across accounts")
	}
	// Both delivery modes must actually send using the account's SMTP server.
	port, letters := smtpInbox(t)
	local := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"username":"","from":"digest@example.com"}`, port)
	call("PUT", path, local, token, 200)
	id := call("POST", "/api/v1/runs", createPayload(t, fixture("testdata/synthetic/articles.json")), token, 202)["run_id"].(string)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		status := call("GET", "/api/v1/runs/"+id, "", token, 200)["status"]
		if status == statusDone {
			break
		}
		if status == statusFailed || time.Now().After(deadline) {
			t.Fatal("run did not complete")
		}
	}
	call("POST", "/api/v1/recipients", `{"email":"reader@example.com"}`, token, 201)
	for _, payload := range []string{`{"mode":"all","dry_run":false}`, `{"recipients":["reader@example.com"],"dry_run":false}`} {
		call("POST", "/api/v1/runs/"+id+"/send", payload, token, 200)
		select {
		case letter := <-letters:
			if !strings.Contains(letter, "From: digest@example.com") || !strings.Contains(letter, "To: reader@example.com") {
				t.Fatal("digest used the wrong SMTP sender or recipient")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("digest was not delivered to the custom SMTP server")
		}
	}
	if len(defaultSender.msgs) != 0 {
		t.Fatal("digest was sent through the verification SMTP transport")
	}
	if err := service.sendVerification(ctx, "new-user@example.com", "123456"); err != nil {
		t.Fatal(err)
	}
	if len(defaultSender.msgs) != 1 || !strings.Contains(string(defaultSender.msgs[0]), "From: codes@example.com") {
		t.Fatal("registration code sender changed after updating digest SMTP")
	}
	doc = call("DELETE", path, "", token, 200)
	if doc["is_default"] != true || doc["settings"].(map[string]any)["from"] != "codes@example.com" {
		t.Fatal("reset did not restore server defaults")
	}
	delivery, err = service.digestMailer(ctx, userID)
	if err != nil || delivery != defaults {
		t.Fatal("default delivery transport was not restored")
	}
}

// smtpInbox accepts local test mail without sending anything outside the test.
func smtpInbox(t *testing.T) (int, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	letters := make(chan string, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 localhost ESMTP\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "DATA"):
						fmt.Fprint(conn, "354 Send message\r\n")
						var body strings.Builder
						for {
							part, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if part == ".\r\n" {
								break
							}
							body.WriteString(part)
						}
						letters <- body.String()
						fmt.Fprint(conn, "250 Queued\r\n")
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprint(conn, "221 Bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 localhost\r\n")
					}
				}
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, letters
}
