package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/credential"
)

// errorBodyLimit caps how much of an error response is kept in the summary.
const errorBodyLimit = 4096

// exitAccepted is submit's sentinel for HTTP 202. It is not a process status.
const exitAccepted ExitCode = -1

// Run submits a deploy request over mTLS and polls until a terminal
// result. Used by `deploy-agent deploy` (the pipeline client).
func Run(ctx context.Context, opts Options) Summary {
	opts = opts.withDefaults()
	summary := Summary{
		Service: opts.Service,
		Tag:     opts.Tag,
	}

	bundle, err := credential.Parse(opts.CredentialPEM)
	if err != nil {
		return fail(summary, ExitRequestFailed, fmt.Sprintf("parse credential: %v", err))
	}
	httpClient, err := newHTTPClient(bundle, opts.Endpoint)
	if err != nil {
		return fail(summary, ExitRequestFailed, fmt.Sprintf("build http client: %v", err))
	}

	return run(ctx, httpClient, opts, summary)
}

// RunLocal submits a deploy request over the agent's local unix socket
// and polls until a terminal result. It needs
// neither an endpoint nor an mTLS credential: reaching the socket file at
// all already proves host access. Used by `deploy-agent update` (the VPS
// operator command).
func RunLocal(ctx context.Context, socketPath string, opts Options) Summary {
	opts = opts.withDefaults()
	opts.Endpoint = localEndpoint
	summary := Summary{
		Service: opts.Service,
		Tag:     opts.Tag,
	}

	httpClient := newUnixHTTPClient(socketPath)

	result := run(ctx, httpClient, opts, summary)
	if result.ExitCode == ExitRequestFailed && strings.Contains(result.Error, "connect to agent failed") {
		result.Error = fmt.Sprintf("%s (is deploy-agent serve running? socket=%s; check: systemctl status deploy-agent)", result.Error, socketPath)
	}
	return result
}

// run implements the shared submit/poll flow used by both Run and RunLocal.
func run(ctx context.Context, httpClient *http.Client, opts Options, summary Summary) Summary {
	deadline := time.Now().Add(opts.PollTimeout)

	deployID, exitCode, msg := submit(ctx, httpClient, opts, deadline)
	if exitCode != exitAccepted {
		return fail(summary, exitCode, msg)
	}
	summary.DeployID = deployID

	return poll(ctx, httpClient, pollJob{
		opts:     opts,
		deployID: deployID,
		deadline: deadline,
		summary:  summary,
	})
}

// submit posts the deploy request, retrying on connection errors (up to
// ConnectRetries) and on 409 already_deploying (until deadline). It
// returns exitCode == exitAccepted on success (202 accepted).
func submit(ctx context.Context, httpClient *http.Client, opts Options, deadline time.Time) (deployID string, exitCode ExitCode, message string) {
	body, _ := json.Marshal(api.DeployRequest{
		Service: opts.Service,
		Tag:     opts.Tag,
	})
	connectAttempts := 0

	for {
		reqCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, opts.Endpoint+"/v1/deploy", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
		var resp *http.Response
		if err == nil {
			resp, err = httpClient.Do(req)
		}
		cancel()

		if err != nil {
			connectAttempts++
			if connectAttempts > opts.ConnectRetries {
				return "", ExitRequestFailed, fmt.Sprintf("connect to agent failed after %d attempts: %v", connectAttempts, err)
			}
			time.Sleep(opts.ConnectBackoff)
			continue
		}

		switch resp.StatusCode {
		case http.StatusAccepted:
			deployID, err := decodeDeployID(resp)
			if err != nil {
				return "", ExitRequestFailed, fmt.Sprintf("decode 202 response: %v", err)
			}
			return deployID, exitAccepted, ""
		case http.StatusConflict:
			resp.Body.Close()
			if time.Now().After(deadline) {
				return "", ExitTimeout, "timed out waiting for a prior deploy of this service to finish"
			}
			time.Sleep(opts.ConnectBackoff)
			continue
		case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound:
			return "", ExitRequestFailed, readErrorMessage(resp)
		default:
			return "", ExitDeployFailed, readErrorMessage(resp)
		}
	}
}

type pollJob struct {
	opts     Options
	deployID string
	deadline time.Time
	summary  Summary
}

func poll(ctx context.Context, httpClient *http.Client, job pollJob) Summary {
	opts := job.opts
	deadline := job.deadline
	summary := job.summary
	for {
		if time.Now().After(deadline) {
			return fail(summary, ExitTimeout, "timed out waiting for deploy to reach a terminal state")
		}

		reqCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, opts.Endpoint+"/v1/deploys/"+job.deployID, nil)
		resp, err := httpClient.Do(req)
		cancel()

		if err != nil {
			time.Sleep(opts.PollInterval)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			msg := readErrorMessage(resp)
			return fail(summary, ExitDeployFailed, msg)
		}

		var status api.DeployStatus
		decodeErr := json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if decodeErr != nil {
			return fail(summary, ExitDeployFailed, fmt.Sprintf("decode status response: %v", decodeErr))
		}

		if !status.State.IsTerminal() {
			time.Sleep(opts.PollInterval)
			continue
		}

		summary.Result = string(status.Result)
		summary.Rollback = string(status.Rollback)
		summary.ImageID = status.ImageID
		summary.Message = status.Message
		if status.Result == api.ResultSuccess || status.Result == api.ResultNoop {
			summary.ExitCode = ExitSuccess
		} else {
			summary.ExitCode = ExitDeployFailed
		}
		return summary
	}
}

func fail(summary Summary, exitCode ExitCode, message string) Summary {
	summary.ExitCode = exitCode
	summary.Error = message
	return summary
}

func decodeDeployID(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	var accepted api.DeployAccepted
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		return "", err
	}
	return accepted.DeployID, nil
}

func readErrorMessage(resp *http.Response) string {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
	fallback := strings.TrimSpace(fmt.Sprintf("http %d: %s", resp.StatusCode, string(data)))
	var errResp api.ErrorResponse
	err := json.Unmarshal(data, &errResp)
	if err != nil {
		return fallback
	}
	if errResp.Message == "" {
		return fallback
	}
	return fmt.Sprintf("%s: %s", errResp.Code, errResp.Message)
}
