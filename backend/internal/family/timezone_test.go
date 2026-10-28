package family

import "testing"

func TestUpdateTimezoneStoresAndAudits(t *testing.T) {
	f := newFixture(t)

	rec := call(t, f.h.UpdateTimezone, f.fam.MemberID, "", UpdateTimezoneRequest{Timezone: "Europe/Berlin"})
	expectStatus(t, rec, 200)

	var stored string
	if err := f.db.QueryRow(`SELECT timezone FROM families WHERE id = ?`, f.fam.FamilyID).Scan(&stored); err != nil {
		t.Fatalf("read family: %v", err)
	}
	if stored != "Europe/Berlin" {
		t.Fatalf("stored timezone = %q, want %q", stored, "Europe/Berlin")
	}

	var audits int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = 'family.timezone_updated'`, f.fam.FamilyID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("family.timezone_updated audit rows = %d, want 1", audits)
	}
}

func TestUpdateTimezoneRejectsInvalidZones(t *testing.T) {
	for _, value := range []string{"", "Mars/Olympus", "europe/berlin x"} {
		t.Run(value, func(t *testing.T) {
			f := newFixture(t)
			rec := call(t, f.h.UpdateTimezone, f.fam.MemberID, "", UpdateTimezoneRequest{Timezone: value})
			expectStatus(t, rec, 400)
			expectErrorCode(t, rec, "invalid_timezone")
		})
	}
}
