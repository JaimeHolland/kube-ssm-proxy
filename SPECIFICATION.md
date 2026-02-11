# kube-ssm-proxy Specification

## Overview

`kube-ssm-proxy` connects to private EKS clusters through AWS SSM port forwarding.
It compiles to a single Go binary with no runtime dependencies beyond:

- **AWS CLI** (with SSM plugin)
- **kubectl**
- **fzf**
- **assume** (Granted exec-credential helper)

## Configuration

A `clusters.yaml` file is expected next to the binary (or in CWD as fallback).

```yaml
clusters:
  - name: "my-cluster"          # Unique display name / kubectl context name
    region: "us-west-2"         # AWS region
    cluster_name: "eks-prod"    # EKS cluster name
    environment: "production"   # Optional label (default: "unknown")
    profile: "MyProfile/Admin"  # AWS CLI profile name
    direct_connect: false       # Optional: skip SSM, connect directly (default: false)
```

### Validation Rules

- `name`, `region`, `cluster_name`, `profile` are required non-empty strings.
- `region` must be at least 3 characters.
- Duplicate `name` values are rejected.

## Flow

### Startup

1. Load and validate `clusters.yaml`.
2. Clean up SSM log files older than 24 hours.
3. Prune duplicate SSM port-forwarding sessions (keep one per target host).
4. Display existing port forwards.
5. Show fzf cluster selector.

### Cluster Selection (fzf)

- Options: `[Kill all SSM sessions]` followed by each cluster.
- Format: `{●/○} {name}` — filled dot means active forward exists.
- Direct-connect clusters show a `🌏` suffix.
- Selecting "Kill all" terminates all SSM processes and marks kubeconfig entries inactive.

### Headless Mode

- Set `KUBECTL_SSM_HEADLESS_SELECTION=<cluster-name>` to skip fzf.
- Set `KUBECTL_SSM_HEADLESS_EXIT=1` to exit immediately after connecting.
- `kill_all` as the selection value triggers the kill-all action.

### SSM Connection (default path)

1. **Fast path**: scan OS processes for an existing SSM forward whose kubeconfig
   port matches the cluster name — reuse it via `kubectl config use-context`.
2. **Authenticate**: `aws sts get-caller-identity --profile X`; on failure,
   `aws sso login --profile X` then retry.
3. **Describe cluster**: AWS SDK `eks.DescribeCluster` — endpoint URL.
4. **Find bastion**: AWS SDK `ec2.DescribeInstances` filtered by
   `tag:Purpose=bastion` + `running`. Requires exactly 1 result.
5. **Allocate port**: first free TCP port in 49152–65535 that is also not
   already assigned to another cluster in kubeconfig.
6. **Mark inactive**: replace `https://localhost:{port}` in kubeconfig with
   `# INACTIVE: https://localhost:{port}` for any cluster already using that port.
7. **Start forward**: launch `aws ssm start-session` as a detached process
   (`Setpgid: true`) with `AWS_DEFAULT_REGION` set. Output is captured to a
   timestamped log file at `~/.cache/kube-ssm-proxy/logs/`.
8. **Wait**: exponential backoff (1s, 2s, 4s, 8s, 16s, 32s) until port is
   reachable via TCP connect. If the SSM process dies during this period, the
   error is reported immediately with log file contents.
9. **Update kubeconfig**: `kubectl config set-cluster`, `set-credentials`
   (Granted exec plugin with env vars), `set-context`, `use-context`.

### Direct Connection

1. Authenticate (same as SSM).
2. Describe cluster — endpoint URL.
3. Update kubeconfig pointing at the real endpoint (no port forward).

## Kubeconfig Layout

| Field | Value |
|---|---|
| Cluster name | `cluster.name` from YAML |
| Server (SSM) | `https://localhost:{port}` |
| Server (direct) | Real EKS endpoint |
| TLS | `--insecure-skip-tls-verify=true` |
| User name | `arn:aws:eks:{region}:{account}:cluster/{cluster_name}` |
| Exec command | `assume` |
| Exec args | `{profile}`, `--exec`, `aws --region {region} eks get-token --cluster-name {cluster_name}` |
| Exec env | `GRANTED_QUIET=true`, `FORCE_NO_ALIAS=true` |

## Process Management

- **Scanning**: `ps -eo pid,args` filtered for `aws` + `ssm` + `start-session` +
  `AWS-StartPortForwardingSession`.
- **Parameter extraction**: parse `host=`, `portNumber=`, `localPortNumber=` from
  command-line args.
- **Termination**: `SIGTERM` — 2s wait — `SIGKILL`.
- **Pruning**: group by target host, keep first, kill rest.

## Logging

SSM session output is captured to timestamped log files at
`~/.cache/kube-ssm-proxy/logs/ssm-port-{port}_{timestamp}.log`. Each line is
prefixed with a timestamp during the connection phase. Logs older than 24 hours
are cleaned up automatically at startup.

## Project Structure

```
kube-ssm-proxy/
├── SPECIFICATION.md
├── Makefile
├── go.mod / go.sum
├── main.go                          # Entry point, orchestration, signal handling
└── internal/
    ├── config/config.go             # YAML loading & validation
    ├── aws/aws.go                   # STS auth, EKS describe, EC2 bastion discovery
    ├── ssm/
    │   ├── process.go               # OS process scanning, port utilities
    │   └── ssm.go                   # Port forward lifecycle: start, stop, prune, logging
    ├── kubeconfig/kubeconfig.go     # kubectl CLI calls for config management
    └── selector/selector.go         # fzf invocation + headless mode
```
