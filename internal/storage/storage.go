// Package storage persists accounts, sessions and user-owned requests in PostgreSQL.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound    = errors.New("record not found")
	ErrEmailTaken  = errors.New("email already registered")
	ErrSourceTaken = errors.New("source already saved")
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

// Source is one site the user's digest can collect.
type Source struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	Enabled   bool      `json:"enabled"`
	Fetch     bool      `json:"fetch"`
	Article   string    `json:"article"`
	Item      string    `json:"item"`
	Title     string    `json:"title"`
	Link      string    `json:"link"`
	Date      string    `json:"date"`
	Text      string    `json:"text"`
	Items     string    `json:"items"`
	Position  int       `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// Recipient is one address in the user's mailing list.
// Status send means include in "send to all"; skip means do not send.
type Recipient struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// SMTPSettings belongs to one account and is used only for digest delivery.
type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"-"`
	From     string `json:"from"`
}

// Repository always scopes reads to the authenticated user. Tokens are SHA-256 hashes.
type Repository interface {
	Register(context.Context, User, string, string, time.Time) (User, error)
	VerifyEmail(context.Context, string) (User, error)
	ReplaceVerification(context.Context, string, string, time.Time) error
	UpdateUser(context.Context, string, *string, *[]string) (User, error)
	UserByEmail(context.Context, string) (User, string, error)
	ChangePassword(context.Context, string, string, string, string) error
	CreateSession(context.Context, string, string, time.Time) error
	SessionUser(context.Context, string) (User, error)
	DeleteSession(context.Context, string) error
	GetSMTPSettings(context.Context, string) (SMTPSettings, error)
	SaveSMTPSettings(context.Context, string, SMTPSettings) error
	DeleteSMTPSettings(context.Context, string) error
	CreateRun(context.Context, Run) error
	UpdateRun(context.Context, Run) error
	GetRun(context.Context, string, string) (Run, error)
	ListRuns(context.Context, string, string) ([]Run, error)
	ListRecipients(context.Context, string) ([]Recipient, error)
	CreateRecipient(context.Context, string, Recipient) (Recipient, error)
	UpdateRecipient(context.Context, string, string, string) (Recipient, error)
	DeleteRecipient(context.Context, string, string) error
	EnsureSources(context.Context, string, []Source) ([]Source, error)
	CreateSource(context.Context, string, Source) (Source, error)
	UpdateSource(context.Context, string, Source) (Source, error)
	DeleteSource(context.Context, string, string) error
	RecoverRuns(context.Context) error
	Ping(context.Context) error
}
