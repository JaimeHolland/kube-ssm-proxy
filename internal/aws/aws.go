package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

// AuthInfo holds the account ID and role name extracted from STS.
type AuthInfo struct {
	AccountID string
	RoleName  string
}

// Authenticate verifies credentials for the given profile by calling
// `aws sts get-caller-identity`. If that fails it prompts the user to
// log in via Granted. The ssoStartURL and ssoRegion are used to build
// the hint command shown to the user.
func Authenticate(profile, ssoStartURL, ssoRegion string) (*AuthInfo, error) {
	info, err := getCallerIdentity(profile)
	if err != nil {
		return nil, &AuthError{
			Profile:     profile,
			SSOStartURL: ssoStartURL,
			SSORegion:   ssoRegion,
		}
	}
	return info, nil
}

// AuthError is returned when AWS credentials are missing or expired.
type AuthError struct {
	Profile     string
	SSOStartURL string
	SSORegion   string
}

func (e *AuthError) Error() string {
	msg := fmt.Sprintf("no active AWS session for profile %q.\n\nTo authenticate, run:\n\n", e.Profile)
	if e.SSOStartURL != "" && e.SSORegion != "" {
		msg += fmt.Sprintf("  granted sso login --sso-start-url %s --sso-region %s\n", e.SSOStartURL, e.SSORegion)
	} else {
		msg += fmt.Sprintf("  granted sso login --profile %s\n", e.Profile)
	}
	msg += "\nThen re-run this tool."
	return msg
}

// DescribeCluster returns the EKS cluster endpoint URL.
func DescribeCluster(profile, region, clusterName string) (string, error) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}

	client := eks.NewFromConfig(cfg)
	out, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: &clusterName,
	})
	if err != nil {
		return "", fmt.Errorf("describe cluster %s: %w", clusterName, err)
	}
	if out.Cluster == nil || out.Cluster.Endpoint == nil {
		return "", fmt.Errorf("cluster %s has no endpoint", clusterName)
	}

	endpoint := *out.Cluster.Endpoint
	log.Printf("EKS endpoint for %s: %s", clusterName, endpoint)
	return endpoint, nil
}

// FindBastion discovers the single running EC2 instance tagged Purpose=bastion
// in the given region.
func FindBastion(profile, region string) (string, error) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}

	client := ec2.NewFromConfig(cfg)
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: strPtr("tag:Purpose"), Values: []string{"bastion"}},
			{Name: strPtr("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe instances: %w", err)
	}

	var instances []string
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			if inst.InstanceId != nil {
				instances = append(instances, *inst.InstanceId)
			}
		}
	}

	switch len(instances) {
	case 0:
		return "", fmt.Errorf("no bastion instance found in %s", region)
	case 1:
		log.Printf("Found bastion: %s", instances[0])
		return instances[0], nil
	default:
		return "", fmt.Errorf("expected 1 bastion in %s, found %d", region, len(instances))
	}
}

// --- helpers ---

func getCallerIdentity(profile string) (*AuthInfo, error) {
	cmd := exec.Command("aws", "sts", "get-caller-identity",
		"--profile", profile, "--output", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sts get-caller-identity: %w", err)
	}

	var result struct {
		Account string `json:"Account"`
		Arn     string `json:"Arn"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse sts output: %w", err)
	}

	roleName := extractRoleName(result.Arn)
	if roleName == "" {
		return nil, fmt.Errorf("could not extract role name from ARN: %s", result.Arn)
	}

	return &AuthInfo{
		AccountID: result.Account,
		RoleName:  roleName,
	}, nil
}

func extractRoleName(arn string) string {
	if idx := strings.Index(arn, "assumed-role/"); idx >= 0 {
		parts := strings.Split(arn[idx+len("assumed-role/"):], "/")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	if idx := strings.Index(arn, "role/"); idx >= 0 {
		parts := strings.Split(arn[idx+len("role/"):], "/")
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return ""
}

func strPtr(s string) *string { return &s }
