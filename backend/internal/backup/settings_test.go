package backup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
)

func TestSealedPassphraseRoundTrip(t *testing.T) {
	sealed, err := sealPassphrase(testDBKey, "correct horse")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed, "correct horse") {
		t.Fatal("sealed value must not contain the passphrase")
	}
	got, err := openPassphrase(testDBKey, sealed)
	if err != nil || got != "correct horse" {
		t.Fatalf("open = %q, %v; want the original passphrase", got, err)
	}
	if _, err := openPassphrase("another-key", sealed); err == nil {
		t.Fatal("a different DB_KEY must not open the passphrase")
	}
}

func TestSaveEncryptionRules(t *testing.T) {
	conn := openTestDB(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	pass := func(s string) *string { return &s }

	tx, _ := conn.BeginTx(ctx, nil)
	if err := SaveEncryption(ctx, tx, testDBKey, true, nil, now); err != ErrPassphraseNotSet {
		t.Fatalf("encrypt without a passphrase: err = %v, want ErrPassphraseNotSet", err)
	}
	tx.Rollback()

	tx, _ = conn.BeginTx(ctx, nil)
	if err := SaveEncryption(ctx, tx, testDBKey, true, pass("short"), now); err != ErrPassphraseTooShort {
		t.Fatalf("short passphrase: err = %v, want ErrPassphraseTooShort", err)
	}
	tx.Rollback()

	tx, _ = conn.BeginTx(ctx, nil)
	if err := SaveEncryption(ctx, tx, testDBKey, true, pass("correct horse"), now); err != nil {
		t.Fatalf("save encryption: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	st, err := ReadSettings(ctx, conn)
	if err != nil || !st.Encrypt || !st.PassphraseSet {
		t.Fatalf("settings = %+v, %v; want encrypt and a passphrase set", st, err)
	}
	if p, err := activePassphrase(ctx, conn, testDBKey); err != nil || p != "correct horse" {
		t.Fatalf("active passphrase = %q, %v", p, err)
	}

	// Turning encryption off keeps the stored passphrase, so it does not have to be typed again.
	tx, _ = conn.BeginTx(ctx, nil)
	if err := SaveEncryption(ctx, tx, testDBKey, false, nil, now); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	tx.Commit()
	st, _ = ReadSettings(ctx, conn)
	if st.Encrypt || !st.PassphraseSet {
		t.Fatalf("after turning off: %+v; want encrypt off and the passphrase kept", st)
	}

	// An empty string clears the stored passphrase.
	tx, _ = conn.BeginTx(ctx, nil)
	if err := SaveEncryption(ctx, tx, testDBKey, false, pass(""), now); err != nil {
		t.Fatalf("clear: %v", err)
	}
	tx.Commit()
	if st, _ = ReadSettings(ctx, conn); st.PassphraseSet {
		t.Fatal("an empty passphrase must clear the stored one")
	}
}

func TestScheduledRunRecordsUnreadablePassphrase(t *testing.T) {
	conn := openTestDB(t)
	ctx := t.Context()
	dir := t.TempDir()
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.Local)

	tx, _ := conn.BeginTx(ctx, nil)
	p := "correct horse"
	if err := SaveEncryption(ctx, tx, testDBKey, true, &p, now); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	// The scheduler is given a different DB_KEY, so the stored passphrase cannot be opened.
	s := NewScheduler(conn, clock.FakeClock{Time: now}, quietLogger(), dir, "a-different-key", filepath.Join(dir, "no-avatars"))
	s.Tick(ctx)
	s.Tick(ctx)

	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = ? AND status = 'failed'`, kindScheduled); n != 1 {
		t.Fatalf("failed scheduled runs = %d, want 1 (no retry until the next slot)", n)
	}
	archives, _ := filepath.Glob(filepath.Join(dir, "*"+Extension))
	if len(archives) != 0 {
		t.Fatalf("no archive should be written when the passphrase cannot be read, found %d", len(archives))
	}
}

func TestNextRunIsNextTwoAM(t *testing.T) {
	afternoon := time.Date(2026, 10, 21, 15, 0, 0, 0, time.Local)
	want := time.Date(2026, 10, 22, 2, 0, 0, 0, time.Local)
	if got := NextRun(afternoon, 2); !got.Equal(want) {
		t.Fatalf("next run = %v, want %v", got, want)
	}
	early := time.Date(2026, 10, 21, 1, 0, 0, 0, time.Local)
	if got := NextRun(early, 2); !got.Equal(time.Date(2026, 10, 21, 2, 0, 0, 0, time.Local)) {
		t.Fatalf("next run before 2am = %v", got)
	}
}

func TestSaveScheduleRejectsOutOfRange(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name string
		hour int
		keep int
	}{
		{"hour below 0", -1, DefaultKeepCount},
		{"hour above 23", 24, DefaultKeepCount},
		{"keep zero", 2, 0},
		{"keep above 60", 2, 61},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, _ := conn.BeginTx(ctx, nil)
			defer tx.Rollback()
			if err := SaveSchedule(ctx, tx, c.hour, c.keep, now); !errors.Is(err, ErrScheduleInvalid) {
				t.Fatalf("err = %v, want ErrScheduleInvalid", err)
			}
		})
	}
}

func TestSaveScheduleStoresAndReadsBack(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	tx, _ := conn.BeginTx(ctx, nil)
	if err := SaveSchedule(ctx, tx, 5, 12, now); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	got, err := ReadSettings(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleHour != 5 || got.KeepCount != 12 {
		t.Fatalf("settings = %+v, want hour 5 and keep 12", got)
	}
}

func TestSchedulerUsesSavedHour(t *testing.T) {
	ctx := context.Background()
	conn := openTestDB(t)
	dir := t.TempDir()
	tx, _ := conn.BeginTx(ctx, nil)
	if err := SaveSchedule(ctx, tx, 5, DefaultKeepCount, time.Now()); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	// Yesterday's 05:00 slot already has a run, so 03:00 is not a missed slot.
	seeded := time.Date(2026, 10, 20, 5, 5, 0, 0, time.Local)
	mustInsertRun(t, conn, "ok", seeded, seeded)

	// 03:00 is after the default 2:00 slot but before the saved 5:00 slot, so no backup should run yet.
	s := newTestScheduler(conn, dir, time.Date(2026, 10, 21, 3, 0, 0, 0, time.Local))
	s.Tick(ctx)
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled'`); n != 1 {
		t.Fatalf("runs at 03:00 with a 05:00 schedule = %d, want only the seeded one", n)
	}
	s.clock = clock.FakeClock{Time: time.Date(2026, 10, 21, 5, 30, 0, 0, time.Local)}
	s.Tick(ctx)
	if n := sqlCount(t, conn, `SELECT COUNT(*) FROM backup_runs WHERE kind = 'scheduled' AND status = 'ok'`); n != 2 {
		t.Fatalf("runs at 05:30 with a 05:00 schedule = %d, want 2", n)
	}
}
