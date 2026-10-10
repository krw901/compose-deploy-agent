package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDeployLifecycle(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	if err := s.CreateDeploy(DeployRecord{
		DeployID:  "d1",
		Service:   "svc",
		TargetTag: "abc",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateDeploy: %v", err)
	}

	active, err := s.FindActiveDeploy("svc")
	if err != nil {
		t.Fatalf("FindActiveDeploy: %v", err)
	}
	if active.DeployID != "d1" {
		t.Fatalf("expected d1, got %s", active.DeployID)
	}

	if err := s.RecordSnapshot("d1", "old-tag", "sha-old", true); err != nil {
		t.Fatalf("RecordSnapshot: %v", err)
	}
	if err := s.FinishDeploy("d1", DeployOutcome{
		Result:        api.ResultSuccess,
		Rollback:      api.RollbackNone,
		TargetImageID: "sha-new",
		FinishedAt:    now.Add(time.Second),
	}); err != nil {
		t.Fatalf("FinishDeploy: %v", err)
	}

	if _, err := s.FindActiveDeploy("svc"); err != ErrNotFound {
		t.Fatalf("expected no active deploy after finishing, got %v", err)
	}

	rec, err := s.GetDeploy("d1")
	if err != nil {
		t.Fatalf("GetDeploy: %v", err)
	}
	if rec.State != api.StateSucceeded || rec.Result != api.ResultSuccess {
		t.Fatalf("unexpected state=%s result=%s", rec.State, rec.Result)
	}
	if !rec.RunningTag.Valid || rec.RunningTag.String != "old-tag" {
		t.Fatalf("expected running_tag old-tag, got %+v", rec.RunningTag)
	}

	latest, err := s.LatestDeploy("svc")
	if err != nil {
		t.Fatalf("LatestDeploy: rec=%v err=%v", latest, err)
	}
	if latest.DeployID != "d1" {
		t.Fatalf("LatestDeploy: rec=%v err=%v", latest, err)
	}
}

func TestMarkInterrupted(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if err := s.CreateDeploy(DeployRecord{
		DeployID:  "d1",
		Service:   "svc",
		TargetTag: "abc",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateDeploy: %v", err)
	}
	if err := s.CreateDeploy(DeployRecord{
		DeployID:  "d2",
		Service:   "svc2",
		TargetTag: "def",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateDeploy: %v", err)
	}
	if err := s.FinishDeploy("d2", DeployOutcome{
		Result:        api.ResultSuccess,
		Rollback:      api.RollbackNone,
		TargetImageID: "sha",
		FinishedAt:    now,
	}); err != nil {
		t.Fatalf("FinishDeploy: %v", err)
	}

	n, err := s.MarkInterrupted(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("MarkInterrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 record marked interrupted, got %d", n)
	}

	rec, err := s.GetDeploy("d1")
	if err != nil {
		t.Fatalf("expected d1 interrupted, got rec=%+v err=%v", rec, err)
	}
	if rec.Result != api.ResultInterrupted {
		t.Fatalf("expected d1 interrupted, got rec=%+v err=%v", rec, err)
	}
	rec2, err := s.GetDeploy("d2")
	if err != nil {
		t.Fatalf("d2 should be untouched, got rec=%+v err=%v", rec2, err)
	}
	if rec2.Result != api.ResultSuccess {
		t.Fatalf("d2 should be untouched, got rec=%+v err=%v", rec2, err)
	}
}

func TestOpenReadOnly_CountActiveDeploys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	if _, err := OpenReadOnly(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist for a missing database, got %v", err)
	}

	writer, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { writer.Close() })
	now := time.Now()
	for _, id := range []string{"d1", "d2", "d3"} {
		if err := writer.CreateDeploy(DeployRecord{
			DeployID:  id,
			Service:   "svc-" + id,
			TargetTag: "abc",
			StartedAt: now,
		}); err != nil {
			t.Fatalf("CreateDeploy %s: %v", id, err)
		}
	}
	if err := writer.FinishDeploy("d3", DeployOutcome{
		Result:        api.ResultSuccess,
		Rollback:      api.RollbackNone,
		TargetImageID: "sha",
		FinishedAt:    now,
	}); err != nil {
		t.Fatalf("FinishDeploy: %v", err)
	}

	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	n, err := reader.CountActiveDeploys()
	if err != nil {
		t.Fatalf("CountActiveDeploys: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 active deploys, got %d", n)
	}
	if err := reader.CreateDeploy(DeployRecord{
		DeployID:  "d4",
		Service:   "svc",
		TargetTag: "abc",
		StartedAt: now,
	}); err == nil {
		t.Fatal("expected write through a read-only store to fail")
	}
}

func TestLastHealthy(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetLastHealthy("svc"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound before any write, got %v", err)
	}

	now := time.Now()
	if err := s.SetLastHealthy("svc", "abc", "sha-abc", now); err != nil {
		t.Fatalf("SetLastHealthy: %v", err)
	}
	rec, err := s.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("unexpected record: %+v err=%v", rec, err)
	}
	if rec.Tag != "abc" || rec.ImageID != "sha-abc" {
		t.Fatalf("unexpected record: %+v err=%v", rec, err)
	}

	// Upsert overwrites.
	if err := s.SetLastHealthy("svc", "def", "sha-def", now.Add(time.Hour)); err != nil {
		t.Fatalf("SetLastHealthy (update): %v", err)
	}
	rec, err = s.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("unexpected record after update: %+v err=%v", rec, err)
	}
	if rec.Tag != "def" || rec.ImageID != "sha-def" {
		t.Fatalf("unexpected record after update: %+v err=%v", rec, err)
	}
}

func TestCertRegistrationAndRevocation(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	if err := s.RegisterCert("fp1", "ci-lss-web", now, now.Add(365*24*time.Hour)); err != nil {
		t.Fatalf("RegisterCert: %v", err)
	}

	rec, err := s.GetCert("fp1")
	if err != nil {
		t.Fatalf("GetCert: %v", err)
	}
	if rec.Revoked() {
		t.Fatalf("expected not revoked initially")
	}

	if err := s.RevokeCert("fp1"); err != nil {
		t.Fatalf("RevokeCert: %v", err)
	}
	rec, err = s.GetCert("fp1")
	if err != nil {
		t.Fatalf("GetCert after revoke: %v", err)
	}
	if !rec.Revoked() {
		t.Fatalf("expected revoked after RevokeCert")
	}

	// Revoking again must be idempotent, not error.
	if err := s.RevokeCert("fp1"); err != nil {
		t.Fatalf("RevokeCert (idempotent): %v", err)
	}

	if _, err := s.GetCert("does-not-exist"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExpiringWithin(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	if err := s.RegisterCert("soon", "a", now, now.Add(10*24*time.Hour)); err != nil {
		t.Fatalf("RegisterCert: %v", err)
	}
	if err := s.RegisterCert("far", "b", now, now.Add(300*24*time.Hour)); err != nil {
		t.Fatalf("RegisterCert: %v", err)
	}
	if err := s.RegisterCert("revoked-soon", "c", now, now.Add(10*24*time.Hour)); err != nil {
		t.Fatalf("RegisterCert: %v", err)
	}
	if err := s.RevokeCert("revoked-soon"); err != nil {
		t.Fatalf("RevokeCert: %v", err)
	}

	expiring, err := s.ExpiringWithin(30 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("ExpiringWithin: %v", err)
	}
	if len(expiring) != 1 || expiring[0].Fingerprint != "soon" {
		t.Fatalf("expected only 'soon' to be reported, got %+v", expiring)
	}
}
