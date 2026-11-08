package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"mono/internal/config"
	"mono/internal/mailer"
	"mono/internal/storage"
)

type testAccount struct {
	user storage.User
	hash string
}
type testToken struct {
	userID  string
	expires time.Time
}
type memoryStore struct {
	mu            sync.Mutex
	users         map[string]testAccount
	sessions      map[string]testToken
	verifications map[string]testToken
	runs          map[string]storage.Run
}

func newMemoryStore() *memoryStore {
	return &memoryStore{users: map[string]testAccount{}, sessions: map[string]testToken{}, verifications: map[string]testToken{}, runs: map[string]storage.Run{}}
}
func (db *memoryStore) Register(_ context.Context, u storage.User, hash, token string, expires time.Time) (storage.User, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := db.users[u.Email]; ok {
		return storage.User{}, storage.ErrEmailTaken
	}
	u.CreatedAt = time.Now()
	db.users[u.Email] = testAccount{u, hash}
	db.verifications[token] = testToken{u.ID, expires}
	return u, nil
}
func (db *memoryStore) VerifyEmail(_ context.Context, token string) (storage.User, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	t, ok := db.verifications[token]
	if !ok || !t.expires.After(time.Now()) {
		return storage.User{}, storage.ErrNotFound
	}
	delete(db.verifications, token)
	for email, a := range db.users {
		if a.user.ID == t.userID {
			a.user.EmailVerified = true
			db.users[email] = a
			return a.user, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}
func (db *memoryStore) ReplaceVerification(_ context.Context, id, token string, expires time.Time) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, a := range db.users {
		if a.user.ID == id && !a.user.EmailVerified {
			for hash, t := range db.verifications {
				if t.userID == id {
					delete(db.verifications, hash)
				}
			}
			db.verifications[token] = testToken{id, expires}
			return nil
		}
	}
	return storage.ErrNotFound
}
func (db *memoryStore) UserByEmail(_ context.Context, email string) (storage.User, string, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	u, ok := db.users[email]
	if !ok {
		return storage.User{}, "", storage.ErrNotFound
	}
	return u.user, u.hash, nil
}
func (db *memoryStore) CreateSession(_ context.Context, id, hash string, expires time.Time) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, a := range db.users {
		if a.user.ID == id && a.user.EmailVerified {
			db.sessions[hash] = testToken{id, expires}
			return nil
		}
	}
	return storage.ErrNotFound
}
func (db *memoryStore) SessionUser(_ context.Context, hash string) (storage.User, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	s, ok := db.sessions[hash]
	if !ok || !s.expires.After(time.Now()) {
		return storage.User{}, storage.ErrNotFound
	}
	for _, u := range db.users {
		if u.user.ID == s.userID && u.user.EmailVerified {
			return u.user, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}
func (db *memoryStore) DeleteSession(_ context.Context, hash string) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.sessions, hash)
	return nil
}
func (db *memoryStore) CreateRun(_ context.Context, run storage.Run) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := db.runs[run.ID]; ok {
		return errors.New("duplicate run")
	}
	if run.ParentID != "" {
		parent, ok := db.runs[run.ParentID]
		if !ok || parent.UserID != run.UserID {
			return errors.New("foreign parent")
		}
	}
	db.runs[run.ID] = run
	return nil
}
func (db *memoryStore) UpdateRun(_ context.Context, run storage.Run) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	old, ok := db.runs[run.ID]
	if !ok || old.UserID != run.UserID {
		return storage.ErrNotFound
	}
	old.Snapshot, old.Status = run.Snapshot, run.Status
	db.runs[run.ID] = old
	return nil
}
func (db *memoryStore) GetRun(_ context.Context, user, id string) (storage.Run, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	r, ok := db.runs[id]
	if !ok || r.UserID != user {
		return storage.Run{}, storage.ErrNotFound
	}
	return r, nil
}
func (db *memoryStore) ListRuns(_ context.Context, user, status string) ([]storage.Run, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	runs := []storage.Run{}
	for _, r := range db.runs {
		if r.UserID == user && (status == "" || status == r.Status) {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	return runs, nil
}
func (db *memoryStore) RecoverRuns(_ context.Context) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for id, r := range db.runs {
		if r.Status == statusRunning {
			var view runView
			_ = json.Unmarshal(r.Snapshot, &view)
			view.Status = statusFailed
			view.Error = "сервер перезапущен до конца прогона"
			view.FinishedAt = formatStamp(time.Now())
			r.Status = statusFailed
			r.Snapshot, _ = json.Marshal(view)
			db.runs[id] = r
		}
	}
	return nil
}
func (db *memoryStore) Ping(context.Context) error { return nil }

func (db *memoryStore) UpdateUser(_ context.Context, id string, name *string, keywords *[]string) (storage.User, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for email, a := range db.users {
		if a.user.ID == id {
			if name != nil {
				a.user.Name = *name
			}
			if keywords != nil {
				a.user.Keywords = append([]string{}, (*keywords)...)
			}
			db.users[email] = a
			return a.user, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}

func TestAuthAndOwnership(t *testing.T) { exerciseAuth(t, newMemoryStore()) }

func TestPostgresAuthAndPersistence(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := storage.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Opening again verifies that migrations are repeatable.
	db2, err := storage.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
	exerciseAuth(t, db)
}

func exerciseAuth(t *testing.T, db storage.Repository) {
	t.Helper()
	dir := t.TempDir()
	sender := &captureSender{}
	makeServer := func() (*httptest.Server, *Server) {
		m := mailer.New(config.Mail{Host: "smtp.example", Port: 465, From: "mono@example.com", DryRun: true}, nil)
		m.SetSender(sender)
		s, err := New(&stubRunner{}, m, dir, slog.New(slog.NewTextHandler(io.Discard, nil)), db, Options{SecureCookies: true, PublicURL: "https://monochrome-digital.online", DatasetRoots: []string{fixture("testdata")}})
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(s.Handler())
		t.Cleanup(func() {
			ts.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)
		})
		return ts, s
	}
	ts, service := makeServer()
	call := func(method, path, payload, token string, want int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
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
		var doc map[string]any
		_ = json.Unmarshal(raw, &doc)
		return doc
	}
	const auth = "/api/v1/auth/"
	call("GET", "/api/v1/runs", "", "", 401)
	call("POST", auth+"register", `{"email":"invalid","password":"12345678"}`, "", 400)
	call("POST", auth+"register", `{"email":"valid@example.com","password":"short"}`, "", 400)
	call("POST", auth+"register", `{"email":"valid@example.com","password":"12345678"} {}`, "", 400)
	suffix, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	email := "user-" + suffix[:12] + "@example.com"
	payload := fmt.Sprintf(`{"email":%q,"password":"valid-password","name":"Рита"}`, strings.ToUpper(email))
	doc := call("POST", auth+"register", payload, "", 201)
	if doc["token"] != nil || doc["verification_required"] != true {
		t.Fatalf("registration must require verification: %+v", doc)
	}
	u, hash, err := db.UserByEmail(context.Background(), email)
	if err != nil || u.EmailVerified || hash == "valid-password" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("valid-password")) != nil {
		t.Fatalf("bad password storage: %v", err)
	}
	call("POST", auth+"register", payload, "", 409)
	call("POST", auth+"login", payload, "", 403)
	call("POST", auth+"login", fmt.Sprintf(`{"email":%q,"password":"wrong-password"}`, email), "", 401)
	link, token := lastVerification(t, sender, email)
	if !strings.HasPrefix(link, "https://monochrome-digital.online/api/v1/auth/verify-email?") {
		t.Fatalf("wrong public URL: %s", link)
	}
	call("GET", auth+"verify-email?token="+token, "", "", 200)
	call("POST", auth+"login", payload, "", 403) // scanners visiting GET cannot confirm the account
	if err := db.ReplaceVerification(context.Background(), u.ID, tokenHash(strings.Repeat("f", 64)), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	call("POST", auth+"verify-email", fmt.Sprintf(`{"token":%q}`, strings.Repeat("f", 64)), "", 400)
	call("POST", auth+"resend-verification", fmt.Sprintf(`{"email":%q}`, email), "", 202)
	_, replacement := lastVerification(t, sender, email)
	call("POST", auth+"verify-email", fmt.Sprintf(`{"token":%q}`, token), "", 400)
	call("POST", auth+"verify-email", fmt.Sprintf(`{"token":%q}`, replacement), "", 200)
	call("POST", auth+"verify-email", fmt.Sprintf(`{"token":%q}`, replacement), "", 400)
	login := call("POST", auth+"login", payload, "", 200)
	first := login["token"].(string)
	if _, err := db.SessionUser(context.Background(), first); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("raw session tokens must not be stored")
	}
	call("GET", auth+"me", "", first, 200)
	// Check cookie authentication and HTTPS attributes without putting credentials in localStorage.
	req, _ := http.NewRequest("POST", ts.URL+auth+"login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	cookies := res.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe session cookies: %+v", cookies)
	}
	req, _ = http.NewRequest("GET", ts.URL+auth+"me", nil)
	req.AddCookie(cookies[0])
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("cookie authentication failed")
	}
	call("POST", auth+"logout", "{}", first, 200)
	call("GET", auth+"me", "", first, 401)
	first = call("POST", auth+"login", payload, "", 200)["token"].(string) // no second confirmation
	call("PATCH", auth+"me", `{"name":"Новое имя","keywords":["CVE","ransomware"]}`, first, 200)
	if err := db.CreateSession(context.Background(), u.ID, tokenHash(suffix), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	call("GET", auth+"me", "", suffix, 401)
	// A second account has no access to the first account's requests or artifacts.
	secondEmail := "other-" + suffix[:12] + "@example.com"
	secondPayload := fmt.Sprintf(`{"email":%q,"password":"other-password"}`, secondEmail)
	call("POST", auth+"register", secondPayload, "", 201)
	_, vtoken := lastVerification(t, sender, secondEmail)
	call("POST", auth+"verify-email", fmt.Sprintf(`{"token":%q}`, vtoken), "", 200)
	second := call("POST", auth+"login", secondPayload, "", 200)["token"].(string)
	other := call("GET", auth+"me", "", second, 200)["user"].(map[string]any)
	if other["name"] == "Новое имя" {
		t.Fatal("profile leaked across accounts")
	}
	created := call("POST", "/api/v1/runs", createPayload(t, fixture("testdata/synthetic/articles.json")), first, 202)
	id := created["run_id"].(string)
	for deadline := time.Now().Add(5 * time.Second); ; {
		state := call("GET", "/api/v1/runs/"+id, "", first, 200)
		if state["status"] == statusDone {
			break
		}
		if state["status"] == statusFailed || time.Now().After(deadline) {
			t.Fatalf("run failed: %+v", state)
		}
		time.Sleep(15 * time.Millisecond)
	}
	history := call("GET", "/api/v1/runs", "", second, 200)
	if len(history["runs"].([]any)) != 0 {
		t.Fatal("another user's history leaked")
	}
	for _, suffix := range []string{"", "/digest", "/post", "/email", "/journal", "/report", "/export"} {
		call("GET", "/api/v1/runs/"+id+suffix, "", second, 404)
	}
	for _, suffix := range []string{"/articles", "/send"} {
		call("POST", "/api/v1/runs/"+id+suffix, "{}", second, 404)
	}
	call("POST", "/api/v1/runs", createPayload(t, filepath.Join(dir, id, "input", "secret.json")), second, 400)
	// An existing file from another run cannot be used as a dataset or an added article.
	private := filepath.Join(dir, id, "post.txt")
	call("POST", "/api/v1/runs", createPayload(t, private), second, 400)
	call("POST", "/api/v1/runs/"+id+"/articles", fmt.Sprintf(`{"path":%q}`, private), first, 400)
	child := call("POST", "/api/v1/runs/"+id+"/articles", `{"title":"Дополнение","text":"Публикация пользователя"}`, first, 202)["run_id"].(string)
	call("GET", "/api/v1/runs/"+child, "", second, 404)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	ts.Close()
	orphanID := "orphan-" + suffix[:12]
	orphanSnapshot, _ := json.Marshal(runView{RunID: orphanID, Status: statusRunning, Stages: []stageProg{}})
	if err := db.CreateRun(context.Background(), storage.Run{ID: orphanID, UserID: u.ID, Status: statusRunning, StartedAt: time.Now(), Snapshot: orphanSnapshot, Request: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	ts, _ = makeServer()
	call("GET", auth+"me", "", first, 200)
	profile := call("GET", auth+"me", "", first, 200)["user"].(map[string]any)
	if profile["name"] != "Новое имя" || len(profile["keywords"].([]any)) != 2 {
		t.Fatal("profile was not persisted")
	}
	if call("GET", "/api/v1/runs/"+orphanID, "", first, 200)["status"] != statusFailed {
		t.Fatal("interrupted run was not recovered")
	}
	call("GET", "/api/v1/runs/"+id+"/export", "", first, 200)
	run, err := db.GetRun(context.Background(), u.ID, id)
	if err != nil || !strings.Contains(string(run.Request), "control_time") {
		t.Fatal("request parameters were not persisted")
	}
	// Check request origin validation even with a valid session.
	req, _ = http.NewRequest("POST", ts.URL+auth+"logout", nil)
	req.Header.Set("Authorization", "Bearer "+first)
	req.Header.Set("Origin", "https://attacker.example")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross-site mutation accepted")
	}
}

func lastVerification(t *testing.T, sender *captureSender, email string) (string, string) {
	t.Helper()
	sender.mu.Lock()
	defer sender.mu.Unlock()
	for i := len(sender.msgs) - 1; i >= 0; i-- {
		msg, err := mail.ReadMessage(strings.NewReader(string(sender.msgs[i])))
		if err != nil {
			t.Fatal(err)
		}
		if msg.Header.Get("To") != email {
			continue
		}
		plain := readPlain(t, msg)
		for _, word := range strings.Fields(plain) {
			if strings.Contains(word, "/api/v1/auth/verify-email?token=") {
				u, err := url.Parse(word)
				if err != nil {
					t.Fatal(err)
				}
				return word, u.Query().Get("token")
			}
		}
	}
	t.Fatal("verification mail missing")
	return "", ""
}

func TestAuthRateLimit(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for i := 0; i < 31; i++ {
		r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{`))
		r.RemoteAddr = "192.0.2.1:1234"
		w := httptest.NewRecorder()
		s.login(w, r)
		want := 400
		if i == 30 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
}
