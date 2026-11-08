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
