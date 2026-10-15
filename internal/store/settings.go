// App settings (email delivery). The caller seals SMTP_PASS; SaveSettings never sees plaintext.
package store

import (
	"context"
	"fmt"
)

type Settings struct {
	SMTPHost  string
	SMTPPort  int
	SMTPUser  string
	SMTPPass  []byte // secretbox blob, nil = none
	MailFrom  string
	UpdatedAt string
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var st Settings
	var pass *[]byte
	err := s.q.QueryRowContext(ctx,
		"SELECT smtp_host, smtp_port, smtp_user, smtp_pass, mail_from, updated_at FROM settings WHERE id=1").
		Scan(&st.SMTPHost, &st.SMTPPort, &st.SMTPUser, &pass, &st.MailFrom, &st.UpdatedAt)
	if err != nil {
		return st, fmt.Errorf("read settings: %w", err)
	}
	if pass != nil {
		st.SMTPPass = *pass
	}
	return st, nil
}

func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	if _, err := s.q.ExecContext(ctx, `UPDATE settings SET smtp_host=?, smtp_port=?, smtp_user=?, smtp_pass=?, mail_from=?, updated_at=? WHERE id=1`,
		st.SMTPHost, st.SMTPPort, st.SMTPUser, st.SMTPPass, st.MailFrom, st.UpdatedAt); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// MailReady is true when the row can actually deliver: host and sender set.
func (st Settings) MailReady() bool { return st.SMTPHost != "" && st.MailFrom != "" }
