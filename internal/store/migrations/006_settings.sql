-- App-level settings (DESIGN §: email delivery). One row, same pattern as the single-row totp.
-- smtp_pass is secretbox-sealed with CARETAKARR_SECRET_KEY; NULL = no password.
CREATE TABLE settings (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  smtp_host  TEXT NOT NULL DEFAULT '',
  smtp_port  INTEGER NOT NULL DEFAULT 587,
  smtp_user  TEXT NOT NULL DEFAULT '',
  smtp_pass  BLOB,
  mail_from  TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO settings VALUES (1, '', 587, '', NULL, '', '');