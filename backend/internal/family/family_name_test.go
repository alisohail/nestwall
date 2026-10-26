package family

import (
	"strings"
	"testing"
)

func TestUpdateFamilyRenamesAndAudits(t *testing.T) {
	f := newFixture(t)

	rec := call(t, f.h.UpdateFamily, f.fam.MemberID, "", UpdateFamilyRequest{FamilyName: "  The Okafors  "})
	expectStatus(t, rec, 200)
	var resp UpdateFamilyResponse
	decodeResp(t, rec, &resp)
	if resp.FamilyName != "The Okafors" {
		t.Fatalf("response name = %q, want trimmed name", resp.FamilyName)
	}

	var name string
	if err := f.db.QueryRow(`SELECT name FROM families WHERE id = ?`, f.fam.FamilyID).Scan(&name); err != nil {
		t.Fatalf("read family: %v", err)
	}
	if name != "The Okafors" {
		t.Fatalf("stored name = %q, want %q", name, "The Okafors")
	}

	var audits int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = 'family.renamed'`, f.fam.FamilyID,
	).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("family.renamed audit rows = %d, want 1", audits)
	}
}

func TestUpdateFamilyRejectsBadNames(t *testing.T) {
	cases := map[string]string{
		"empty":    "   ",
		"too long": strings.Repeat("a", 81),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			rec := call(t, f.h.UpdateFamily, f.fam.MemberID, "", UpdateFamilyRequest{FamilyName: value})
			expectStatus(t, rec, 400)
			expectErrorCode(t, rec, "validation_failed")
		})
	}
}
