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

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

// Reuse structs from preview.go (or move them to a common package)
// For now, redefining locally to avoid import cycles if I move things around later.
type WebRTCScreencastFrameParams struct {
	Data      string  `json:"data"`
	SessionID int     `json:"sessionId"`
	Metadata  struct {
		Timestamp float64 `json:"timestamp"`
	} `json:"metadata"`
}

func webrtcOfferHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	sess, ok := sessionManager.GetSession(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		log.Println("Failed to decode offer:", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Println("Received WebRTC offer")

	// Create PeerConnection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		http.Error(w, "Failed to create peer connection: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle incoming Data Channel
	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
		log.Println("New DataChannel:", d.Label())
		if d.Label() == "screencast" {
			d.OnOpen(func() {
				log.Println("Data channel 'screencast' opened")
				go streamScreencastToDataChannel(sess, d)
			})
		}
	})

	// Set the remote SessionDescription
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		log.Println("Failed to set remote description:", err)
		http.Error(w, "Failed to set remote description: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create an answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		log.Println("Failed to create answer:", err)
		http.Error(w, "Failed to create answer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Sets the LocalDescription, and starts our UDP listeners
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		log.Println("Failed to set local description:", err)
		http.Error(w, "Failed to set local description: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Gather candidates (wait for gathering complete for simplicity in this MVP)
	log.Println("Waiting for ICE gathering...")
	select {
	case <-webrtc.GatheringCompletePromise(peerConnection):
		log.Println("ICE gathering complete")
	case <-time.After(2 * time.Second):
		log.Println("ICE gathering timed out, proceeding")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peerConnection.LocalDescription())
}

func streamScreencastToDataChannel(sess interface{ GetWSURL() string }, dc *webrtc.DataChannel) {
	// Connect to CDP (Page Target logic from preview.go)
	// Parse port from Browser WS URL
	u, _ := url.Parse(sess.GetWSURL())
	port := u.Port()

	// Find Page Target
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/json", port))
	if err != nil {
		log.Println("Failed to query browser targets:", err)
		return
	}
	defer resp.Body.Close()

	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		log.Println("Failed to decode browser targets:", err)
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
		log.Println("No page target found")
		return
	}
	log.Println("Connecting to Page CDP:", pageWSURL)

	conn, _, err := websocket.DefaultDialer.Dial(pageWSURL, nil)
	if err != nil {
		log.Println("Failed to connect to page:", err)
		return
	}
	defer conn.Close()

	// Enable Page domain
	log.Println("Enabling Page domain...")
	conn.WriteJSON(map[string]interface{}{"id": 1, "method": "Page.enable"})

	// Bring to front
	log.Println("Bringing page to front...")
	conn.WriteJSON(map[string]interface{}{"id": 10, "method": "Page.bringToFront"})

	// Start Screencast
	log.Println("Starting screencast...")
	startMsg := map[string]interface{}{
		"id":     2,
		"method": "Page.startScreencast",
		"params": map[string]interface{}{
			"format": "jpeg",
			"quality": 80,
			"maxWidth": 1280,
			"maxHeight": 720,
		},
	}
	if err := conn.WriteJSON(startMsg); err != nil {
		log.Println("Failed to send startScreencast:", err)
	}

	// Navigate to example.com
	navMsg := map[string]interface{}{
		"id":     3,
		"method": "Page.navigate",
		"params": map[string]interface{}{
			"url": "https://www.google.com",
		},
	}
	conn.WriteJSON(navMsg)

	// Inject Animation Script to prove liveness
	// Inject Animation Script to prove liveness (Removed for production)
	// go func() { ... }()

	var writeMu sync.Mutex

	// Automated scrolling
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
				log.Println("Automation write error:", err)
				return
			}
		}
	}()

	var idCounter int64 = 100

	for {
		var msg CDPMessage
		// Read raw JSON first to debug
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error reading from CDP:", err)
			break
		}
		
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Println("Error unmarshalling CDP message:", err)
			continue
		}

		if msg.Method == "Page.screencastFrame" {
			// log.Println("Received screencast frame")
			var params WebRTCScreencastFrameParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				log.Println("Failed to unmarshal screencast params:", err)
				continue
			}

			data, err := base64.StdEncoding.DecodeString(params.Data)
			if err != nil {
				log.Println("Failed to decode base64 data:", err)
				continue
			}

			// Send metadata first
			metaMsg := map[string]interface{}{
				"type": "frame-start",
				"size": len(data),
			}
			metaJSON, _ := json.Marshal(metaMsg)
			if err := dc.SendText(string(metaJSON)); err != nil {
				log.Println("Failed to send metadata:", err)
				break
			}

			// Chunk and send
			const chunkSize = 60000 // Leave room for headers if needed, safe under 64k
			for i := 0; i < len(data); i += chunkSize {
				end := i + chunkSize
				if end > len(data) {
					end = len(data)
				}
				if err := dc.Send(data[i:end]); err != nil {
					log.Println("Failed to send chunk:", err)
					break
				}
			}

			// Ack
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
				log.Println("Failed to send Ack:", writeErr)
			} else {
				// log.Printf("Sent Ack for SessionID: %d, CmdID: %d\n", params.SessionID, idCounter)
			}
		} else {
			// log.Println("Received other message:", msg.Method)
		}
	}
}
