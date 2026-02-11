# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build

```bash
make build    # builds ./kube-ssm-proxy binary
make clean    # removes binary
make tidy     # runs go mod tidy
```

No tests or linter are configured yet.

## What This Project Does

kube-ssm-proxy connects to private EKS clusters via AWS SSM port forwarding. It manages SSM sessions, kubeconfig entries, and cluster selection through fzf. Single binary, no runtime dependencies beyond AWS CLI (with SSM plugin), kubectl, fzf, and Granted (`assume`).

## Architecture

**Entry point**: `main.go` — orchestration loop that loads config, cleans up stale state, runs the fzf selector, then delegates to `connectSSM()` or `connectDirect()` based on the cluster's `direct_connect` flag.

**Two connection paths**:
- **SSM path** (`connectSSM`): authenticate → describe EKS cluster → find bastion EC2 instance (tagged `Purpose=bastion`) → allocate ephemeral port → start `aws ssm start-session` as detached process → poll until port is listening → update kubeconfig
- **Direct path** (`connectDirect`): authenticate → describe EKS cluster → update kubeconfig pointing at real endpoint

**Internal packages**:
- `config` — loads and validates `clusters.yaml` (searched next to binary, then CWD)
- `aws` — STS auth (shells out to `aws` CLI), EKS describe and EC2 bastion discovery (AWS SDK v2)
- `ssm` — SSM process lifecycle. `ssm.go` handles start/stop/prune/logging. `process.go` handles OS process scanning (`ps -eo pid,args`) and port utilities
- `kubeconfig` — all `kubectl config` mutations. Manages active/inactive states by prefixing servers with `# INACTIVE:`
- `selector` — fzf integration and headless mode (via `KUBECTL_SSM_HEADLESS_SELECTION` env var)
- `tips` — rotating daily tip, not yet wired into the main flow

**Key patterns**:
- SSM sessions are detached (`Setpgid: true`) so they survive parent exit
- Port allocation scans 49152–65535, skipping ports in kubeconfig and ports already listening
- Process scanning parses `ps` output to find SSM sessions and extract `host=`, `portNumber=`, `localPortNumber=` parameters
- Termination uses SIGTERM → 2s wait → SIGKILL

## Configuration

The binary reads `clusters.yaml` (not committed, in `.gitignore`). See `SPECIFICATION.md` for the full schema and validation rules. SSO settings (`sso_start_url`, `sso_region`) are at the top level of the YAML for login hint commands.
