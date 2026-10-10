// Command deploy-agent is the single binary: it dispatches to the serve, setup,
// ca, deploy, update, self-check and self-upgrade subcommands based on
// os.Args[1].
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "setup":
		err = runSetup(os.Args[2:])
	case "ca":
		err = runCA(os.Args[2:])
	case "deploy":
		err = runDeploy(os.Args[2:])
	case "update":
		err = runUpdate(os.Args[2:])
	case "self-check":
		err = runSelfCheck(os.Args[2:])
	case "self-upgrade":
		err = runSelfUpgrade(os.Args[2:])
	case "version":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: deploy-agent <subcommand> [flags]

subcommands:
  serve                        run the mTLS deploy server (systemd-managed)
  serve -t                     test the serve configuration and exit
  setup [--ip <ip>] [--yes]    first-run wizard (VPS, root): serve.env, CA and
                                server certificate, services.yaml, start serve
  ca init --ip <ip>[,<ip|dns>...]
                               initialize the local CA and server certificate
  ca issue -n/--name <name>    issue a client certificate
  ca revoke -f/--fingerprint <fp>
  ca list
  ca reissue-server [--ip <ip>[,<ip|dns>...]]
                               reissue the server certificate; omit --ip to
                               keep the current IP and DNS names
  deploy -e/--endpoint <url> -s/--service <name> -t/--tag <tag>
                                run from the pipeline: submits over mTLS
  update -s/--service <name> -t/--tag <tag>
                                run on the VPS (SSH'd in, no endpoint/mTLS
                                needed): submits to the local serve process
                                via its unix socket
  self-check                   check whether a newer deploy-agent is published
                                (exit 0: up to date, 10: update available)
  self-upgrade [--force]       upgrade this deploy-agent binary in place and
                                restart the systemd service (VPS only, root);
                                not to be confused with update, which updates
                                a managed service
  version`)
}
