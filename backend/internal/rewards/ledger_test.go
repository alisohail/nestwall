package rewards

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// postEntry posts one entry in its own transaction and commits it.
func postEntry(t *testing.T, conn *sql.DB, e Entry) (Posted, error) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	posted, err := Post(context.Background(), tx, testNow, e)
	if err != nil {
		return Posted{}, err
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return posted, nil
}

func bonusEntry(f testhelper.Family, amount int, key string) Entry {
	return Entry{
		FamilyID:       f.FamilyID,
		MemberID:       f.MemberID,
		Type:           Bonus,
		Amount:         amount,
		IdempotencyKey: key,
	}
}

func TestPostWritesEntry(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	posted, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if posted.ID == "" || posted.Replayed {
		t.Fatalf("posted = %+v, want a new row", posted)
	}
	if got := memberBalance(t, conn, f); got != 5 {
		t.Fatalf("balance = %d, want 5", got)
	}
}

func TestPostReplayReturnsStoredRow(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	first, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1"))
	if err != nil {
		t.Fatalf("first post: %v", err)
	}
	second, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1"))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.ID != first.ID {
		t.Fatalf("replay = %+v, want replayed row %s", second, first.ID)
	}
	if got := memberBalance(t, conn, f); got != 5 {
		t.Fatalf("balance = %d, want 5 after replay", got)
	}
}

func TestPostConflictingReplayIsRejected(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	if _, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1")); err != nil {
		t.Fatalf("first post: %v", err)
	}
	_, err := postEntry(t, conn, bonusEntry(f, 7, "bonus:1"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
	if got := memberBalance(t, conn, f); got != 5 {
		t.Fatalf("balance = %d, want 5 after conflict", got)
	}
}

func TestBalanceIsSumOfEntries(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	entries := []Entry{
		{FamilyID: f.FamilyID, MemberID: f.MemberID, Type: TaskAward, Amount: 10, IdempotencyKey: "a"},
		{FamilyID: f.FamilyID, MemberID: f.MemberID, Type: Penalty, Amount: -3, IdempotencyKey: "b"},
		// A pending redemption is a negative entry, so it reduces the balance directly.
		{FamilyID: f.FamilyID, MemberID: f.MemberID, Type: Redemption, Amount: -4, IdempotencyKey: "c"},
	}
	for _, e := range entries {
		if _, err := postEntry(t, conn, e); err != nil {
			t.Fatalf("post %s: %v", e.IdempotencyKey, err)
		}
	}
	if got := memberBalance(t, conn, f); got != 3 {
		t.Fatalf("balance = %d, want 3", got)
	}
}

func TestBalanceIsScopedToFamily(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	if _, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1")); err != nil {
		t.Fatalf("post: %v", err)
	}
	other, err := Balance(context.Background(), conn, ids.New(), f.MemberID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if other != 0 {
		t.Fatalf("balance in another family = %d, want 0", other)
	}
}

func TestRejectsWrongSign(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	cases := []Entry{
		{Type: Bonus, Amount: -1},
		{Type: TaskAward, Amount: 0},
		{Type: Penalty, Amount: 2},
		{Type: Redemption, Amount: 2},
		{Type: Reversal, Amount: 0},
	}
	for i, c := range cases {
		c.FamilyID = f.FamilyID
		c.MemberID = f.MemberID
		c.IdempotencyKey = ids.New()
		_, err := postEntry(t, conn, c)
		if !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("case %d (%s, %d): err = %v, want ErrInvalidEntry", i, c.Type, c.Amount, err)
		}
	}
	if got := memberBalance(t, conn, f); got != 0 {
		t.Fatalf("balance = %d, want 0 after rejected entries", got)
	}
}

func TestRejectsBadReferenceAndTransfer(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	cases := []Entry{
		{Type: TaskAward, Amount: 5, ReferenceType: "task_occurrence"},
		{Type: TaskAward, Amount: 5, ReferenceID: ids.New()},
		{Type: TransferOut, Amount: -5},
		{Type: Bonus, Amount: 5, TransferID: ids.New()},
	}
	for i, c := range cases {
		c.FamilyID = f.FamilyID
		c.MemberID = f.MemberID
		c.IdempotencyKey = ids.New()
		_, err := postEntry(t, conn, c)
		if !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("case %d: err = %v, want ErrInvalidEntry", i, err)
		}
	}
}

func TestLedgerIsAppendOnly(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)

	if _, err := postEntry(t, conn, bonusEntry(f, 5, "bonus:1")); err != nil {
		t.Fatalf("post: %v", err)
	}
	if _, err := conn.Exec(`UPDATE points_ledger SET amount = 100 WHERE family_id = ?`, f.FamilyID); err == nil {
		t.Error("update succeeded, want the ledger to block it")
	}
	if _, err := conn.Exec(`DELETE FROM points_ledger WHERE family_id = ?`, f.FamilyID); err == nil {
		t.Error("delete succeeded, want the ledger to block it")
	}
	if got := memberBalance(t, conn, f); got != 5 {
		t.Fatalf("balance = %d, want 5 after blocked changes", got)
	}
}

// postOnce posts one entry in its own transaction. Unlike postEntry it never calls t, so it is safe in a goroutine.
func postOnce(conn *sql.DB, e Entry) error {
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := Post(context.Background(), tx, testNow, e); err != nil {
		return err
	}
	return tx.Commit()
}

func TestConcurrentPostingsKeepBalance(t *testing.T) {
	conn := testhelper.OpenDB(t)
	// Each new SQLCipher connection runs a slow key derivation. A bounded pool keeps
	// writers from waiting past the busy timeout while they open connections.
	conn.SetMaxOpenConns(4)
	f := testhelper.OneFamily(t, conn)

	const writers = 40
	errs := make(chan error, writers*2)
	var wg sync.WaitGroup
	for i := 1; i <= writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e := bonusEntry(f, n, fmt.Sprintf("bonus:concurrent:%d", n))
			for attempt := 0; attempt < 2; attempt++ { // the second attempt is a replay
				if err := postOnce(conn, e); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent post: %v", err)
	}

	want := writers * (writers + 1) / 2
	if got := memberBalance(t, conn, f); got != want {
		t.Fatalf("balance = %d, want %d", got, want)
	}
	var rows int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM points_ledger WHERE family_id = ?`, f.FamilyID).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != writers {
		t.Fatalf("rows = %d, want %d (replays must not add rows)", rows, writers)
	}
}

func TestCrossFamilyMemberIsRefused(t *testing.T) {
	conn := testhelper.OpenDB(t)
	mine := testhelper.OneFamily(t, conn)
	other := testhelper.OneFamily(t, conn)

	_, err := postEntry(t, conn, Entry{
		FamilyID:       mine.FamilyID,
		MemberID:       other.MemberID,
		Type:           Bonus,
		Amount:         5,
		IdempotencyKey: ids.New(),
	})
	if err == nil {
		t.Fatal("post with another family's member succeeded, want a foreign key error")
	}
	if got := memberBalance(t, conn, mine); got != 0 {
		t.Fatalf("balance = %d, want 0 after refused post", got)
	}
}
