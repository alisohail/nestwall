package family

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"nestwall/backend/internal/platform/ids"
)

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func TestParentCannotRemoveOwnAccount(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID

	rec := call(t, f.h.RemoveMember, actor, actor, nil)
	expectStatus(t, rec, http.StatusConflict)
	expectErrorCode(t, rec, "cannot_remove_self")
	if countRows(t, f.db, `SELECT COUNT(*) FROM family_members WHERE id = ?`, actor) != 1 {
		t.Fatal("own membership was deleted")
	}
}

func TestParentCanRemoveAnotherParent(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	otherParent := addMember(t, f.db, f.fam.FamilyID, parentRoleName)

	expectStatus(t, call(t, f.h.RemoveMember, actor, otherParent, nil), http.StatusNoContent)
	if countRows(t, f.db, `SELECT COUNT(*) FROM family_members WHERE id = ?`, otherParent) != 0 {
		t.Fatal("other parent still exists after delete")
	}
}

func TestRemovingChildDeletesHistoryAndClearsActorReferences(t *testing.T) {
	f := newFixture(t)
	actor := f.fam.MemberID
	child := addMember(t, f.db, f.fam.FamilyID, "Child")

	insertLedger := func(id, member, entryType string, amount int, actorID any, key string) {
		t.Helper()
		if _, err := f.db.Exec(
			`INSERT INTO points_ledger (id, family_id, family_member_id, entry_type, amount, actor_member_id, idempotency_key)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, f.fam.FamilyID, member, entryType, amount, actorID, key,
		); err != nil {
			t.Fatalf("insert ledger row: %v", err)
		}
	}
	childEntry := ids.New()
	parentEntry := ids.New()
	insertLedger(childEntry, child, "task_award", 5, nil, "child-award")
	insertLedger(parentEntry, actor, "adjustment", 3, child, "parent-adjust")

	expectStatus(t, call(t, f.h.RemoveMember, actor, child, nil), http.StatusNoContent)

	if countRows(t, f.db, `SELECT COUNT(*) FROM family_members WHERE id = ?`, child) != 0 {
		t.Fatal("child membership still exists")
	}
	if countRows(t, f.db, `SELECT COUNT(*) FROM points_ledger WHERE id = ?`, childEntry) != 0 {
		t.Fatal("child's ledger row still exists")
	}
	if countRows(t, f.db, `SELECT COUNT(*) FROM points_ledger WHERE id = ? AND actor_member_id IS NULL`, parentEntry) != 1 {
		t.Fatal("parent's ledger row was removed or still points at the deleted child")
	}
	if countRows(t, f.db, `SELECT COUNT(*) FROM ledger_purge_scope`) != 0 {
		t.Fatal("ledger purge scope was not closed after the removal")
	}
	if _, err := f.db.Exec(`UPDATE points_ledger SET amount = 9 WHERE id = ?`, parentEntry); err == nil {
		t.Fatal("ledger accepted an update after the purge closed")
	}

	rec := callListMembers(t, f.h, actor, "include_inactive=true")
	expectStatus(t, rec, http.StatusOK)
	var resp ListMembersResponse
	decodeResp(t, rec, &resp)
	for _, m := range resp.Members {
		if m.ID == child {
			t.Fatal("deleted member still listed")
		}
	}
}

func TestRemovingMemberDeletesPictureFiles(t *testing.T) {
	f := newFixture(t)
	child := addMember(t, f.db, f.fam.FamilyID, "Child")
	avatarID := ids.New()
	if _, err := f.db.Exec(
		`INSERT INTO member_avatars (id, family_id, family_member_id) VALUES (?, ?, ?)`,
		avatarID, f.fam.FamilyID, child,
	); err != nil {
		t.Fatalf("insert avatar: %v", err)
	}
	files := []string{
		filepath.Join(f.h.avatarDir, avatarID+"-256.jpg"),
		filepath.Join(f.h.avatarDir, avatarID+"-96.jpg"),
	}
	for _, p := range files {
		if err := os.WriteFile(p, []byte("jpeg"), 0o600); err != nil {
			t.Fatalf("write picture: %v", err)
		}
	}

	expectStatus(t, call(t, f.h.RemoveMember, f.fam.MemberID, child, nil), http.StatusNoContent)

	for _, p := range files {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("picture %s still on disk after member removal: %v", p, err)
		}
	}
	if countRows(t, f.db, `SELECT COUNT(*) FROM member_avatars WHERE family_member_id = ?`, child) != 0 {
		t.Fatal("member_avatars rows still exist after member removal")
	}
}
