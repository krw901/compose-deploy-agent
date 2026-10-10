package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by lookup methods when no matching row exists.
var ErrNotFound = errors.New("not found")

// CertRecord mirrors a row of the client_certs table.
type CertRecord struct {
	Fingerprint string
	Name        string
	IssuedAt    time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// Revoked reports whether the certificate has been revoked.
func (c CertRecord) Revoked() bool { return c.RevokedAt != nil }

// RegisterCert inserts a newly issued client certificate's fingerprint.
func (s *Store) RegisterCert(fingerprint, name string, issuedAt, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx(), `
		INSERT INTO client_certs (fingerprint, name, issued_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, NULL)`,
		fingerprint, name, formatTime(issuedAt), formatTime(expiresAt))
	if err != nil {
		return fmt.Errorf("register cert: %w", err)
	}
	return nil
}

// RevokeCert marks a certificate as revoked. It is idempotent.
func (s *Store) RevokeCert(fingerprint string) error {
	res, err := s.db.ExecContext(ctx(), `
		UPDATE client_certs SET revoked_at = ? WHERE fingerprint = ? AND revoked_at IS NULL`,
		formatTime(time.Now()), fingerprint)
	if err != nil {
		return fmt.Errorf("revoke cert: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either already revoked or unknown; caller decides how to report.
		if _, err := s.GetCert(fingerprint); err != nil {
			return err
		}
	}
	return nil
}

// GetCert looks up a single certificate record by fingerprint.
func (s *Store) GetCert(fingerprint string) (*CertRecord, error) {
	row := s.db.QueryRowContext(ctx(), `
		SELECT fingerprint, name, issued_at, expires_at, revoked_at
		FROM client_certs WHERE fingerprint = ?`, fingerprint)
	rec, err := scanCertRow(row)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// ListCerts returns all registered certificates, most recently issued first.
func (s *Store) ListCerts() ([]CertRecord, error) {
	rows, err := s.db.QueryContext(ctx(), `
		SELECT fingerprint, name, issued_at, expires_at, revoked_at
		FROM client_certs ORDER BY issued_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list certs: %w", err)
	}
	defer rows.Close()

	var out []CertRecord
	for rows.Next() {
		rec, err := scanCertCols(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// ExpiringWithin returns non-revoked certificates whose ExpiresAt falls
// within the given horizon from now. Used for the expiry warning.
func (s *Store) ExpiringWithin(horizon time.Duration) ([]CertRecord, error) {
	all, err := s.ListCerts()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(horizon)
	var out []CertRecord
	for _, c := range all {
		if !c.Revoked() && c.ExpiresAt.Before(deadline) {
			out = append(out, c)
		}
	}
	return out, nil
}

// scanner is the single-method subset of *sql.Row and *sql.Rows used to scan one cert.
type scanner interface {
	Scan(dest ...any) error
}

func scanCertRow(row *sql.Row) (*CertRecord, error) {
	rec, err := scanCertGeneric(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rec, err
}

func scanCertCols(row *sql.Rows) (*CertRecord, error) {
	return scanCertGeneric(row)
}

func scanCertGeneric(row scanner) (*CertRecord, error) {
	var (
		rec       CertRecord
		issuedAt  string
		expiresAt string
		revokedAt sql.NullString
	)
	if err := row.Scan(&rec.Fingerprint, &rec.Name, &issuedAt, &expiresAt, &revokedAt); err != nil {
		return nil, err
	}
	t, err := parseTime(issuedAt)
	if err != nil {
		return nil, fmt.Errorf("parse issued_at: %w", err)
	}
	rec.IssuedAt = t
	t, err = parseTime(expiresAt)
	if err != nil {
		return nil, fmt.Errorf("parse expires_at: %w", err)
	}
	rec.ExpiresAt = t
	if revokedAt.Valid {
		t, err := parseTime(revokedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse revoked_at: %w", err)
		}
		rec.RevokedAt = &t
	}
	return &rec, nil
}
