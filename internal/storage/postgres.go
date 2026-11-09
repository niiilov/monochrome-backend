package storage

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Postgres struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Postgres, error) {
	if url == "" {
		return nil, errors.New("укажите DATABASE_URL для PostgreSQL")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("некорректный DATABASE_URL")
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("не удалось создать подключение PostgreSQL")
	}
	db := &Postgres{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("PostgreSQL недоступен: проверьте DATABASE_URL и доступность базы")
	}
	if err := db.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("миграция PostgreSQL: %w", err)
	}
	return db, nil
}

func (db *Postgres) Close()                         { db.pool.Close() }
func (db *Postgres) Ping(ctx context.Context) error { return db.pool.Ping(ctx) }

func (db *Postgres) migrate(ctx context.Context) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize schema setup when multiple processes start at the same time.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(736402910)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(raw)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (db *Postgres) Register(ctx context.Context, user User, passwordHash, tokenHash string, expires time.Time) (User, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO users(id,email,name,password_hash) VALUES($1,$2,$3,$4) RETURNING created_at`, user.ID, user.Email, user.Name, passwordHash).Scan(&user.CreatedAt)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO email_verifications(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, tokenHash, user.ID, expires); err != nil {
		return User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func (db *Postgres) UserByEmail(ctx context.Context, email string) (User, string, error) {
	var user User
	var hash string
	err := db.pool.QueryRow(ctx, `SELECT id,email,name,created_at,password_hash,email_verified_at IS NOT NULL,keywords FROM users WHERE email=$1`, email).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &hash, &user.EmailVerified, &user.Keywords)
	return user, hash, notFound(err)
}

// ChangePassword checks the previous hash and revokes other sessions atomically.
func (db *Postgres) ChangePassword(ctx context.Context, userID, oldHash, newHash, keepSession string) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res, err := tx.Exec(ctx, `UPDATE users SET password_hash=$3 WHERE id=$1 AND password_hash=$2`, userID, oldHash, newHash)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, userID, keepSession); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (db *Postgres) CreateSession(ctx context.Context, userID, hash string, expires time.Time) error {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND expires_at <= now()`, userID); err != nil {
		return err
	}
	res, err := tx.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) SELECT $1,id,$3 FROM users WHERE id=$2 AND email_verified_at IS NOT NULL`, hash, userID, expires)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (db *Postgres) SessionUser(ctx context.Context, hash string) (User, error) {
	var user User
	err := db.pool.QueryRow(ctx, `SELECT u.id,u.email,u.name,u.created_at,true,u.keywords FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at > now() AND u.email_verified_at IS NOT NULL`, hash).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.EmailVerified, &user.Keywords)
	return user, notFound(err)
}

// VerifyEmail consumes a non-expired token and confirms the account atomically.
func (db *Postgres) VerifyEmail(ctx context.Context, hash string) (User, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var userID string
	if err = tx.QueryRow(ctx, `DELETE FROM email_verifications WHERE token_hash=$1 AND expires_at>now() RETURNING user_id`, hash).Scan(&userID); err != nil {
		return User{}, notFound(err)
	}
	var user User
	err = tx.QueryRow(ctx, `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1 RETURNING id,email,name,created_at,true,keywords`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.EmailVerified, &user.Keywords)
	if err != nil {
		return User{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return user, nil
}

func (db *Postgres) ReplaceVerification(ctx context.Context, userID, hash string, expires time.Time) error {
	res, err := db.pool.Exec(ctx, `INSERT INTO email_verifications(token_hash,user_id,expires_at) SELECT $1,id,$3 FROM users WHERE id=$2 AND email_verified_at IS NULL ON CONFLICT(user_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,expires_at=EXCLUDED.expires_at,created_at=now()`, hash, userID, expires)
	if err == nil && res.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (db *Postgres) DeleteSession(ctx context.Context, hash string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hash)
	return err
}

func (db *Postgres) UpdateUser(ctx context.Context, id string, name *string, keywords *[]string) (User, error) {
	var user User
	err := db.pool.QueryRow(ctx, `UPDATE users SET name=COALESCE($2,name),keywords=COALESCE($3,keywords) WHERE id=$1 RETURNING id,email,name,created_at,email_verified_at IS NOT NULL,keywords`, id, name, keywords).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.EmailVerified, &user.Keywords)
	return user, notFound(err)
}

func (db *Postgres) CreateRun(ctx context.Context, run Run) error {
	_, err := db.pool.Exec(ctx, `INSERT INTO runs(id,user_id,parent_id,status,started_at,snapshot,request) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7)`, run.ID, run.UserID, run.ParentID, run.Status, run.StartedAt, run.Snapshot, run.Request)
	return err
}

func (db *Postgres) UpdateRun(ctx context.Context, run Run) error {
	res, err := db.pool.Exec(ctx, `UPDATE runs SET status=$3,snapshot=$4 WHERE id=$1 AND user_id=$2`, run.ID, run.UserID, run.Status, run.Snapshot)
	if err == nil && res.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (db *Postgres) GetRun(ctx context.Context, userID, id string) (Run, error) {
	var run Run
	err := db.pool.QueryRow(ctx, `SELECT id,user_id,COALESCE(parent_id,''),status,started_at,snapshot,request FROM runs WHERE id=$1 AND user_id=$2`, id, userID).Scan(&run.ID, &run.UserID, &run.ParentID, &run.Status, &run.StartedAt, &run.Snapshot, &run.Request)
	return run, notFound(err)
}

func (db *Postgres) ListRuns(ctx context.Context, userID, status string) ([]Run, error) {
	rows, err := db.pool.Query(ctx, `SELECT id,user_id,COALESCE(parent_id,''),status,started_at,snapshot,request FROM runs WHERE user_id=$1 AND ($2='' OR status=$2) ORDER BY started_at DESC,id DESC`, userID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var run Run
		if err = rows.Scan(&run.ID, &run.UserID, &run.ParentID, &run.Status, &run.StartedAt, &run.Snapshot, &run.Request); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (db *Postgres) ListRecipients(ctx context.Context, userID string) ([]Recipient, error) {
	rows, err := db.pool.Query(ctx, `SELECT id,user_id,email,status,created_at FROM recipients WHERE user_id=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Recipient{}
	for rows.Next() {
		var item Recipient
		if err = rows.Scan(&item.ID, &item.UserID, &item.Email, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

func (db *Postgres) CreateRecipient(ctx context.Context, userID string, item Recipient) (Recipient, error) {
	err := db.pool.QueryRow(ctx, `INSERT INTO recipients(id,user_id,email,status) VALUES($1,$2,$3,$4) RETURNING created_at`, item.ID, userID, item.Email, item.Status).Scan(&item.CreatedAt)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "recipients_user_email_key" {
		return Recipient{}, ErrEmailTaken
	}
	if err != nil {
		return Recipient{}, err
	}
	item.UserID = userID
	return item, nil
}

func (db *Postgres) UpdateRecipient(ctx context.Context, userID, id, status string) (Recipient, error) {
	var item Recipient
	err := db.pool.QueryRow(ctx, `UPDATE recipients SET status=$3 WHERE id=$1 AND user_id=$2 RETURNING id,user_id,email,status,created_at`, id, userID, status).Scan(&item.ID, &item.UserID, &item.Email, &item.Status, &item.CreatedAt)
	return item, notFound(err)
}

func (db *Postgres) DeleteRecipient(ctx context.Context, userID, id string) error {
	res, err := db.pool.Exec(ctx, `DELETE FROM recipients WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (db *Postgres) EnsureSources(ctx context.Context, userID string, defaults []Source) ([]Source, error) {
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var seeded bool
	err = tx.QueryRow(ctx, `SELECT sources_seeded FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&seeded)
	if err != nil {
		return nil, notFound(err)
	}
	if !seeded {
		for i, item := range defaults {
			item.Position = i
			if _, err = tx.Exec(ctx, insertSourceSQL, item.ID, userID, item.Name, item.Kind, item.URL, item.Enabled, item.Fetch, item.Article, item.Item, item.Title, item.Link, item.Date, item.Text, item.Items, item.Position); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET sources_seeded=true WHERE id=$1`, userID); err != nil {
			return nil, err
		}
	}
	list, err := listSources(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return list, nil
}

func (db *Postgres) CreateSource(ctx context.Context, userID string, item Source) (Source, error) {
	err := db.pool.QueryRow(ctx, `INSERT INTO sources(id,user_id,name,kind,url,enabled,"fetch",article,item,title,link,date_sel,text_sel,items,position)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,(SELECT COALESCE(MAX(position),0)+1 FROM sources WHERE user_id=$2))
		RETURNING position,created_at`, item.ID, userID, item.Name, item.Kind, item.URL, item.Enabled, item.Fetch, item.Article, item.Item, item.Title, item.Link, item.Date, item.Text, item.Items).Scan(&item.Position, &item.CreatedAt)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "sources_user_url_key" {
		return Source{}, ErrSourceTaken
	}
	if err != nil {
		return Source{}, err
	}
	item.UserID = userID
	return item, nil
}

func (db *Postgres) UpdateSource(ctx context.Context, userID string, item Source) (Source, error) {
	err := db.pool.QueryRow(ctx, `UPDATE sources SET name=$3,kind=$4,url=$5,enabled=$6,"fetch"=$7,article=$8,item=$9,title=$10,link=$11,date_sel=$12,text_sel=$13,items=$14
		WHERE id=$1 AND user_id=$2
		RETURNING id,user_id,name,kind,url,enabled,"fetch",article,item,title,link,date_sel,text_sel,items,position,created_at`,
		item.ID, userID, item.Name, item.Kind, item.URL, item.Enabled, item.Fetch, item.Article, item.Item, item.Title, item.Link, item.Date, item.Text, item.Items).Scan(
		&item.ID, &item.UserID, &item.Name, &item.Kind, &item.URL, &item.Enabled, &item.Fetch, &item.Article, &item.Item, &item.Title, &item.Link, &item.Date, &item.Text, &item.Items, &item.Position, &item.CreatedAt)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "sources_user_url_key" {
		return Source{}, ErrSourceTaken
	}
	return item, notFound(err)
}

func (db *Postgres) DeleteSource(ctx context.Context, userID, id string) error {
	res, err := db.pool.Exec(ctx, `DELETE FROM sources WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if res.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

const insertSourceSQL = `INSERT INTO sources(id,user_id,name,kind,url,enabled,"fetch",article,item,title,link,date_sel,text_sel,items,position)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`

type sourceQuery interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func listSources(ctx context.Context, q sourceQuery, userID string) ([]Source, error) {
	rows, err := q.Query(ctx, `SELECT id,user_id,name,kind,url,enabled,"fetch",article,item,title,link,date_sel,text_sel,items,position,created_at FROM sources WHERE user_id=$1 ORDER BY position,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Source{}
	for rows.Next() {
		var item Source
		if err = rows.Scan(&item.ID, &item.UserID, &item.Name, &item.Kind, &item.URL, &item.Enabled, &item.Fetch, &item.Article, &item.Item, &item.Title, &item.Link, &item.Date, &item.Text, &item.Items, &item.Position, &item.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, rows.Err()
}

// RecoverRuns is for the single worker process that owns the runs volume.
func (db *Postgres) RecoverRuns(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `UPDATE runs SET status='failed', snapshot=snapshot || jsonb_build_object('status','failed','error','сервер перезапущен до конца прогона','finished_at',to_char(now() AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')) WHERE status='running'`)
	return err
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
