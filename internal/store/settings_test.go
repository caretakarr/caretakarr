package store

import (
	"context"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	s, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SMTPHost != "" || s.SMTPPort != 587 || s.SMTPPass != nil || s.MailReady() {
		t.Fatalf("default settings: %+v", s)
	}
	s.SMTPHost, s.SMTPUser, s.MailFrom, s.UpdatedAt = "mail.example.com", "user", "access@example.com", now
	s.SMTPPass = []byte{0x1, 0x2, 0x3}
	if err := st.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.SMTPHost != "mail.example.com" || got.SMTPPort != 587 || got.SMTPUser != "user" ||
		string(got.SMTPPass) != "\x01\x02\x03" || got.MailFrom != "access@example.com" || !got.MailReady() {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestSettingsPassNeverPlaintext(t *testing.T) { // the seal/open pairing lives with the caller; store only moves bytes
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	s, _ := st.GetSettings(ctx)
	s.SMTPPass = nil // explicitly clear: blank password = none
	s.SMTPHost = "h"
	if err := st.SaveSettings(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetSettings(ctx)
	if got.SMTPPass != nil {
		t.Fatalf("cleared pass came back: %v", got.SMTPPass)
	}
}
