package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ClusterConfig holds configuration for a single cluster.
type ClusterConfig struct {
	Name          string `yaml:"name"`
	Region        string `yaml:"region"`
	ClusterName   string `yaml:"cluster_name"`
	Environment   string `yaml:"environment"`
	Profile       string `yaml:"profile"`
	DirectConnect bool   `yaml:"direct_connect"`
}

// SSOConfig holds SSO settings used for login hints.
type SSOConfig struct {
	StartURL string `yaml:"sso_start_url"`
	Region   string `yaml:"sso_region"`
}

type configFile struct {
	SSO      SSOConfig       `yaml:"sso"`
	Clusters []ClusterConfig `yaml:"clusters"`
}

// LoadClusters reads clusters.yaml from the same directory as the running binary
// and returns validated cluster configs and SSO settings.
func LoadClusters() ([]ClusterConfig, SSOConfig, error) {
	path, err := findConfigPath()
	if err != nil {
		return nil, SSOConfig{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, SSOConfig{}, fmt.Errorf("read config: %w", err)
	}

	var cf configFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, SSOConfig{}, fmt.Errorf("parse config: %w", err)
	}

	if len(cf.Clusters) == 0 {
		return nil, SSOConfig{}, fmt.Errorf("no clusters defined in %s", path)
	}

	seen := make(map[string]bool)
	for i := range cf.Clusters {
		c := &cf.Clusters[i]
		if err := validateCluster(c, i); err != nil {
			return nil, SSOConfig{}, err
		}
		if seen[c.Name] {
			return nil, SSOConfig{}, fmt.Errorf("cluster %d: duplicate name %q", i, c.Name)
		}
		seen[c.Name] = true
	}

	return cf.Clusters, cf.SSO, nil
}

func validateCluster(c *ClusterConfig, idx int) error {
	if c.Name == "" {
		return fmt.Errorf("cluster %d: missing name", idx)
	}
	if c.Region == "" || len(c.Region) < 3 {
		return fmt.Errorf("cluster %d: invalid region %q", idx, c.Region)
	}
	if c.ClusterName == "" {
		return fmt.Errorf("cluster %d: missing cluster_name", idx)
	}
	if c.Profile == "" {
		return fmt.Errorf("cluster %d: missing profile", idx)
	}
	if c.Environment == "" {
		c.Environment = "unknown"
	}
	return nil
}

func findConfigPath() (string, error) {
	// First try next to the binary
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), "clusters.yaml")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// Fall back to current working directory
	if _, err := os.Stat("clusters.yaml"); err == nil {
		return "clusters.yaml", nil
	}
	return "", fmt.Errorf("clusters.yaml not found (checked binary dir and cwd)")
}
