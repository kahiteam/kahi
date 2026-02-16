// Package ctl implements the CLI control client for communicating
// with a running Kahi daemon over its Unix socket or TCP API.
package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Client communicates with a Kahi daemon API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	username   string
	password   string
}

// NewUnixClient creates a client that connects via Unix socket.
func NewUnixClient(socketPath string) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		},
		baseURL: "http://unix",
	}
}

// NewTCPClient creates a client that connects via TCP.
func NewTCPClient(addr, username, password string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "http://" + addr,
		username:   username,
		password:   password,
	}
}

func (c *Client) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return c.httpClient.Do(req)
}

func (c *Client) doJSON(method, path string, body io.Reader) (map[string]any, error) {
	resp, err := c.do(method, path, body)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}

	if resp.StatusCode >= 400 {
		msg := "unknown error"
		if e, ok := result["error"].(string); ok {
			msg = e
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return result, nil
}

// ProcessInfo is the JSON structure returned by the API.
type ProcessInfo struct {
	Name        string `json:"name"`
	Group       string `json:"group"`
	State       string `json:"state"`
	StateCode   int    `json:"statecode"`
	PID         int    `json:"pid"`
	Uptime      int64  `json:"uptime"`
	Description string `json:"description"`
	ExitStatus  int    `json:"exitstatus"`
}

// --- Process control operations ---

// Start starts a process by name.
func (c *Client) Start(name string) error {
	_, err := c.doJSON("POST", "/api/v1/processes/"+name+"/start", nil)
	return err
}

// Stop stops a process by name.
func (c *Client) Stop(name string) error {
	_, err := c.doJSON("POST", "/api/v1/processes/"+name+"/stop", nil)
	return err
}

// Restart restarts a process by name.
func (c *Client) Restart(name string) error {
	_, err := c.doJSON("POST", "/api/v1/processes/"+name+"/restart", nil)
	return err
}

// Signal sends a signal to a process.
func (c *Client) Signal(name, sig string) error {
	body := fmt.Sprintf(`{"signal":"%s"}`, sig)
	_, err := c.doJSON("POST", "/api/v1/processes/"+name+"/signal", strings.NewReader(body))
	return err
}

// StartGroup starts all processes in a group.
func (c *Client) StartGroup(name string) error {
	_, err := c.doJSON("POST", "/api/v1/groups/"+name+"/start", nil)
	return err
}

// StopGroup stops all processes in a group.
func (c *Client) StopGroup(name string) error {
	_, err := c.doJSON("POST", "/api/v1/groups/"+name+"/stop", nil)
	return err
}

// RestartGroup restarts all processes in a group.
func (c *Client) RestartGroup(name string) error {
	_, err := c.doJSON("POST", "/api/v1/groups/"+name+"/restart", nil)
	return err
}

// --- Status display ---

// Status retrieves and formats process status.
func (c *Client) Status(names []string, jsonOutput bool, w io.Writer) error {
	resp, err := c.do("GET", "/api/v1/processes", nil)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var procs []ProcessInfo
	if err := json.NewDecoder(resp.Body).Decode(&procs); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}

	// Filter by names if specified.
	if len(names) > 0 {
		filter := make(map[string]bool)
		for _, n := range names {
			filter[n] = true
		}
		var filtered []ProcessInfo
		for _, p := range procs {
			if filter[p.Name] {
				filtered = append(filtered, p)
			}
		}
		procs = filtered
	}

	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(procs)
	}

	return formatStatusTable(procs, w, isTerminal(w))
}

func formatStatusTable(procs []ProcessInfo, w io.Writer, color bool) error {
	// Sort by name.
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].Name < procs[j].Name
	})

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "NAME\tSTATE\tPID\tUPTIME\tDESCRIPTION\n")

	for _, p := range procs {
		state := p.State
		if color {
			state = colorState(p.State)
		}

		pid := "-"
		if p.PID > 0 {
			pid = fmt.Sprintf("%d", p.PID)
		}

		uptime := "-"
		if p.Uptime > 0 {
			uptime = formatDuration(time.Duration(p.Uptime) * time.Second)
		}

		desc := p.Description
		if p.State == "EXITED" {
			desc = fmt.Sprintf("exit %d", p.ExitStatus)
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, state, pid, uptime, desc)
	}
	return tw.Flush()
}

