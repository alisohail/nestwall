package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"nestwall/backend/internal/platform/clock"
)

func TestShareCustomNoteStoredAndReturned(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const text = "Well done on the spelling test!"
	send := func(req ShareRequest, key string) (ShareResult, error) {
		req.ToMemberID = bo.memberID
		req.Amount = 5
		return SendShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
			ana.memberID, ana.userID, req, key)
	}

	res, err := send(ShareRequest{NoteText: text}, "t1")
	if err != nil {
		t.Fatalf("custom note: %v", err)
	}
	if res.NoteText == nil || *res.NoteText != text || res.NoteKey != nil {
		t.Fatalf("result note = key %v text %v, want custom text only", res.NoteKey, res.NoteText)
	}
	var stored string
	if err := f.db.QueryRow(`SELECT note_text FROM point_shares WHERE id = ?`, res.ID).Scan(&stored); err != nil {
		t.Fatalf("read note_text: %v", err)
	}
	if stored != text {
		t.Fatalf("stored note_text = %q, want %q", stored, text)
	}

	preset, err := send(ShareRequest{NoteKey: "good_job"}, "t2")
	if err != nil {
		t.Fatalf("preset note: %v", err)
	}
	if preset.NoteKey == nil || *preset.NoteKey != "good_job" || preset.NoteText != nil {
		t.Fatalf("preset result = key %v text %v, want preset only", preset.NoteKey, preset.NoteText)
	}

	none, err := send(ShareRequest{}, "t3")
	if err != nil {
		t.Fatalf("no note: %v", err)
	}
	if none.NoteKey != nil || none.NoteText != nil {
		t.Fatalf("no-note result = key %v text %v, want none", none.NoteKey, none.NoteText)
	}
}

func TestShareCustomNoteReplayAndConflict(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const text = "Thanks for the ride"
	req := ShareRequest{ToMemberID: bo.memberID, Amount: 5, NoteText: text}
	first, err := SendShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		ana.memberID, ana.userID, req, "r1")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := SendShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		ana.memberID, ana.userID, req, "r1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.ID != first.ID {
		t.Fatalf("replay = %+v, want Replayed with id %s", again, first.ID)
	}

	changed := req
	changed.NoteText = "Something else"
	if _, err := SendShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		ana.memberID, ana.userID, changed, "r1"); !errors.Is(err, ErrShareIdemConflict) {
		t.Fatalf("different text: err = %v, want ErrShareIdemConflict", err)
	}
	dropped := req
	dropped.NoteText = ""
	if _, err := SendShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		ana.memberID, ana.userID, dropped, "r1"); !errors.Is(err, ErrShareIdemConflict) {
		t.Fatalf("no text: err = %v, want ErrShareIdemConflict", err)
	}
}

func TestShareCustomNoteHTTP(t *testing.T) {
	f := withShareRoutes(t, newCatFixture(t))
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const path = "/api/v1/points/share"
	to := fmt.Sprintf(`"to_member_id":%q,"amount":5`, bo.memberID)

	rejected := []struct {
		name string
		body string
		key  string
	}{
		{"201 characters", `{` + to + `,"note_text":"` + strings.Repeat("a", 201) + `"}`, "n1"},
		{"preset and text", `{` + to + `,"note_key":"good_job","note_text":"Nice"}`, "n2"},
	}
	for _, c := range rejected {
		rec := f.do(ana, http.MethodPost, path, c.body, map[string]string{"Idempotency-Key": c.key})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %s", c.name, rec.Code, rec.Body.String())
		}
	}

	// 200 characters is the limit. Multibyte characters count once each, so this is 400 bytes.
	long := strings.Repeat("é", 200)
	rec := f.do(ana, http.MethodPost, path, `{`+to+`,"note_text":"`+long+`"}`, map[string]string{"Idempotency-Key": "n3"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("200 characters: status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created ShareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if created.NoteText == nil || *created.NoteText != long {
		t.Fatalf("response note_text not returned as sent")
	}

	// Whitespace-only text is trimmed to nothing, so the share has no note.
	rec = f.do(ana, http.MethodPost, path, `{`+to+`,"note_text":"   "}`, map[string]string{"Idempotency-Key": "n4"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("blank text: status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var blank ShareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &blank); err != nil {
		t.Fatalf("decode blank share: %v", err)
	}
	if blank.NoteText != nil || blank.NoteKey != nil {
		t.Fatalf("blank text result = key %v text %v, want no note", blank.NoteKey, blank.NoteText)
	}
}
