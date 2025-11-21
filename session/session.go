package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	CDPURL    string    `json:"cdp_url"`
	Port      int       `json:"port"`

	cmd      *exec.Cmd
	cancel   context.CancelFunc
	wsURL    string
	mu       sync.Mutex
	isClosed bool
}

func NewSession(duration time.Duration) (*Session, error) {
	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	// Find Chrome executable
	chromePath := "google-chrome"
	if _, err := exec.LookPath(chromePath); err != nil {
		// Try macOS path
		macPath := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		if _, err := os.Stat(macPath); err == nil {
			chromePath = macPath
		}
	}

	cmd := exec.CommandContext(ctx, chromePath,
		"--headless=new",
		"--no-sandbox",
		// "--disable-gpu",
		// "--use-gl=swiftshader",
		// "--mute-audio",
		"--remote-debugging-port=0",
		"--user-data-dir=/tmp/chrome-profile-"+id,
		"--window-size=1920,1080", // Set a default window size
	)

	// Capture stderr to find the DevTools URL
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	// Parse DevTools URL from stderr
	wsURL, err := parseDevToolsURL(stderr)
	if err != nil {
		cancel()
		cmd.Process.Kill()
		return nil, fmt.Errorf("failed to parse devtools url: %w", err)
	}

	s := &Session{
		ID:        id,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(duration),
		cmd:       cmd,
		cancel:    cancel,
		wsURL:     wsURL,
	}

	// Auto-cleanup
	go func() {
		select {
		case <-time.After(duration):
			s.Stop()
		case <-ctx.Done():
			// Already stopped
		}
	}()

	return s, nil
}

func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isClosed {
		return
	}
	s.isClosed = true
	s.cancel()
	// Cleanup user data dir
	os.RemoveAll("/tmp/chrome-profile-" + s.ID)
}

func (s *Session) GetWSURL() string {
	return s.wsURL
}

func parseDevToolsURL(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	// Chrome prints: DevTools listening on ws://127.0.0.1:33693/devtools/browser/uuid
	re := regexp.MustCompile(`DevTools listening on (ws://.+)\n?`)

	// Read for a few seconds max
	timeout := time.After(5 * time.Second)
	ch := make(chan string)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			// log.Println("Chrome stderr:", line) // Debugging
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				ch <- matches[1]
				return
			}
		}
		close(ch)
	}()

	select {
	case url, ok := <-ch:
		if !ok {
			return "", fmt.Errorf("scanner closed without finding url")
		}
		return url, nil
	case <-timeout:
		return "", fmt.Errorf("timeout waiting for devtools url")
	}
}
