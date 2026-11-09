package storage

import "context"

func (db *Postgres) GetSMTPSettings(ctx context.Context, userID string) (SMTPSettings, error) {
	var settings SMTPSettings
	err := db.pool.QueryRow(ctx, `SELECT host,port,username,password,sender FROM digest_smtp_settings WHERE user_id=$1`, userID).Scan(&settings.Host, &settings.Port, &settings.Username, &settings.Password, &settings.From)
	return settings, notFound(err)
}

func (db *Postgres) SaveSMTPSettings(ctx context.Context, userID string, settings SMTPSettings) error {
	_, err := db.pool.Exec(ctx, `INSERT INTO digest_smtp_settings(user_id,host,port,username,password,sender) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(user_id) DO UPDATE SET host=EXCLUDED.host,port=EXCLUDED.port,username=EXCLUDED.username,password=EXCLUDED.password,sender=EXCLUDED.sender`, userID, settings.Host, settings.Port, settings.Username, settings.Password, settings.From)
	return err
}

func (db *Postgres) DeleteSMTPSettings(ctx context.Context, userID string) error {
	_, err := db.pool.Exec(ctx, `DELETE FROM digest_smtp_settings WHERE user_id=$1`, userID)
	return err
}
