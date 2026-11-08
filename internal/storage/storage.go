// Package storage persists accounts, sessions and user-owned requests in PostgreSQL.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("record not found")
	ErrEmailTaken = errors.New("email already registered")
)

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	EmailVerified bool      `json:"email_verified"`
	Keywords      []string  `json:"keywords"`
}

type Run struct {
	ID        string
	UserID    string
	ParentID  string
	Status    string
	StartedAt time.Time
	Snapshot  json.RawMessage
	Request   json.RawMessage
}

// Repository always scopes reads to the authenticated user. Tokens are SHA-256 hashes.
type Repository interface {
	Register(context.Context, User, string, string, time.Time) (User, error)
	VerifyEmail(context.Context, string) (User, error)
	ReplaceVerification(context.Context, string, string, time.Time) error
	UpdateUser(context.Context, string, *string, *[]string) (User, error)
	UserByEmail(context.Context, string) (User, string, error)
	CreateSession(context.Context, string, string, time.Time) error
	SessionUser(context.Context, string) (User, error)
	DeleteSession(context.Context, string) error
	CreateRun(context.Context, Run) error
	UpdateRun(context.Context, Run) error
	GetRun(context.Context, string, string) (Run, error)
	ListRuns(context.Context, string, string) ([]Run, error)
	RecoverRuns(context.Context) error
	Ping(context.Context) error
}
