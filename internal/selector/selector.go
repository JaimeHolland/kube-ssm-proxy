package selector

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"kube-ssm-proxy/internal/config"
)

const killOption = "[Kill all SSM sessions]"

// Select presents an fzf-based cluster selector and returns the chosen
// cluster config, or nil if the user cancelled. The special "kill all"
// action is returned as (nil, true, nil).
//
// activeNames is the set of cluster names that have an active port forward.
//
// In headless mode (KUBECTL_SSM_HEADLESS_SELECTION env var), fzf is
// bypassed and the matching cluster is returned directly.
func Select(clusters []config.ClusterConfig, activeNames map[string]bool) (*config.ClusterConfig, bool, error) {
	headless := os.Getenv("KUBECTL_SSM_HEADLESS_SELECTION")
	if headless != "" {
		return headlessSelect(clusters, headless)
	}
	return fzfSelect(clusters, activeNames)
}

func headlessSelect(clusters []config.ClusterConfig, selection string) (*config.ClusterConfig, bool, error) {
	fmt.Printf("\033[33mHEADLESS MODE: Using selection '%s' from environment variable\033[0m\n", selection)

	if selection == "kill_all" || selection == killOption {
		return nil, true, nil
	}

	if c := matchCluster(clusters, selection); c != nil {
		return c, false, nil
	}
	return nil, false, fmt.Errorf("HEADLESS MODE: no cluster matching %q", selection)
}

func fzfSelect(clusters []config.ClusterConfig, activeNames map[string]bool) (*config.ClusterConfig, bool, error) {
	for {
		options := buildOptions(clusters, activeNames)
		input := strings.Join(options, "\n")

		cmd := exec.Command("fzf",
			"--prompt", "Cluster> ",
			"--height", "40%",
			"--reverse",
			"--border",
		)
		cmd.Stdin = strings.NewReader(input)
		cmd.Stderr = os.Stderr

		out, err := cmd.Output()
		if err != nil {
			return nil, false, nil
		}

		selected := strings.TrimSpace(string(out))
		if selected == killOption {
			return nil, true, nil
		}

		if c := matchCluster(clusters, selected); c != nil {
			return c, false, nil
		}
	}
}

// matchCluster finds a cluster matching a selection string, handling the
// status prefix (●/○) and direct-connect suffix (🌏).
func matchCluster(clusters []config.ClusterConfig, selected string) *config.ClusterConfig {
	for i := range clusters {
		c := &clusters[i]
		icon := ""
		if c.DirectConnect {
			icon = " 🌏"
		}
		if c.Name == selected ||
			"● "+c.Name == selected ||
			"○ "+c.Name == selected ||
			"● "+c.Name+icon == selected ||
			"○ "+c.Name+icon == selected {
			return c
		}
	}
	return nil
}

func buildOptions(clusters []config.ClusterConfig, activeNames map[string]bool) []string {
	opts := []string{killOption}
	for _, c := range clusters {
		status := "○"
		if activeNames[c.Name] {
			status = "●"
		}
		icon := ""
		if c.DirectConnect {
			icon = " 🌏"
		}
		opts = append(opts, fmt.Sprintf("%s %s%s", status, c.Name, icon))
	}
	return opts
}
