package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// DefaultPostDeployTimeout applies when post_deploy omits timeout.
	DefaultPostDeployTimeout = 3 * time.Minute
	// MaxPostDeployTimeout caps post_deploy.timeout so that a rollback
	// still fits inside the agent's fixed 10-minute per-deploy budget.
	MaxPostDeployTimeout = 5 * time.Minute
)

// userPartPattern matches one side of a "user[:group]" exec user. It
// forbids a leading "-" so the value can never be parsed as a CLI flag.
var userPartPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)

// composeServicePattern matches compose service names.
var composeServicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// PostDeploy is a fixed command run with `docker compose exec -T -u <user>
// <service> <args...>` after a new version passes readiness. Every field
// comes from services.yaml only; nothing in a deploy request can change it.
type PostDeploy struct {
	// Service is the compose service to exec into. Defaults to the
	// managed service's compose service.
	Service string `yaml:"service,omitempty"`
	// User is the exec user ("name", "uid", "name:group"). Required, and
	// never root: running as root is refused at load time.
	User string `yaml:"user"`
	// Args is the argv executed in the container, without any shell.
	Args []string `yaml:"args"`
	// Timeout bounds the command; on expiry the process is killed and the
	// deploy fails (and rolls back). Defaults to DefaultPostDeployTimeout.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

func validatePostDeploy(pd PostDeploy) error {
	if !composeServicePattern.MatchString(pd.Service) {
		return fmt.Errorf("service %q is not a valid compose service name", pd.Service)
	}
	err := validateExecUser(pd.User)
	if err != nil {
		return err
	}
	if len(pd.Args) == 0 {
		return errors.New("args is required")
	}
	if strings.HasPrefix(pd.Args[0], "-") {
		return fmt.Errorf("args[0] %q must be a command, not a flag", pd.Args[0])
	}
	for i, arg := range pd.Args {
		if arg == "" {
			return fmt.Errorf("args[%d] is empty", i)
		}
		if strings.ContainsFunc(arg, unicode.IsControl) {
			return fmt.Errorf("args[%d] contains control characters", i)
		}
	}
	if pd.Timeout <= 0 || pd.Timeout > MaxPostDeployTimeout {
		return fmt.Errorf("timeout %s must be > 0 and <= %s", pd.Timeout, MaxPostDeployTimeout)
	}
	return nil
}

// validateExecUser requires an explicit non-root exec user. Both the user
// and the optional group are checked, so "0", "root", "root:x", "0:0",
// "app:root" and "app:0" are all refused.
func validateExecUser(user string) error {
	if user == "" {
		return errors.New("user is required: post_deploy may not run as the image's default user (often root)")
	}
	parts := strings.Split(user, ":")
	if len(parts) > 2 {
		return fmt.Errorf("user %q must be \"user\" or \"user:group\"", user)
	}
	for _, part := range parts {
		if !userPartPattern.MatchString(part) {
			return fmt.Errorf("user %q contains an invalid name %q", user, part)
		}
		if isRootID(part) {
			return fmt.Errorf("user %q runs as root; post_deploy must use a non-root user and group", user)
		}
	}
	return nil
}

func isRootID(name string) bool {
	if name == "root" {
		return true
	}
	id, err := strconv.ParseUint(name, 10, 64)
	if err != nil {
		return false
	}
	return id == 0
}
