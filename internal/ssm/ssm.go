package ssm

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// timestampWriter wraps an io.Writer and prepends a timestamp to each line.
type timestampWriter struct {
	w   io.Writer
	buf []byte // partial line buffer
}

func (tw *timestampWriter) Write(p []byte) (int, error) {
	tw.buf = append(tw.buf, p...)
	total := len(p)

	for {
		idx := bytes.IndexByte(tw.buf, '\n')
		if idx < 0 {
			break
		}
		line := tw.buf[:idx]
		tw.buf = tw.buf[idx+1:]

		if len(line) == 0 {
			continue
		}

		ts := time.Now().Format("2006-01-02 15:04:05")
		fmt.Fprintf(tw.w, "[%s] [ssm] %s\n", ts, line)
	}
	return total, nil
}

// StartForward launches an SSM port-forwarding session as a detached process.
// It allocates a port that is both free (not listening) and not already in
// kubeconfig, marks any stale kubeconfig entries for that port as inactive,
// starts the process, and waits for the port to become reachable by polling
// every 2 seconds for up to 120 seconds.
//
// stderr is captured to a temp log file so failures are visible.
func StartForward(
	clusterName, bastionID, targetHost, profile, region string,
	reservedPorts map[int]bool,
	markInactive func(int),
) (int, error) {
	port, err := FindAvailablePort(reservedPorts)
	if err != nil {
		return 0, err
	}

	// Mark any existing clusters using this port as inactive
	if markInactive != nil {
		markInactive(port)
	}

	// Strip https:// from target host
	host := strings.TrimPrefix(targetHost, "https://")

	params := fmt.Sprintf("host=%s,portNumber=443,localPortNumber=%d", host, port)
	args := []string{
		"ssm", "start-session",
		"--target", bastionID,
		"--document-name", "AWS-StartPortForwardingSessionToRemoteHost",
		"--parameters", params,
		"--profile", profile,
		"--region", region,
	}

	cmd := exec.Command("aws", args...)

	// Detach into its own process group so it survives parent exit
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture stderr to a log file for debugging
	logDir := ssmLogDir()
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	logPath := filepath.Join(logDir, fmt.Sprintf("ssm-port-%d_%s.log", port, timestamp))
	logFile, err := os.Create(logPath)
	if err != nil {
		return 0, fmt.Errorf("create ssm log file: %w", err)
	}
	tsWriter := &timestampWriter{w: logFile}

	// Write connection context header for debugging
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(logFile, "[%s] cluster=%s region=%s profile=%s bastion=%s target=%s port=%d\n",
		ts, clusterName, region, profile, bastionID, host, port)

	cmd.Stdout = tsWriter
	cmd.Stderr = tsWriter

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start ssm session: %w", err)
	}

	log.Printf("SSM process started with PID: %d (log: %s)", cmd.Process.Pid, logPath)
	// Don't close logFile — the detached process continues writing to it via tsWriter

	// Wait for the process in a goroutine so we can detect early exit.
	// Without this, the zombie process keeps isProcessAlive returning true.
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
	}()

	// Poll every 2s for up to 120s (60 attempts)
	const pollInterval = 2 * time.Second
	const maxAttempts = 60
	start := time.Now()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		elapsed := time.Since(start).Truncate(time.Second)
		fmt.Fprintf(os.Stderr, "\r\033[K⏳ Waiting for SSM tunnel... %s", elapsed)
		time.Sleep(pollInterval)
		if IsPortListening(port) {
			fmt.Fprintf(os.Stderr, "\r\033[K") // clear spinner line
			log.Printf("SSM port forward ready on port %d (took %s)", port, time.Since(start).Truncate(time.Second))
			return port, nil
		}
		// Check if process died early
		select {
		case <-exited:
			fmt.Fprintf(os.Stderr, "\r\033[K") // clear spinner line
			logContent, _ := os.ReadFile(logFile.Name())
			return 0, fmt.Errorf("SSM process (PID %d) died. Log:\n%s", cmd.Process.Pid, string(logContent))
		default:
		}
	}

	fmt.Fprintf(os.Stderr, "\r\033[K") // clear spinner line
	logContent, _ := os.ReadFile(logFile.Name())
	return 0, fmt.Errorf("port %d not listening after 120s. SSM log:\n%s", port, string(logContent))
}

// StopAll terminates every SSM port-forwarding process.
func StopAll() int {
	forwards, err := ListForwards()
	if err != nil {
		log.Printf("Warning: failed to list forwards: %v", err)
		return 0
	}
	count := 0
	for _, f := range forwards {
		if killProcess(f.PID) {
			count++
		}
	}
	return count
}

// PruneDuplicates ensures at most one forward per target host.
// Keeps the first forward encountered, kills the rest.
func PruneDuplicates() int {
	forwards, err := ListForwards()
	if err != nil {
		return 0
	}

	byTarget := make(map[string][]Forward)
	for _, f := range forwards {
		byTarget[f.TargetHost] = append(byTarget[f.TargetHost], f)
	}

	total := 0
	for host, items := range byTarget {
		if len(items) <= 1 {
			continue
		}
		// Keep the first, kill the rest
		for _, f := range items[1:] {
			if killProcess(f.PID) {
				log.Printf("Pruned duplicate SSM forward for %s (port %d, PID %d)", host, f.LocalPort, f.PID)
				total++
			}
		}
	}
	return total
}

// CleanOldLogs removes SSM log files older than 24 hours.
// Called at startup to prevent log accumulation.
func CleanOldLogs() {
	dir := ssmLogDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func ssmLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	dir := filepath.Join(home, ".cache", "kube-ssm-proxy", "logs")
	os.MkdirAll(dir, 0o755)
	return dir
}

// killProcess sends SIGTERM, waits 2s, then SIGKILL if still alive.
func killProcess(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false
	}
	time.Sleep(2 * time.Second)
	// Try SIGKILL in case it's still running
	_ = proc.Signal(syscall.SIGKILL)
	return true
}
