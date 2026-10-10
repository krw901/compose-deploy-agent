package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LastHealthyRecord mirrors a row of the last_healthy table.
type LastHealthyRecord struct {
	Service   string
	Tag       string
	ImageID   string
	UpdatedAt time.Time
}

// GetLastHealthy returns the last confirmed-healthy version for a service,
// or ErrNotFound if none has ever been recorded.
func (s *Store) GetLastHealthy(service string) (*LastHealthyRecord, error) {
	row := s.db.QueryRowContext(ctx(), `
		SELECT service, tag, image_id, updated_at FROM last_healthy WHERE service = ?`, service)
	var rec LastHealthyRecord
	var updatedAt string
	if err := row.Scan(&rec.Service, &rec.Tag, &rec.ImageID, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get last_healthy: %w", err)
	}
	t, err := parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	rec.UpdatedAt = t
	return &rec, nil
}

// SetLastHealthy upserts the last confirmed-healthy version for a service.
// Callers must only invoke this when a deploy succeeded, or when a
// pre-deploy snapshot was confirmed healthy.
func (s *Store) SetLastHealthy(service, tag, imageID string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx(), `
		INSERT INTO last_healthy (service, tag, image_id, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(service) DO UPDATE SET tag = excluded.tag, image_id = excluded.image_id, updated_at = excluded.updated_at`,
		service, tag, imageID, formatTime(updatedAt))
	if err != nil {
		return fmt.Errorf("set last_healthy: %w", err)
	}
	return nil
}
