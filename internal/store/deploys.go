package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

// DeployRecord mirrors a row of the deploys table, which doubles as both
// the audit log and the backing store for GET /v1/deploys/{id}.
type DeployRecord struct {
	DeployID       string
	Service        string
	TargetTag      string
	RunningTag     sql.NullString
	RunningImageID sql.NullString
	RunningHealthy sql.NullBool
	TargetImageID  sql.NullString
	State          api.State
	Result         api.Result
	Rollback       api.Rollback
	RolledBackTo   sql.NullString
	Message        string
	StartedAt      time.Time
	FinishedAt     *time.Time
}

// CreateDeploy inserts a new deploy record in the "deploying" state.
func (s *Store) CreateDeploy(rec DeployRecord) error {
	_, err := s.db.ExecContext(ctx(), `
		INSERT INTO deploys (deploy_id, service, target_tag, state, started_at)
		VALUES (?, ?, ?, ?, ?)`,
		rec.DeployID, rec.Service, rec.TargetTag, string(api.StateDeploying), formatTime(rec.StartedAt))
	if err != nil {
		return fmt.Errorf("create deploy: %w", err)
	}
	return nil
}

// RecordSnapshot stores the pre-deploy running version snapshot.
func (s *Store) RecordSnapshot(deployID, runningTag, runningImageID string, healthy bool) error {
	_, err := s.db.ExecContext(ctx(), `
		UPDATE deploys SET running_tag = ?, running_image_id = ?, running_healthy = ?
		WHERE deploy_id = ?`,
		nullableString(runningTag), nullableString(runningImageID), boolToInt(healthy), deployID)
	if err != nil {
		return fmt.Errorf("record snapshot: %w", err)
	}
	return nil
}

// DeployOutcome is the terminal result written onto a deploy record.
type DeployOutcome struct {
	Result        api.Result
	Rollback      api.Rollback
	TargetImageID string
	RolledBackTo  string
	Message       string
	FinishedAt    time.Time
}

// FinishDeploy transitions a deploy record to a terminal state.
func (s *Store) FinishDeploy(deployID string, outcome DeployOutcome) error {
	state := api.StateSucceeded
	if outcome.Result != api.ResultSuccess && outcome.Result != api.ResultNoop {
		state = api.StateFailed
	}
	_, err := s.db.ExecContext(ctx(), `
		UPDATE deploys SET state = ?, result = ?, rollback = ?, target_image_id = ?, rolled_back_to = ?, message = ?, finished_at = ?
		WHERE deploy_id = ?`,
		string(state), string(outcome.Result), string(outcome.Rollback),
		nullableString(outcome.TargetImageID), nullableString(outcome.RolledBackTo),
		outcome.Message, formatTime(outcome.FinishedAt), deployID)
	if err != nil {
		return fmt.Errorf("finish deploy: %w", err)
	}
	return nil
}

// GetDeploy fetches a single deploy record by id.
func (s *Store) GetDeploy(deployID string) (*DeployRecord, error) {
	row := s.db.QueryRowContext(ctx(), deploySelectColumns+` WHERE deploy_id = ?`, deployID)
	rec, err := scanDeployRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rec, err
}

// FindActiveDeploy returns the currently in-progress deploy for a service,
// if any (state = deploying). Used to implement reuse and 409 conflict.
func (s *Store) FindActiveDeploy(service string) (*DeployRecord, error) {
	row := s.db.QueryRowContext(ctx(), deploySelectColumns+`
		WHERE service = ? AND state = ? ORDER BY started_at DESC LIMIT 1`,
		service, string(api.StateDeploying))
	rec, err := scanDeployRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rec, err
}

// CountActiveDeploys returns the number of deploys, across all services,
// still in the "deploying" state.
func (s *Store) CountActiveDeploys() (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx(), `SELECT COUNT(*) FROM deploys WHERE state = ?`, string(api.StateDeploying)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active deploys: %w", err)
	}
	return n, nil
}

// LatestDeploy returns the most recent deploy record for a service, of any
// state, or ErrNotFound if the service has never been deployed.
func (s *Store) LatestDeploy(service string) (*DeployRecord, error) {
	row := s.db.QueryRowContext(ctx(), deploySelectColumns+`
		WHERE service = ? ORDER BY started_at DESC LIMIT 1`, service)
	rec, err := scanDeployRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return rec, err
}

// MarkInterrupted marks all deploys still in the "deploying" state as
// interrupted. Called once at startup: a deploy
// left in this state means the agent restarted mid-deploy. Returns the
// number of records updated.
func (s *Store) MarkInterrupted(finishedAt time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx(), `
		UPDATE deploys SET state = ?, result = ?, rollback = ?, message = ?, finished_at = ?
		WHERE state = ?`,
		string(api.StateFailed), string(api.ResultInterrupted), string(api.RollbackNone),
		"agent restarted while this deploy was in progress", formatTime(finishedAt), string(api.StateDeploying))
	if err != nil {
		return 0, fmt.Errorf("mark interrupted: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

const deploySelectColumns = `
	SELECT deploy_id, service, target_tag, running_tag, running_image_id, running_healthy,
	       target_image_id, state, result, rollback, rolled_back_to, message, started_at, finished_at
	FROM deploys`

func scanDeployRow(row *sql.Row) (*DeployRecord, error) {
	var (
		rec        DeployRecord
		state      string
		result     sql.NullString
		rollback   sql.NullString
		message    sql.NullString
		startedAt  string
		finishedAt sql.NullString
	)
	if err := row.Scan(&rec.DeployID, &rec.Service, &rec.TargetTag, &rec.RunningTag, &rec.RunningImageID,
		&rec.RunningHealthy, &rec.TargetImageID, &state, &result, &rollback, &rec.RolledBackTo, &message,
		&startedAt, &finishedAt); err != nil {
		return nil, err
	}
	rec.State = api.State(state)
	if result.Valid {
		rec.Result = api.Result(result.String)
	}
	if rollback.Valid {
		rec.Rollback = api.Rollback(rollback.String)
	}
	rec.Message = message.String
	t, err := parseTime(startedAt)
	if err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	rec.StartedAt = t
	if finishedAt.Valid {
		t, err := parseTime(finishedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse finished_at: %w", err)
		}
		rec.FinishedAt = &t
	}
	return &rec, nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{
		String: s,
		Valid:  true,
	}
}

func boolToInt(b bool) sql.NullBool {
	return sql.NullBool{
		Bool:  b,
		Valid: true,
	}
}
