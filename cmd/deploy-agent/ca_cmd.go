package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/krwu/compose-deploy-agent/internal/ca"
	"github.com/krwu/compose-deploy-agent/internal/paths"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

// runCA dispatches `deploy-agent ca <init|issue|revoke|list|reissue-server>`.
// Every subcommand here must only ever run from the CLI, never behind the
// HTTP listener.
func runCA(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: deploy-agent ca <init|issue|revoke|list|reissue-server> [flags]")
	}

	switch args[0] {
	case "init":
		return runCAInit(args[1:])
	case "issue":
		return runCAIssue(args[1:])
	case "revoke":
		return runCARevoke(args[1:])
	case "list":
		return runCAList(args[1:])
	case "reissue-server":
		return runCAReissueServer(args[1:])
	default:
		return fmt.Errorf("unknown `ca` subcommand %q", args[0])
	}
}

const ipFlagUsage = "comma-separated IPs and DNS names clients use to reach the agent " +
	"(server certificate SAN); at least one IP is required"

func caFlagSet(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	dataDir := fs.String("data-dir", paths.DefaultDataDir, "data directory (sqlite db, CA material)")
	return fs, dataDir
}

// splitIPList splits a comma-separated --ip value, trimming spaces and
// dropping empty entries. Validation is left to the ca package.
func splitIPList(s string) []string {
	var ips []string
	for ip := range strings.SplitSeq(s, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips
}

func runCAInit(args []string) error {
	fs, dataDir := caFlagSet("ca init")
	ipFlag := fs.String("ip", "", ipFlagUsage)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ips := splitIPList(*ipFlag)
	if len(ips) == 0 {
		return errors.New("--ip is required")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := ca.NewManager(*dataDir).Init(ips); err != nil {
		return err
	}
	fmt.Printf("CA initialized in %s; server certificate issued for %s\n", *dataDir, strings.Join(ips, ", "))
	return nil
}

func runCAIssue(args []string) error {
	fs, dataDir := caFlagSet("ca issue")
	name := stringFlag(fs, "name", "n", "", "identifier for the issued certificate, e.g. the pipeline name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	st, err := store.Open(paths.DBPath(*dataDir))
	if err != nil {
		return err
	}
	defer st.Close()

	bundlePEM, err := ca.NewManager(*dataDir).IssueClient(*name, st)
	if err != nil {
		return err
	}
	// Printed once to stdout; the operator pastes this into the pipeline's
	// credential store. The agent never persists this bundle.
	fmt.Print(bundlePEM)
	return nil
}

func runCARevoke(args []string) error {
	fs, dataDir := caFlagSet("ca revoke")
	fingerprint := stringFlag(fs, "fingerprint", "f", "", "fingerprint of the certificate to revoke")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fingerprint == "" {
		return fmt.Errorf("--fingerprint is required (see `deploy-agent ca list`)")
	}

	st, err := store.Open(paths.DBPath(*dataDir))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := ca.Revoke(st, *fingerprint); err != nil {
		return err
	}
	fmt.Printf("revoked %s\n", *fingerprint)
	return nil
}

func runCAList(args []string) error {
	fs, dataDir := caFlagSet("ca list")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := store.Open(paths.DBPath(*dataDir))
	if err != nil {
		return err
	}
	defer st.Close()

	certs, err := ca.List(st)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "FINGERPRINT\tNAME\tISSUED_AT\tEXPIRES_AT\tREVOKED_AT")
	for _, c := range certs {
		revoked := "-"
		if c.RevokedAt != nil {
			revoked = c.RevokedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Fingerprint, c.Name,
			c.IssuedAt.Format("2006-01-02T15:04:05Z07:00"), c.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), revoked)
	}
	return tw.Flush()
}

func runCAReissueServer(args []string) error {
	fs, dataDir := caFlagSet("ca reissue-server")
	ipFlag := fs.String("ip", "", ipFlagUsage+"; omit to keep the names on the current server certificate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mgr := ca.NewManager(*dataDir)
	names := splitIPList(*ipFlag)
	if len(names) == 0 {
		var err error
		names, err = currentServerNames(mgr)
		if err != nil {
			return err
		}
	}
	if err := mgr.ReissueServer(names); err != nil {
		return err
	}
	fmt.Printf("server certificate reissued for %s; restart deploy-agent to pick it up\n", strings.Join(names, ", "))
	return nil
}

// currentServerNames reads the IP and DNS SANs of the certificate about to
// be replaced. Those names live on the certificate, so a renewal does not
// need a second copy in a file or the database.
func currentServerNames(mgr *ca.Manager) ([]string, error) {
	ips, dns, err := mgr.ServerNames()
	if err != nil {
		return nil, fmt.Errorf("no --ip given and the current server certificate could not be read: %w", err)
	}
	names := append(ips, dns...)
	if len(names) == 0 {
		return nil, errors.New("current server certificate has no IP or DNS name; pass --ip")
	}
	return names, nil
}
