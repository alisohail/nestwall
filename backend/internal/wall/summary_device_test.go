package wall

import (
	"encoding/json"
	"net/http"
	"testing"
)

// fetchWithQuery reads the summary with extra query parameters, such as the device location.
func fetchWithQuery(t *testing.T, s summaryFx, query string) summaryResponse {
	t.Helper()
	rec := send(s.mux, http.MethodGet, "/api/v1/wall/summary"+query, "", s.cookie, deviceRemote)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary%s: status %d, body %s", query, rec.Code, rec.Body.String())
	}
	var out summaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	return out
}

func TestSummaryCarriesChildPictureWhenShown(t *testing.T) {
	s := newSummaryFx(t)
	s.setOptions(t, summaryOptions{ShowPictures: true})
	id := addPicture(t, s, s.childMemberID)

	c := childByID(t, s.fetch(t), s.childMemberID)
	if c.AvatarID == nil || *c.AvatarID != id {
		t.Fatalf("avatar_id = %v, want %s", c.AvatarID, id)
	}
}

func TestSummaryHidesChildPictureWhenOff(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s summaryFx)
	}{
		{"device hides pictures", func(t *testing.T, s summaryFx) {
			mustExec(t, s.db, `UPDATE wall_devices SET show_pictures = 0 WHERE id = ?`, s.deviceID)
		}},
		{"family hides avatars on the wall", func(t *testing.T, s summaryFx) {
			mustExec(t, s.db, `UPDATE families SET show_avatars_on_wall = 0 WHERE id = ?`, s.familyID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSummaryFx(t)
			s.setOptions(t, summaryOptions{ShowPictures: true})
			addPicture(t, s, s.childMemberID)
			tc.setup(t, s)

			if c := childByID(t, s.fetch(t), s.childMemberID); c.AvatarID != nil {
				t.Fatalf("avatar_id = %s, want none", *c.AvatarID)
			}
		})
	}
}

func TestSummaryUsesDeviceLocationWhenFamilyHasNone(t *testing.T) {
	s := newSummaryFx(t)
	w := fetchWithQuery(t, s, "?lat=51.5&lon=-0.12").Weather
	if w == nil || !w.Configured || w.Source != "device" || w.Forecast == nil {
		t.Fatalf("weather = %+v, want a device-located forecast", w)
	}
}

func TestSummaryPrefersFamilyLocationOverDevice(t *testing.T) {
	s := newSummaryFx(t)
	s.setLondon(t)
	w := fetchWithQuery(t, s, "?lat=40.7&lon=-74.0").Weather
	if w == nil || w.Source != "family" || w.LocationName == nil || *w.LocationName != "London" {
		t.Fatalf("weather = %+v, want the family location", w)
	}
}

func TestSummaryIgnoresMalformedDeviceLocation(t *testing.T) {
	for _, query := range []string{"?lat=51.5", "?lat=abc&lon=1", "?lat=95&lon=0", "?lat=0&lon=200"} {
		t.Run(query, func(t *testing.T) {
			s := newSummaryFx(t)
			w := fetchWithQuery(t, s, query).Weather
			if w == nil || w.Configured {
				t.Fatalf("weather = %+v, want not configured and the summary still served", w)
			}
		})
	}
}
