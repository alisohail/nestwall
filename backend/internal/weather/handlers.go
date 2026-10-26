package weather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

const maxSettingsBody = 4 << 10

// Handlers handles the family weather endpoints.
type Handlers struct {
	db      *sql.DB
	service *Service
	logger  *slog.Logger
}

// NewHandlers creates a Handlers instance.
func NewHandlers(db *sql.DB, service *Service, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, service: service, logger: logger}
}

// Settings is the family's location, temperature unit and weather animation. A null latitude and
// longitude clears the location. Unit is "c" or "f". Animation is "calm" or "live".
type Settings struct {
	LocationName    *string  `json:"location_name"`
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	TemperatureUnit string   `json:"temperature_unit"`
	Animation       string   `json:"animation"`
}

// Forecast handles GET /api/v1/weather. Any signed-in member can read it, so the kid Today and the
// parent dashboard can show it. Optional lat and lon query parameters give the viewer's device
// location, which is used only when the family has no location.
func (h *Handlers) Forecast(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	device, code, msg := ParseDeviceLocation(r)
	if code != "" {
		web.RespondError(w, http.StatusBadRequest, code, msg, nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	resp, err := h.service.Current(ctx, familyID, device)
	if errors.Is(err, ErrUnavailable) {
		web.RespondError(w, http.StatusServiceUnavailable, "weather_unavailable", "Weather is unavailable right now", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to read weather", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

// GetSettings handles GET /api/v1/family/weather-settings.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	cur, err := readSettings(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "failed to read weather settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, cur)
}

// UpdateSettings handles PUT /api/v1/family/weather-settings. The audit log records the unit and
// whether a location is set, never the coordinates.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	actorUserID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_json", "Invalid request body", nil)
		return
	}
	req, code, msg := validateSettings(req)
	if code != "" {
		web.RespondError(w, http.StatusBadRequest, code, msg, nil)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	var familyID string
	err = tx.QueryRowContext(ctx,
		`SELECT fm.family_id FROM family_members fm WHERE fm.id = ? AND fm.status = 'active'`,
		memberID).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE families
		    SET location_name = ?, latitude = ?, longitude = ?, temperature_unit = ?, weather_animation = ?,
		        updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		  WHERE id = ?`,
		req.LocationName, req.Latitude, req.Longitude, req.TemperatureUnit, req.Animation, familyID); err != nil {
		h.internalError(w, "failed to save weather settings", err)
		return
	}

	meta := map[string]any{"temperature_unit": req.TemperatureUnit, "animation": req.Animation, "location_set": req.Latitude != nil}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "family.weather_updated", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, req)
}

// validateSettings trims the name, clears it when no location is set, and checks the ranges. It
// returns an error code and message when the settings are not valid.
func validateSettings(s Settings) (Settings, string, string) {
	if s.LocationName != nil {
		trimmed := strings.TrimSpace(*s.LocationName)
		s.LocationName = &trimmed
	}
	if (s.Latitude == nil) != (s.Longitude == nil) {
		return s, "invalid_location", "Latitude and longitude must be set together"
	}
	if s.Latitude == nil {
		s.LocationName = nil
	} else {
		if *s.Latitude < -90 || *s.Latitude > 90 {
			return s, "invalid_latitude", "Latitude must be between -90 and 90"
		}
		if *s.Longitude < -180 || *s.Longitude > 180 {
			return s, "invalid_longitude", "Longitude must be between -180 and 180"
		}
		if s.LocationName != nil && *s.LocationName == "" {
			s.LocationName = nil
		}
		if s.LocationName != nil && utf8.RuneCountInString(*s.LocationName) > 80 {
			return s, "invalid_location_name", "Location name must be 80 characters or fewer"
		}
	}
	if s.TemperatureUnit != "c" && s.TemperatureUnit != "f" {
		return s, "invalid_temperature_unit", "Temperature unit must be c or f"
	}
	if s.Animation != "calm" && s.Animation != "live" {
		return s, "invalid_animation", "Animation must be calm or live"
	}
	return s, "", ""
}

// ParseDeviceLocation reads the optional lat and lon query parameters. Both or neither must be
// set. The range test is written so NaN is rejected too.
func ParseDeviceLocation(r *http.Request) (*Coordinates, string, string) {
	q := r.URL.Query()
	latRaw, lonRaw := q.Get("lat"), q.Get("lon")
	if latRaw == "" && lonRaw == "" {
		return nil, "", ""
	}
	if latRaw == "" || lonRaw == "" {
		return nil, "invalid_location", "Latitude and longitude must be set together"
	}
	lat, err := strconv.ParseFloat(latRaw, 64)
	if err != nil || !(lat >= -90 && lat <= 90) {
		return nil, "invalid_latitude", "Latitude must be between -90 and 90"
	}
	lon, err := strconv.ParseFloat(lonRaw, 64)
	if err != nil || !(lon >= -180 && lon <= 180) {
		return nil, "invalid_longitude", "Longitude must be between -180 and 180"
	}
	return &Coordinates{Latitude: lat, Longitude: lon}, "", ""
}

func readSettings(ctx context.Context, db *sql.DB, familyID string) (Settings, error) {
	var out Settings
	var name sql.NullString
	var lat, lon sql.NullFloat64
	err := db.QueryRowContext(ctx,
		`SELECT location_name, latitude, longitude, temperature_unit, weather_animation FROM families WHERE id = ?`,
		familyID).Scan(&name, &lat, &lon, &out.TemperatureUnit, &out.Animation)
	if err != nil {
		return Settings{}, err
	}
	if name.Valid {
		out.LocationName = &name.String
	}
	if lat.Valid {
		out.Latitude = &lat.Float64
	}
	if lon.Valid {
		out.Longitude = &lon.Float64
	}
	return out, nil
}

// familyOf returns the family of an active member.
func familyOf(ctx context.Context, db *sql.DB, memberID string) (string, error) {
	var familyID string
	err := db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID).Scan(&familyID)
	return familyID, err
}

func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
