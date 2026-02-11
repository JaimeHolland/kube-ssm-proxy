package kubeconfig

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

// SetClusterSSM configures kubectl for an SSM-forwarded cluster.
//   - Cluster server: https://localhost:{port} with insecure TLS
//   - Credentials: Granted exec plugin
//   - Context: cluster name, switched to current
func SetClusterSSM(contextName, clusterName, region, profile, accountID string, port int) error {
	userName := arnUser(region, accountID, clusterName)
	server := fmt.Sprintf("https://localhost:%d", port)

	cmds := kubectlCommands(contextName, userName, server, clusterName, region, profile)
	return runAll(cmds)
}

// SetClusterDirect configures kubectl for a direct-connect cluster.
//   - Cluster server: real EKS endpoint with insecure TLS
//   - Credentials: Granted exec plugin
//   - Context: cluster name, switched to current
func SetClusterDirect(contextName, clusterName, region, profile, accountID, endpoint string) error {
	userName := arnUser(region, accountID, clusterName)

	cmds := kubectlCommands(contextName, userName, endpoint, clusterName, region, profile)
	return runAll(cmds)
}

// SwitchContext runs `kubectl config use-context`.
func SwitchContext(contextName string) error {
	return run("kubectl", "config", "use-context", contextName)
}

// ContextForPort returns the kubectl cluster name whose server is
// https://localhost:{port}, or "" if none found.
func ContextForPort(port int) string {
	data, err := kubeconfigJSON()
	if err != nil {
		return ""
	}

	target := fmt.Sprintf("https://localhost:%d", port)

	clusters, _ := data["clusters"].([]interface{})
	for _, item := range clusters {
		m, _ := item.(map[string]interface{})
		name, _ := m["name"].(string)
		cluster, _ := m["cluster"].(map[string]interface{})
		server, _ := cluster["server"].(string)

		if strings.HasPrefix(server, "# INACTIVE:") {
			continue
		}
		if server == target {
			return name
		}
	}
	return ""
}

// MarkPortInactive finds all kubectl clusters with server https://localhost:{port}
// and replaces the server with "# INACTIVE: https://localhost:{port}".
func MarkPortInactive(port int) {
	data, err := kubeconfigJSON()
	if err != nil {
		return
	}

	target := fmt.Sprintf("https://localhost:%d", port)
	clusters, _ := data["clusters"].([]interface{})
	for _, item := range clusters {
		m, _ := item.(map[string]interface{})
		name, _ := m["name"].(string)
		cluster, _ := m["cluster"].(map[string]interface{})
		server, _ := cluster["server"].(string)

		if server == target {
			log.Printf("Marking cluster %q as inactive (port %d)", name, port)
			_ = run("kubectl", "config", "set-cluster", name,
				"--server", "# INACTIVE: "+server)
		}
	}
}

// MarkAllLocalhostInactive marks every https://localhost:* cluster as inactive.
func MarkAllLocalhostInactive() {
	data, err := kubeconfigJSON()
	if err != nil {
		return
	}

	clusters, _ := data["clusters"].([]interface{})
	for _, item := range clusters {
		m, _ := item.(map[string]interface{})
		name, _ := m["name"].(string)
		cluster, _ := m["cluster"].(map[string]interface{})
		server, _ := cluster["server"].(string)

		if strings.HasPrefix(server, "https://localhost:") {
			log.Printf("Marking cluster %q as inactive", name)
			_ = run("kubectl", "config", "set-cluster", name,
				"--server", "# INACTIVE: "+server)
		}
	}
}

// PortsInUse returns the set of localhost ports currently assigned to
// non-inactive clusters in kubeconfig. Used by port allocation to avoid
// collisions with existing entries.
func PortsInUse() map[int]bool {
	ports := make(map[int]bool)
	data, err := kubeconfigJSON()
	if err != nil {
		return ports
	}

	clusters, _ := data["clusters"].([]interface{})
	for _, item := range clusters {
		m, _ := item.(map[string]interface{})
		cluster, _ := m["cluster"].(map[string]interface{})
		server, _ := cluster["server"].(string)

		if strings.HasPrefix(server, "# INACTIVE:") {
			continue
		}
		if strings.HasPrefix(server, "https://localhost:") {
			portStr := strings.TrimPrefix(server, "https://localhost:")
			if p, err := strconv.Atoi(portStr); err == nil {
				ports[p] = true
			}
		}
	}
	return ports
}

// --- helpers ---

func arnUser(region, accountID, clusterName string) string {
	return fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", region, accountID, clusterName)
}

func kubectlCommands(contextName, userName, server, clusterName, region, profile string) [][]string {
	return [][]string{
		// 1. Set cluster
		{"kubectl", "config", "set-cluster", contextName,
			"--server=" + server,
			"--insecure-skip-tls-verify=true"},
		// 2. Set credentials — exec plugin
		{"kubectl", "config", "set-credentials", userName,
			"--exec-command", "assume",
			"--exec-api-version", "client.authentication.k8s.io/v1beta1",
			"--exec-arg", profile,
			"--exec-arg", "--exec",
			"--exec-arg", fmt.Sprintf("aws --region %s eks get-token --cluster-name %s", region, clusterName)},
		// 3. Set credentials — env vars
		{"kubectl", "config", "set-credentials", userName,
			"--exec-env", "GRANTED_QUIET=true",
			"--exec-env", "FORCE_NO_ALIAS=true"},
		// 4. Set context
		{"kubectl", "config", "set-context", contextName,
			"--cluster", contextName,
			"--user", userName},
		// 5. Use context
		{"kubectl", "config", "use-context", contextName},
	}
}

func runAll(cmds [][]string) error {
	for _, args := range cmds {
		if err := run(args[0], args[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %s (%w)", name, args, string(out), err)
	}
	return nil
}

func kubeconfigJSON() (map[string]interface{}, error) {
	cmd := exec.Command("kubectl", "config", "view", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl config view: %w", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parse kubeconfig json: %w", err)
	}
	return data, nil
}
