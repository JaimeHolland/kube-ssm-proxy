package ssm

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Forward represents an active SSM port-forwarding process.
type Forward struct {
	PID        int
	LocalPort  int
	TargetHost string
	TargetPort int
}

// ListForwards scans OS processes for active SSM port-forwarding sessions.
// It shells out to `ps -eo pid,args` and parses lines matching the SSM
// port-forwarding document name.
func ListForwards() ([]Forward, error) {
	out, err := exec.Command("ps", "-eo", "pid,args").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}

	var forwards []Forward
	seenPorts := make(map[int]bool)

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "aws") ||
			!strings.Contains(line, "ssm") ||
			!strings.Contains(line, "start-session") ||
			!strings.Contains(line, "AWS-StartPortForwardingSession") {
			continue
		}

		f, ok := parseLine(line)
		if !ok {
			continue
		}
		if seenPorts[f.LocalPort] {
			continue
		}
		if !IsPortListening(f.LocalPort) {
			continue
		}
		seenPorts[f.LocalPort] = true
		forwards = append(forwards, f)
	}
	return forwards, nil
}

// parseLine extracts PID, local port, target host, and target port from a
// ps output line.
func parseLine(line string) (Forward, bool) {
	// Line format: "  PID  aws ssm start-session ... --parameters host=X,portNumber=Y,localPortNumber=Z ..."
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Forward{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Forward{}, false
	}

	rest := strings.Join(fields[1:], " ")

	host := extractParam(rest, "host=")
	portStr := extractParam(rest, "portNumber=")
	localStr := extractParam(rest, "localPortNumber=")
	if host == "" || localStr == "" {
		return Forward{}, false
	}

	localPort, err := strconv.Atoi(localStr)
	if err != nil {
		return Forward{}, false
	}
	targetPort := 443
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			targetPort = p
		}
	}

	return Forward{
		PID:        pid,
		LocalPort:  localPort,
		TargetHost: host,
		TargetPort: targetPort,
	}, true
}

// extractParam pulls the value of key=value from a comma/space-delimited
// parameter string.
func extractParam(s, key string) string {
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := len(s)
	for i := start; i < len(s); i++ {
		if s[i] == ',' || s[i] == ' ' {
			end = i
			break
		}
	}
	return s[start:end]
}

// FindAvailablePort returns the first free TCP port in [49152, 65535].
// It skips ports that are already listening AND ports in the reserved set
// (typically ports already assigned in kubeconfig).
func FindAvailablePort(reserved map[int]bool) (int, error) {
	for port := 49152; port <= 65535; port++ {
		if reserved[port] {
			continue
		}
		if !IsPortListening(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port in range 49152-65535")
}

// IsPortListening returns true if a TCP connect to localhost:port succeeds.
func IsPortListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
