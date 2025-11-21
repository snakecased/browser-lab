package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// CDP Messages
type CDPMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ScreencastFrameParams struct {
	Data      string  `json:"data"`
	SessionID int     `json:"sessionId"`
	Metadata  struct {
		Timestamp float64 `json:"timestamp"`
	} `json:"metadata"`
}

func previewHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	sess, ok := sessionManager.GetSession(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Parse port from Browser WS URL
	u, _ := url.Parse(sess.GetWSURL())
	port := u.Port()

	// Find Page Target
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/json", port))
	if err != nil {
		http.Error(w, "Failed to query browser targets: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		http.Error(w, "Failed to decode browser targets: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Log targets for debugging
	targetsJSON, _ := json.Marshal(targets)
	log.Println("Browser targets:", string(targetsJSON))

	var pageWSURL string
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			pageWSURL = t.WebSocketDebuggerURL
			break
		}
	}

	if pageWSURL == "" {
		http.Error(w, "No page target found", http.StatusNotFound)
		return
	}

	// Connect to Page CDP
	conn, _, err := websocket.DefaultDialer.Dial(pageWSURL, nil)
	if err != nil {
		http.Error(w, "Failed to connect to page: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Enable Page domain
	enableMsg := map[string]interface{}{
		"id":     4,
		"method": "Page.enable",
	}
	if err := conn.WriteJSON(enableMsg); err != nil {
		log.Println("Failed to enable Page domain:", err)
	}

	// Navigate to example.com (Test)
	navMsg := map[string]interface{}{
		"id":     2,
		"method": "Page.navigate",
		"params": map[string]interface{}{
			"url": "https://www.example.com",
		},
	}
	if err := conn.WriteJSON(navMsg); err != nil {
		log.Println("Failed to navigate:", err)
	} else {
		log.Println("Navigation command sent")
	}

	// Bring to front
	btfMsg := map[string]interface{}{
		"id":     3,
		"method": "Page.bringToFront",
	}
	conn.WriteJSON(btfMsg)

	// Start Screencast
	log.Println("Starting screencast...")
	startMsg := map[string]interface{}{
		"id":     1,
		"method": "Page.startScreencast",
		"params": map[string]interface{}{
			"format": "jpeg",
			// "quality":       80,
			// "maxWidth":      1280,
			// "maxHeight":     720,
			// "everyNthFrame": 1,
		},
	}
	if err := conn.WriteJSON(startMsg); err != nil {
		log.Println("Failed to start screencast:", err)
		http.Error(w, "Failed to start screencast: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println("Screencast started command sent")

	// Set up MJPEG stream
	m := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+m.Boundary())

	// Read loop
	for {
		var msg CDPMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Println("Error reading from CDP:", err)
			break
		}

		if msg.Method == "Page.screencastFrame" {
			// log.Println("Received screencast frame")
			var params ScreencastFrameParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				log.Println("Error unmarshalling frame params:", err)
				continue
			}

			// Decode Base64 image
			data, err := base64.StdEncoding.DecodeString(params.Data)
			if err != nil {
				log.Println("Error decoding base64:", err)
				continue
			}

			// Write frame
			partHeader := make(textproto.MIMEHeader)
			partHeader.Set("Content-Type", "image/jpeg")
			partHeader.Set("Content-Length", fmt.Sprintf("%d", len(data)))
			part, err := m.CreatePart(partHeader)
			if err != nil {
				log.Println("Error creating multipart part:", err)
				break
			}
			if _, err := part.Write(data); err != nil {
				log.Println("Error writing part data:", err)
				break
			}
			
			// Flush if possible (though multipart writer usually handles this, explicit flush helps with buffering issues)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			// Acknowledge frame
			ackCmd := map[string]interface{}{
				"id":     time.Now().UnixNano(), 
				"method": "Page.screencastFrameAck",
				"params": map[string]interface{}{
					"sessionId": params.SessionID,
				},
			}
			if err := conn.WriteJSON(ackCmd); err != nil {
				log.Println("Error sending ack:", err)
				break
			}
		} else {
			log.Println("Received other message:", msg.Method, string(msg.Params))
			// Check if it's an error
			// if msg.Error != nil ... (need to update struct)
		}
	}
}