func colorState(state string) string {
	switch state {
	case "RUNNING":
		return "\033[32m" + state + "\033[0m"
	case "FATAL":
		return "\033[31m" + state + "\033[0m"
	case "STARTING", "BACKOFF":
		return "\033[33m" + state + "\033[0m"
	case "STOPPING":
		return "\033[33m" + state + "\033[0m"
	default:
		return state
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, _ := f.Stat()
		return stat != nil && (stat.Mode()&os.ModeCharDevice) != 0
	}
	return false
}

// --- Log tailing ---

// Tail reads log output from a process.
func (c *Client) Tail(name, stream string, bytes int, w io.Writer) error {
	if stream == "" {
		stream = "stdout"
	}
	path := fmt.Sprintf("/api/v1/processes/%s/log/%s?length=%d", name, stream, bytes)
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
			return fmt.Errorf("server error (status %d)", resp.StatusCode)
		}
		return fmt.Errorf("%s", errBody["error"])
	}

	_, err = io.Copy(w, resp.Body)
	return err
}

// TailFollow streams log output via SSE.
func (c *Client) TailFollow(ctx context.Context, name, stream string, w io.Writer) error {
	if stream == "" {
		stream = "stdout"
	}
	path := fmt.Sprintf("/api/v1/processes/%s/log/%s/stream", name, stream)
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errBody map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
			return fmt.Errorf("server error (status %d)", resp.StatusCode)
		}
		return fmt.Errorf("%s", errBody["error"])
	}

	// Parse SSE stream.
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Extract data lines from SSE.
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				if strings.HasPrefix(line, "data: ") {
					fmt.Fprintln(w, line[6:])
				}
			}
		}
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// --- Daemon operations ---

// Shutdown initiates daemon shutdown.
func (c *Client) Shutdown() error {
	_, err := c.doJSON("POST", "/api/v1/shutdown", nil)
	return err
}

// Reload triggers config reload.
func (c *Client) Reload() (map[string]any, error) {
	return c.doJSON("POST", "/api/v1/config/reload", nil)
}

// Version returns daemon version info.
func (c *Client) Version() (map[string]any, error) {
	return c.doJSON("GET", "/api/v1/version", nil)
}

// PID returns the daemon PID.
func (c *Client) PID(name string) (string, error) {
	if name == "" {
		// Daemon PID -- use version endpoint which includes PID.
		result, err := c.doJSON("GET", "/api/v1/version", nil)
		if err != nil {
			return "", err
		}
		if pid, ok := result["pid"]; ok {
			return fmt.Sprintf("%v", pid), nil
		}
		return "", fmt.Errorf("pid not available from version endpoint")
	}

	// Process PID.
	resp, err := c.do("GET", "/api/v1/processes/"+name, nil)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var info ProcessInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("no such process: %s", name)
	}
	return fmt.Sprintf("%d", info.PID), nil
}

// --- Health checks ---

// Health checks daemon liveness.
func (c *Client) Health() (string, error) {
	resp, err := c.do("GET", "/healthz", nil)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	return body["status"], nil
}

// Ready checks daemon readiness, optionally filtering by process names.
func (c *Client) Ready(processes []string) (string, error) {
	path := "/readyz"
	if len(processes) > 0 {
		path += "?process=" + strings.Join(processes, ",")
	}
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("invalid response: %w", err)
	}
	status, _ := body["status"].(string)
	return status, nil
}

// Reread previews config changes without applying.
func (c *Client) Reread() (map[string]any, error) {
	return c.doJSON("POST", "/api/v1/config/reload", nil)
}

// WriteStdin writes data to a process's stdin.
func (c *Client) WriteStdin(name, data string) error {
	body := fmt.Sprintf(`{"data":"%s\n"}`, data)
	_, err := c.doJSON("POST", "/api/v1/processes/"+name+"/stdin",
		bytes.NewReader([]byte(body)))
	return err
}
