# kube-ssm-proxy

Connect to private EKS clusters through AWS SSM port forwarding. A single Go binary that manages SSM sessions, kubeconfig entries, and cluster selection via fzf.

## Prerequisites

- [AWS CLI](https://aws.amazon.com/cli/) with the [SSM plugin](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [fzf](https://github.com/junegunn/fzf)
- [Granted](https://docs.commonfate.io/granted/getting-started) (`assume` exec-credential helper)

## Installation

```bash
git clone https://github.com/JaimeHolland/kube-ssm-proxy.git
cd kube-ssm-proxy
make build
```

This produces a `kube-ssm-proxy` binary in the project root.

## Configuration

Create a `clusters.yaml` file next to the binary (or in your current working directory):

```yaml
clusters:
  - name: "my-cluster"
    region: "us-west-2"
    cluster_name: "eks-prod"
    environment: "production"
    profile: "MyProfile/Admin"
    use_bastion: true
    bastion_tag: "Purpose=bastion"
```

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Display name and kubectl context name |
| `region` | Yes | AWS region (min 3 characters) |
| `cluster_name` | Yes | EKS cluster name |
| `profile` | Yes | AWS CLI profile name |
| `environment` | No | Label (default: `"unknown"`) |
| `use_bastion` | No | Connect via SSM bastion (`true`) or directly to the EKS endpoint (`false`) (default: `true`) |
| `bastion_tag` | No | EC2 tag filter for bastion discovery in `key=value` format (default: `"Purpose=bastion"`). Ignored and warned about when `use_bastion: false`. |

## Usage

```bash
./kube-ssm-proxy
```

An interactive fzf selector shows your configured clusters. Active port forwards are indicated with a filled dot (`●`). Select a cluster to connect, or choose `[Kill all SSM sessions]` to tear down all active forwards.

### Headless Mode

Skip the interactive selector for scripting:

```bash
KUBECTL_SSM_HEADLESS_SELECTION=my-cluster ./kube-ssm-proxy
KUBECTL_SSM_HEADLESS_EXIT=1 KUBECTL_SSM_HEADLESS_SELECTION=my-cluster ./kube-ssm-proxy
```

## How It Works

1. Loads and validates `clusters.yaml`
2. Cleans up stale SSM log files and prunes duplicate sessions
3. Presents the fzf cluster selector (or uses headless selection)
4. Authenticates via AWS SSO if needed
5. Discovers the EKS endpoint and bastion instance
6. Starts an SSM port-forwarding session (or reuses an existing one)
7. Updates kubeconfig so `kubectl` commands target the selected cluster

For clusters with `use_bastion: false`, steps 5-6 are skipped and kubeconfig points straight at the EKS endpoint.

## License

MIT
