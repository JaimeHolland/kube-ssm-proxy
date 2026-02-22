package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"kube-ssm-proxy/internal/aws"
	"kube-ssm-proxy/internal/config"
	"kube-ssm-proxy/internal/kubeconfig"
	"kube-ssm-proxy/internal/selector"
	"kube-ssm-proxy/internal/ssm"
)

// ANSI helpers
const (
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	dim    = "\033[2m"
	bold   = "\033[1m"
	reset  = "\033[0m"
)

func main() {
	// Signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Printf("\n%sReceived exit signal. Cleaning up...%s\n", yellow, reset)
		os.Exit(0)
	}()

	fmt.Printf("\n%sPress Escape or Ctrl+C in the selector to exit.%s\n", dim, reset)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to load configuration: %v%s\n", red, err, reset)
		os.Exit(1)
	}
	log.Printf("Loaded %d clusters", len(cfg.Clusters))

	// Clean up old SSM log files
	ssm.CleanOldLogs()

	// Prune duplicate SSM sessions
	if pruned := ssm.PruneDuplicates(); pruned > 0 {
		log.Printf("Pruned %d duplicate SSM sessions at startup", pruned)
	}

	// Display existing port forwards and select
	var selected *config.ClusterConfig
	for {
		displayForwards()

		var killAll bool
		var err error
		selected, killAll, err = selector.Select(cfg.Clusters, activeClusterNames(), cfg.FzfHeight)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s%v%s\n", red, err, reset)
			os.Exit(1)
		}

		if killAll {
			fmt.Printf("\n%sKilling all SSM port forwarding sessions...%s\n", red, reset)
			ssm.StopAll()
			kubeconfig.MarkAllLocalhostInactive()
			continue
		}

		break
	}

	if selected == nil {
		// User cancelled
		return
	}

	fmt.Printf("\n%sConnecting to %s...%s\n", blue, selected.Name, reset)

	if selected.DirectConnect {
		connectDirect(selected, cfg.SSO)
	} else {
		connectSSM(selected, cfg.SSO)
	}

	// Check for headless exit
	if os.Getenv("KUBECTL_SSM_HEADLESS_EXIT") != "" {
		fmt.Printf("%sHEADLESS MODE: Exiting after headless selection%s\n", yellow, reset)
		return
	}

	// Display final state
	displayForwards()
}

// connectSSM handles the SSM port-forward path.
func connectSSM(cluster *config.ClusterConfig, sso config.SSOConfig) {
	// Fast path: check if there's already a forward for this cluster
	forwards, _ := ssm.ListForwards()
	for _, f := range forwards {
		ctx := kubeconfig.ContextForPort(f.LocalPort)
		if ctx == cluster.Name {
			log.Printf("Reusing existing forward on port %d", f.LocalPort)
			if err := kubeconfig.SwitchContext(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "%sFailed to switch context: %v%s\n", red, err, reset)
				os.Exit(1)
			}
			fmt.Printf("%sConnection established to %s (reused port %d)%s\n", green, cluster.Name, f.LocalPort, reset)
			return
		}
	}

	// Authenticate
	auth, err := aws.Authenticate(cluster.Profile, sso.StartURL, sso.Region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%s%v%s\n", red, err, reset)
		os.Exit(1)
	}

	// Get EKS endpoint
	endpoint, err := aws.DescribeCluster(cluster.Profile, cluster.Region, cluster.ClusterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to get cluster endpoint: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// Find bastion
	bastionID, err := aws.FindBastion(cluster.Profile, cluster.Region, cluster.BastionTag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to find bastion: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// Start port forward (skip ports already in kubeconfig)
	port, err := ssm.StartForward(cluster.Name, bastionID, endpoint, cluster.Profile, cluster.Region,
		kubeconfig.PortsInUse(), kubeconfig.MarkPortInactive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to start port forward: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// Update kubeconfig
	if err := kubeconfig.SetClusterSSM(
		cluster.Name, cluster.ClusterName, cluster.Region,
		cluster.Profile, auth.AccountID, port,
	); err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to update kubeconfig: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	fmt.Printf("%sConnection established to %s (port %d)%s\n", green, cluster.Name, port, reset)
}

// connectDirect handles the direct-connect path (no SSM).
func connectDirect(cluster *config.ClusterConfig, sso config.SSOConfig) {
	auth, err := aws.Authenticate(cluster.Profile, sso.StartURL, sso.Region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%s%v%s\n", red, err, reset)
		os.Exit(1)
	}

	endpoint, err := aws.DescribeCluster(cluster.Profile, cluster.Region, cluster.ClusterName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to get cluster endpoint: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	if err := kubeconfig.SetClusterDirect(
		cluster.Name, cluster.ClusterName, cluster.Region,
		cluster.Profile, auth.AccountID, endpoint,
	); err != nil {
		fmt.Fprintf(os.Stderr, "%sFailed to update kubeconfig: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	fmt.Printf("%sConnection established to %s (direct)%s\n", green, cluster.Name, reset)
}

// displayForwards prints existing SSM port-forwarding sessions.
func displayForwards() {
	forwards, err := ssm.ListForwards()
	if err != nil {
		log.Printf("Warning: %v", err)
		return
	}

	if len(forwards) == 0 {
		fmt.Printf("\n%sNo existing SSM port forwards found.%s\n", dim, reset)
		return
	}

	fmt.Printf("\n%s%sExisting SSM Port Forwards:%s\n", bold, reset, reset)
	for _, f := range forwards {
		ctx := kubeconfig.ContextForPort(f.LocalPort)
		if ctx != "" {
			fmt.Printf("  %s●%s Port %d [%s] -> %s (PID: %d)\n",
				green, reset, f.LocalPort, ctx, f.TargetHost, f.PID)
		} else {
			fmt.Printf("  %s●%s Port %d -> %s (PID: %d)\n",
				green, reset, f.LocalPort, f.TargetHost, f.PID)
		}
	}
}

// activeClusterNames returns the set of cluster names that have an active
// SSM port forward, by matching forward ports to kubeconfig entries.
func activeClusterNames() map[string]bool {
	names := make(map[string]bool)
	forwards, err := ssm.ListForwards()
	if err != nil {
		return names
	}
	for _, f := range forwards {
		ctx := kubeconfig.ContextForPort(f.LocalPort)
		if ctx != "" {
			names[ctx] = true
		}
	}
	return names
}
