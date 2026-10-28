package family

import "testing"

func TestUpdateTimeFormatStoresAndAudits(t *testing.T) {
	f := newFixture(t)

	rec := call(t, f.h.UpdateTimeFormat, f.fam.MemberID, "", UpdateTimeFormatRequest{TimeFormat: "24h"})
	expectStatus(t, rec, 200)

	var stored string
	if err := f.db.QueryRow(`SELECT time_format FROM families WHERE id = ?`, f.fam.FamilyID).Scan(&stored); err != nil {
		t.Fatalf("read family: %v", err)
	}
	if stored != "24h" {
		t.Fatalf("stored time_format = %q, want %q", stored, "24h")
	}

	var audits int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = 'family.time_format_updated'`, f.fam.FamilyID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("family.time_format_updated audit rows = %d, want 1", audits)
	}
}

func TestUpdateTimeFormatRejectsUnknownValues(t *testing.T) {
	for _, value := range []string{"", "24H", "military", "12 h"} {
		t.Run(value, func(t *testing.T) {
			f := newFixture(t)
			rec := call(t, f.h.UpdateTimeFormat, f.fam.MemberID, "", UpdateTimeFormatRequest{TimeFormat: value})
			expectStatus(t, rec, 400)
			expectErrorCode(t, rec, "invalid_time_format")
		})
	}
}
