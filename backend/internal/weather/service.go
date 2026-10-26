package weather

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
)

// Response is the family's weather. Configured is false until a location is known: either the
// family's location or, when the family has none, the viewer's device location. Source says which
// one was used ("family" or "device"). Animation is "calm" or "live" and sets how much the tile
// moves. Forecast is nil when no location is known or the provider could not be reached.
type Response struct {
	Configured      bool      `json:"configured"`
	Source          string    `json:"source,omitempty"`
	LocationName    *string   `json:"location_name"`
	TemperatureUnit string    `json:"temperature_unit"`
	Animation       string    `json:"animation"`
	Forecast        *Forecast `json:"forecast"`
}

// Coordinates is the viewer's device location. It is used only when the family has no location,
// and it is rounded to about 1 km before any lookup, so it is never stored.
type Coordinates struct {
	Latitude  float64
	Longitude float64
}

// Service reads the family's location and returns its forecast. The wall summary and the session
// endpoint both use it.
type Service struct {
	db     *sql.DB
	client *Client
	names  *PlaceNames
	logger *slog.Logger
}

// NewService creates a Service.
func NewService(db *sql.DB, client *Client, logger *slog.Logger) *Service {
	return &Service{db: db, client: client, names: NewPlaceNames("", client.clk), logger: logger}
}

// deviceLabel names the device location. When the lookup fails, the rounded coordinates are the
// label, so the tile still says where the forecast is for.
func (s *Service) deviceLabel(ctx context.Context, latitude, longitude float64) string {
	name, err := s.names.Name(ctx, latitude, longitude)
	if err != nil {
		s.logger.Warn("place name unavailable, showing coordinates")
		return fmt.Sprintf("%.2f, %.2f", latitude, longitude)
	}
	return name
}

// Current returns the family's weather. The family's location wins. When the family has none and
// device is set, the forecast uses the device location. When the provider fails and nothing is
// kept, the error is ErrUnavailable and the response still says whether a location is known.
func (s *Service) Current(ctx context.Context, familyID string, device *Coordinates) (Response, error) {
	var zone, unit, animation string
	var name sql.NullString
	var lat, lon sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT timezone, location_name, latitude, longitude, temperature_unit, weather_animation FROM families WHERE id = ?`,
		familyID).Scan(&zone, &name, &lat, &lon, &unit, &animation)
	if err != nil {
		return Response{}, err
	}

	resp := Response{TemperatureUnit: unit, Animation: animation}
	place := Place{Unit: unit, Timezone: zone}
	switch {
	case lat.Valid && lon.Valid:
		resp.Source = "family"
		if name.Valid {
			resp.LocationName = &name.String
		}
		place.Latitude, place.Longitude = lat.Float64, lon.Float64
	case device != nil:
		resp.Source = "device"
		place.Latitude = math.Round(device.Latitude*100) / 100
		place.Longitude = math.Round(device.Longitude*100) / 100
		label := s.deviceLabel(ctx, place.Latitude, place.Longitude)
		resp.LocationName = &label
	default:
		return resp, nil
	}
	resp.Configured = true

	fc, err := s.client.Forecast(ctx, place)
	if err != nil {
		s.logger.Warn("weather forecast unavailable")
		return resp, err
	}
	if fc.Stale {
		s.logger.Warn("weather forecast unavailable, serving the last answer")
	}
	resp.Forecast = &fc
	return resp, nil
}
