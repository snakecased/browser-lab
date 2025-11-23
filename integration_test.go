package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

type testSessionResponse struct {
	ID         string `json:"id"`
	CDPURL     string `json:"cdp_url"`
	PreviewURL string `json:"preview_url"`
}

func TestIntegration(t *testing.T) {
	// Check if server is already running, if not skip test
	t.Log("Checking if server is running...")
	if err := waitForServer(serverURL + "/sessions"); err != nil {
		t.Skip("Server is not running. Please start the server first with: ./browser-lab.sh start-funnel")
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

	// Connect directly to the WebSocket URL using chromedp's WebSocket dialer
	// Skip NewRemoteAllocator since it has issues with our URL structure
	wsURL := sessResp.CDPURL
	t.Logf("Connecting directly to WebSocket: %s", wsURL)

	// Create allocator context with the WebSocket URL directly
	allocatorContext, cancelAllocator := chromedp.NewRemoteAllocator(context.Background(), wsURL)
	defer cancelAllocator()

	// Create a CDP context
	ctx, cancelCtx := chromedp.NewContext(allocatorContext)
	defer cancelCtx()

	// Define the tasks with longer timeout
	t.Log("Navigating to example.com...")
	var title string

	// Create a context with a longer timeout for navigation
	navCtx, navCancel := context.WithTimeout(ctx, 30*time.Second)
	defer navCancel()

	err = chromedp.Run(navCtx,
		chromedp.Navigate("http://example.com"),
		chromedp.WaitReady("body"),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}
	t.Logf("Page Title: %s", title)

	// Try Wikipedia too
	t.Log("Navigating to Wikipedia...")
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.wikipedia.org"),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Logf("Warning: Failed to navigate to Wikipedia: %v", err)
	} else {
		t.Logf("Wikipedia Page Title: %s", title)
	}

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

func createSession() (*testSessionResponse, error) {
	reqBody, _ := json.Marshal(map[string]int{"duration_minutes": 5})
	resp, err := http.Post(serverURL+"/sessions", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var sessionResp testSessionResponse
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
