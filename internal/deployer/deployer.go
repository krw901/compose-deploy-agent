// Package deployer implements the deploy state machine: per-service concurrency control,
// idempotent no-op detection via local image IDs, readiness checking, and
// snapshot-based rollback. It is the only package that touches both
// internal/dockerx and internal/store.
package deployer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/store"
	"github.com/krwu/compose-deploy-agent/internal/tagfmt"
)

// Timing constants, deliberately not configurable
// ("readiness、超时、回滚策略不在清单中配置").
const (
	// TotalDeployTimeout bounds a single deploy end to end.
	TotalDeployTimeout = 10 * time.Minute
	// ReadinessWindow is the total time budget for a readiness check.
	ReadinessWindow = 60 * time.Second
	// ReadinessInterval is how often the readiness check polls docker.
	ReadinessInterval = 2 * time.Second
	// StableWindow is how long a healthcheck-less container must stay
	// running with an unchanged restart count to be considered ready.
	StableWindow = 10 * time.Second
)

// ErrServiceNotFound is returned when the requested service is not present
// in services.yaml (the deploy allowlist).
type ErrServiceNotFound struct {
	Service string
}

func (e ErrServiceNotFound) Error() string {
	return fmt.Sprintf("service %q is not managed by this agent", e.Service)
}

// ErrInvalidTag is returned when the requested tag fails format validation.
type ErrInvalidTag struct {
	Reason error
}

func (e ErrInvalidTag) Error() string { return e.Reason.Error() }
func (e ErrInvalidTag) Unwrap() error { return e.Reason }

// ErrAlreadyDeploying is returned when a different tag is already being
// deployed for the same service.
type ErrAlreadyDeploying struct {
	ActiveDeployID string
	ActiveTag      string
}

func (e ErrAlreadyDeploying) Error() string {
	return fmt.Sprintf("service already deploying tag %q (deploy_id=%s)", e.ActiveTag, e.ActiveDeployID)
}

// activeDeploy tracks the single in-flight deploy for a service. The
// in-memory map, not sqlite, is the concurrency authority: sqlite is a
// supplement used for persistence and querying,
// never for coordinating who gets to run next.
type activeDeploy struct {
	deployID string
	tag      string
}

// Deployer runs the deploy state machine for services described by a
// config.Manifest.
type Deployer struct {
	manifest *config.Manifest
	docker   *dockerx.Client
	store    *store.Store

	mu     sync.Mutex
	active map[string]*activeDeploy

	// Timing knobs. These default to the fixed values above (not
	// configurable per-service via the manifest); tests shrink them so
	// state-machine tests don't block on real wall clock time.
	totalDeployTimeout time.Duration
	readinessWindow    time.Duration
	readinessInterval  time.Duration
	stableWindow       time.Duration
}

// New returns a Deployer for the given manifest, docker client and store.
func New(manifest *config.Manifest, docker *dockerx.Client, s *store.Store) *Deployer {
	return &Deployer{
		manifest:           manifest,
		docker:             docker,
		store:              s,
		active:             map[string]*activeDeploy{},
		totalDeployTimeout: TotalDeployTimeout,
		readinessWindow:    ReadinessWindow,
		readinessInterval:  ReadinessInterval,
		stableWindow:       StableWindow,
	}
}

// SetTimingForTest overrides the deploy timing knobs. It exists only so
// tests can shrink the fixed timing windows and must
// never be called from production code.
func (d *Deployer) SetTimingForTest(total, readinessWindow, readinessInterval, stableWindow time.Duration) {
	d.totalDeployTimeout = total
	d.readinessWindow = readinessWindow
	d.readinessInterval = readinessInterval
	d.stableWindow = stableWindow
}

// StartDeploy validates and, if accepted, asynchronously begins a deploy of
// service to tag. It returns the deploy_id to hand back to the client,
// and whether an existing in-flight deploy was
// reused rather than a new one started.
func (deployer *Deployer) StartDeploy(ctx context.Context, service, tag string) (deployID string, reused bool, err error) {
	return deployer.startDeploy(ctx, service, tag, false)
}

// StartDeployDigest is StartDeploy for services configured with
// image_ref: digest; digest must be "sha256:<64 hex>".
func (deployer *Deployer) StartDeployDigest(ctx context.Context, service, digest string) (string, bool, error) {
	return deployer.startDeploy(ctx, service, digest, true)
}

// Manifest returns the manifest the deployer was built with.
func (deployer *Deployer) Manifest() *config.Manifest {
	return deployer.manifest
}

func (deployer *Deployer) startDeploy(ctx context.Context, service, tag string, isDigest bool) (string, bool, error) {
	svc, ok := deployer.manifest.Lookup(service)
	if !ok {
		return "", false, ErrServiceNotFound{Service: service}
	}
	err := validateRequestedRef(service, svc, tag, isDigest)
	if err != nil {
		return "", false, ErrInvalidTag{Reason: err}
	}

	deployer.mu.Lock()
	if cur, ok := deployer.active[service]; ok {
		if cur.tag == tag {
			deployer.mu.Unlock()
			return cur.deployID, true, nil
		}
		deployer.mu.Unlock()
		return "", false, ErrAlreadyDeploying{
			ActiveDeployID: cur.deployID,
			ActiveTag:      cur.tag,
		}
	}

	id, err := newDeployID()
	if err != nil {
		deployer.mu.Unlock()
		return "", false, fmt.Errorf("generate deploy id: %w", err)
	}
	cur := &activeDeploy{
		deployID: id,
		tag:      tag,
	}
	deployer.active[service] = cur
	deployer.mu.Unlock()

	startedAt := time.Now()
	if err := deployer.store.CreateDeploy(store.DeployRecord{
		DeployID:  id,
		Service:   service,
		TargetTag: tag,
		StartedAt: startedAt,
	}); err != nil {
		deployer.mu.Lock()
		delete(deployer.active, service)
		deployer.mu.Unlock()
		return "", false, fmt.Errorf("persist deploy record: %w", err)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), deployer.totalDeployTimeout)
	go func() {
		defer cancel()
		defer func() {
			deployer.mu.Lock()
			if deployer.active[service] == cur {
				delete(deployer.active, service)
			}
			deployer.mu.Unlock()
		}()
		deployer.run(runCtx, service, svc, tag, id)
	}()

	return id, false, nil
}

// MarkInterruptedOnStartup marks any deploys left in the "deploying" state
// (from a previous process that crashed or was restarted mid-deploy) as
// interrupted. Must be called once during agent startup, before serving
// any requests.
func (d *Deployer) MarkInterruptedOnStartup() (int, error) {
	return d.store.MarkInterrupted(time.Now())
}

// validateRequestedRef checks that the request carries the reference kind
// the service is configured for, in the strict format for that kind. A
// digest-only service never accepts a tag (tags can be re-pointed), and a
// tag service never accepts a digest (its compose file uses "repo:${VAR}").
func validateRequestedRef(service string, svc config.Service, ref string, isDigest bool) error {
	if svc.UsesDigest() != isDigest {
		if svc.UsesDigest() {
			return fmt.Errorf("service %q only accepts image digests (image_ref: digest), not tags", service)
		}
		return fmt.Errorf("service %q deploys by tag; digest is only accepted with image_ref: digest", service)
	}
	return validRefForService(svc, ref)
}

// validRefForService validates ref against svc's image_ref mode.
func validRefForService(svc config.Service, ref string) error {
	if svc.UsesDigest() {
		return tagfmt.ValidateDigest(ref)
	}
	return tagfmt.Validate(ref)
}

func newDeployID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
