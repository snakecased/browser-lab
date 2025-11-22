package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// Configuration
const (
	serverPort = "8080"
	serverURL  = "http://localhost:" + serverPort
)

type SessionResponse struct {
	ID         string    `json:"id"`
	CDPURL     string    `json:"cdp_url"`
	PreviewURL string    `json:"preview_url"`
}

type mockTransport struct {
	wsURL string
	rt    http.RoundTripper
}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/json/version") {
		body := fmt.Sprintf(`{
			"Browser": "Chrome/Headless",
			"Protocol-Version": "1.3",
			"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/120.0.0.0 Safari/537.36",
			"V8-Version": "12.0.0",
			"WebKit-Version": "537.36 (@0)",
			"webSocketDebuggerUrl": "%s"
		}`, t.wsURL)
		
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	}
	if t.rt == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.rt.RoundTrip(req)
}

func TestIntegration(t *testing.T) {
	// 1. Build the server
	t.Log("Building server...")
	serverBin := filepath.Join(os.TempDir(), "browser-server-test")
	// Build the package in the parent directory
	cmdBuild := exec.Command("go", "build", "-o", serverBin, "..")
	if out, err := cmdBuild.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build server: %v\nOutput: %s", err, out)
	}
	defer os.Remove(serverBin)

	// 2. Start the server
	t.Log("Starting server...")
	cmdServer := exec.Command(serverBin)
	// cmdServer.Stdout = os.Stdout // Uncomment for debugging
	// cmdServer.Stderr = os.Stderr
	if err := cmdServer.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		if cmdServer.Process != nil {
			cmdServer.Process.Kill()
		}
	}()

	// 3. Wait for server to be ready
	if err := waitForServer(serverURL + "/sessions"); err != nil {
		t.Fatalf("Server did not start in time: %v", err)
	}
	t.Log("Server is ready.")

	// 4. Create a session
	t.Log("Creating session...")
	sessResp, err := createSession()
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	t.Logf("Session created: ID=%s, CDP=%s", sessResp.ID, sessResp.CDPURL)
	defer stopSession(sessResp.ID)

	// 5. Connect CDP and perform actions
	t.Log("Connecting to CDP...")

	// Intercept http.DefaultTransport to mock the version check
	originalTransport := http.DefaultTransport
	mock := &mockTransport{
		wsURL: sessResp.CDPURL,
		rt:    originalTransport,
	}
	http.DefaultTransport = mock
	defer func() { http.DefaultTransport = originalTransport }()

	// Create a new allocator context using the remote WebSocket URL
	// chromedp uses http.DefaultClient to check /json/version
	allocatorContext, cancelAllocator := chromedp.NewRemoteAllocator(context.Background(), sessResp.CDPURL)
	defer cancelAllocator()

	// Create a CDP context
	ctx, cancelCtx := chromedp.NewContext(allocatorContext)
	defer cancelCtx()

	// Define the tasks
	t.Log("Navigating to Wikipedia...")
	var title string
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.wikipedia.org"),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}
	t.Logf("Page Title: %s", title)

	t.Log("Scrolling slowly...")
	// Scroll down in steps
	for i := 0; i < 5; i++ {
		err = chromedp.Run(ctx,
			chromedp.Evaluate(`window.scrollBy(0, 200);`, nil),
			chromedp.Sleep(500*time.Millisecond),
		)
		if err != nil {
			t.Fatalf("Failed to scroll: %v", err)
		}
	}

	t.Log("Moving mouse...")
	// Move mouse across the screen
	err = chromedp.Run(ctx,
		chromedp.MouseEvent(input.MouseMoved, 100, 100),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.MouseEvent(input.MouseMoved, 200, 200),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.MouseEvent(input.MouseMoved, 300, 300),
	)
	if err != nil {
		t.Fatalf("Failed to move mouse: %v", err)
	}

	t.Log("Test scenario completed successfully.")
}

func waitForServer(url string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func createSession() (*SessionResponse, error) {
	reqBody, _ := json.Marshal(map[string]int{"duration_minutes": 5})
	resp, err := http.Post(serverURL+"/sessions", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var sessionResp SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return nil, err
	}
	return &sessionResp, nil
}

func stopSession(id string) error {
	req, err := http.NewRequest("DELETE", serverURL+"/sessions/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil
}
