package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// WHIPResource represents an active WHIP session
type WHIPResource struct {
	ID             string
	PeerConnection *webrtc.PeerConnection
	SessionID      string
	DataChannel    *webrtc.DataChannel
	mu             sync.Mutex
}

var (
	whipResources   = make(map[string]*WHIPResource)
	whipResourcesMu sync.RWMutex
)

// whipHandler implements the WHIP (WebRTC-HTTP Ingestion Protocol) endpoint
// POST /sessions/{id}/whip - Creates a new WHIP resource
func whipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	sessionID := vars["id"]

	sess, ok := sessionManager.GetSession(sessionID)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Verify Content-Type is application/sdp
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/sdp" {
		http.Error(w, "Content-Type must be application/sdp", http.StatusBadRequest)
		return
	}

	// Read the SDP offer from request body
	var offerSDP []byte
	offerSDP = make([]byte, r.ContentLength)
	_, err := r.Body.Read(offerSDP)
	if err != nil && err.Error() != "EOF" {
		http.Error(w, "Failed to read SDP offer: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Parse the SDP offer
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(offerSDP),
	}

	log.Printf("WHIP: Received offer for session %s", sessionID)

	// Create PeerConnection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
			{
				URLs: []string{"stun:stun.cloudflare.com:3478"},
			},
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		http.Error(w, "Failed to create peer connection: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create WHIP resource
	resourceID := uuid.New().String()
	resource := &WHIPResource{
		ID:             resourceID,
		PeerConnection: peerConnection,
		SessionID:      sessionID,
	}

	// Handle incoming Data Channel from client
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Printf("WHIP: New DataChannel '%s' for resource %s", d.Label(), resourceID)
		if d.Label() == "screencast" {
			resource.mu.Lock()
			resource.DataChannel = d
			resource.mu.Unlock()

			d.OnOpen(func() {
				log.Printf("WHIP: Data channel 'screencast' opened for resource %s", resourceID)
				go streamScreencastToDataChannel(sess, d)
			})

			d.OnClose(func() {
				log.Printf("WHIP: Data channel 'screencast' closed for resource %s", resourceID)
			})
		}
	})

	// Handle connection state changes
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("WHIP: Connection state changed to %s for resource %s", state.String(), resourceID)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			// Clean up the resource
			whipResourcesMu.Lock()
			delete(whipResources, resourceID)
			whipResourcesMu.Unlock()
		}
	})

	// Set the remote SessionDescription (the offer)
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		log.Printf("WHIP: Failed to set remote description: %v", err)
		http.Error(w, "Failed to set remote description: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create an answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Printf("WHIP: Failed to create answer: %v", err)
		http.Error(w, "Failed to create answer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Set the LocalDescription
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		log.Printf("WHIP: Failed to set local description: %v", err)
		http.Error(w, "Failed to set local description: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Wait for ICE gathering to complete
	log.Printf("WHIP: Waiting for ICE gathering for resource %s", resourceID)
	select {
	case <-webrtc.GatheringCompletePromise(peerConnection):
		log.Printf("WHIP: ICE gathering complete for resource %s", resourceID)
	case <-time.After(3 * time.Second):
		log.Printf("WHIP: ICE gathering timed out for resource %s, proceeding", resourceID)
	}

	// Store the resource
	whipResourcesMu.Lock()
	whipResources[resourceID] = resource
	whipResourcesMu.Unlock()

	// Respond with the answer SDP
	// WHIP requires:
	// - Status: 201 Created
	// - Content-Type: application/sdp
	// - Location header with the resource URL for PATCH/DELETE operations
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", fmt.Sprintf("/sessions/%s/whip/%s", sessionID, resourceID))
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(peerConnection.LocalDescription().SDP))

	log.Printf("WHIP: Created resource %s for session %s", resourceID, sessionID)
}

// whipResourceHandler handles PATCH and DELETE operations on a WHIP resource
// PATCH /sessions/{id}/whip/{resourceId} - Updates ICE candidates (trickle ICE)
// DELETE /sessions/{id}/whip/{resourceId} - Terminates the WHIP session
func whipResourceHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	resourceID := vars["resourceId"]

	whipResourcesMu.RLock()
	resource, ok := whipResources[resourceID]
	whipResourcesMu.RUnlock()

	if !ok {
		http.Error(w, "WHIP resource not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPatch:
		// PATCH is used for trickle ICE (optional in this implementation)
		// For now, we'll return 204 No Content to indicate we don't need additional ICE candidates
		w.WriteHeader(http.StatusNoContent)
		log.Printf("WHIP: PATCH request received for resource %s (trickle ICE not implemented)", resourceID)

	case http.MethodDelete:
		// DELETE terminates the WHIP session
		resource.mu.Lock()
		if resource.PeerConnection != nil {
			resource.PeerConnection.Close()
		}
		resource.mu.Unlock()

		whipResourcesMu.Lock()
		delete(whipResources, resourceID)
		whipResourcesMu.Unlock()

		w.WriteHeader(http.StatusOK)
		log.Printf("WHIP: Deleted resource %s", resourceID)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// streamScreencastToDataChannel streams browser screencast frames via WebRTC data channel
func streamScreencastToDataChannel(sess interface{ GetWSURL() string }, dc *webrtc.DataChannel) {
	// Connect to CDP (Page Target logic)
	u, _ := url.Parse(sess.GetWSURL())
	port := u.Port()

	// Find Page Target
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/json", port))
	if err != nil {
		log.Println("WHIP: Failed to query browser targets:", err)
		return
	}
	defer resp.Body.Close()

	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		log.Println("WHIP: Failed to decode browser targets:", err)
		return
	}

	var pageWSURL string
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			pageWSURL = t.WebSocketDebuggerURL
			break
		}
	}

	if pageWSURL == "" {
		log.Println("WHIP: No page target found")
		return
	}
	log.Println("WHIP: Connecting to Page CDP:", pageWSURL)

	conn, _, err := websocket.DefaultDialer.Dial(pageWSURL, nil)
	if err != nil {
		log.Println("WHIP: Failed to connect to page:", err)
		return
	}
	defer conn.Close()

	// Enable Page domain
	log.Println("WHIP: Enabling Page domain...")
	conn.WriteJSON(map[string]interface{}{"id": 1, "method": "Page.enable"})

	// Bring to front
	log.Println("WHIP: Bringing page to front...")
	conn.WriteJSON(map[string]interface{}{"id": 10, "method": "Page.bringToFront"})

	// Navigate to google.com FIRST before starting screencast
	log.Println("WHIP: Navigating to google.com...")
	navMsg := map[string]interface{}{
		"id":     3,
		"method": "Page.navigate",
		"params": map[string]interface{}{
			"url": "https://www.google.com",
		},
	}
	conn.WriteJSON(navMsg)

	// Wait for page to start loading
	time.Sleep(2 * time.Second)

	// Start Screencast AFTER navigation
	log.Println("WHIP: Starting screencast...")
	startMsg := map[string]interface{}{
		"id":     2,
		"method": "Page.startScreencast",
		"params": map[string]interface{}{
			"format":        "jpeg",
			"quality":       80,
			"maxWidth":      1280,
			"maxHeight":     720,
			"everyNthFrame": 1, // Send every frame
		},
	}
	if err := conn.WriteJSON(startMsg); err != nil {
		log.Println("WHIP: Failed to send startScreencast:", err)
		return
	}

	var writeMu sync.Mutex

	// Automated scrolling to demonstrate liveness
	go func() {
		time.Sleep(3 * time.Second) // Wait for page load
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		scrollDown := true
		for range ticker.C {
			y := 100
			if !scrollDown {
				y = -100
			}
			scrollDown = !scrollDown

			expr := fmt.Sprintf("window.scrollBy(0, %d);", y)
			cmd := map[string]interface{}{
				"id":     time.Now().UnixNano(),
				"method": "Runtime.evaluate",
				"params": map[string]interface{}{
					"expression": expr,
				},
			}

			writeMu.Lock()
			err := conn.WriteJSON(cmd)
			writeMu.Unlock()

			if err != nil {
				log.Println("WHIP: Automation write error:", err)
				return
			}
		}
	}()

	var idCounter int64 = 100
	frameCount := 0

	// Read loop for screencast frames
	log.Println("WHIP: Starting CDP message read loop...")
	for {
		var msg CDPMessage
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("WHIP: Error reading from CDP:", err)
			break
		}

		if err := json.Unmarshal(message, &msg); err != nil {
			log.Println("WHIP: Error unmarshalling CDP message:", err)
			continue
		}

		// Log non-frame CDP messages for debugging
		if msg.Method != "" && msg.Method != "Page.screencastFrame" {
			log.Printf("WHIP: Received CDP message: %s", msg.Method)
		}

		if msg.Method == "Page.screencastFrame" {
			frameCount++
			if frameCount == 1 || frameCount%30 == 0 {
				log.Printf("WHIP: Received screencast frame #%d", frameCount)
			}

			var params WebRTCScreencastFrameParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				log.Println("WHIP: Failed to unmarshal screencast params:", err)
				continue
			}

			data, err := base64.StdEncoding.DecodeString(params.Data)
			if err != nil {
				log.Println("WHIP: Failed to decode base64 data:", err)
				continue
			}

			// Send metadata first
			metaMsg := map[string]interface{}{
				"type": "frame-start",
				"size": len(data),
			}
			metaJSON, _ := json.Marshal(metaMsg)
			if err := dc.SendText(string(metaJSON)); err != nil {
				log.Println("WHIP: Failed to send metadata:", err)
				break
			}

			// Chunk and send binary data
			const chunkSize = 60000 // Safe under 64KB limit
			for i := 0; i < len(data); i += chunkSize {
				end := i + chunkSize
				if end > len(data) {
					end = len(data)
				}
				if err := dc.Send(data[i:end]); err != nil {
					log.Println("WHIP: Failed to send chunk:", err)
					break
				}
			}

			// Acknowledge the frame
			idCounter++
			ackCmd := map[string]interface{}{
				"id":     idCounter,
				"method": "Page.screencastFrameAck",
				"params": map[string]interface{}{
					"sessionId": params.SessionID,
				},
			}
			writeMu.Lock()
			writeErr := conn.WriteJSON(ackCmd)
			writeMu.Unlock()
			if writeErr != nil {
				log.Println("WHIP: Failed to send Ack:", writeErr)
			}
		}
	}
}

// CDPMessage represents a Chrome DevTools Protocol message
type CDPMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// WebRTCScreencastFrameParams represents the parameters of a screencast frame
type WebRTCScreencastFrameParams struct {
	Data      string `json:"data"`
	SessionID int    `json:"sessionId"`
	Metadata  struct {
		Timestamp float64 `json:"timestamp"`
	} `json:"metadata"`
}
