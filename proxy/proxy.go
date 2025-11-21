package proxy

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func ProxyCDP(w http.ResponseWriter, r *http.Request, targetURL string) {
	// Connect to the target Chrome CDP WebSocket
	targetWS, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		http.Error(w, "Failed to connect to browser CDP: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer targetWS.Close()

	// Upgrade the client connection to WebSocket
	clientWS, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Failed to upgrade client connection:", err)
		return
	}
	defer clientWS.Close()

	// Pipe messages
	errChan := make(chan error, 2)

	go func() {
		for {
			msgType, msg, err := targetWS.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			if err := clientWS.WriteMessage(msgType, msg); err != nil {
				errChan <- err
				return
			}
		}
	}()

	go func() {
		for {
			msgType, msg, err := clientWS.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			if err := targetWS.WriteMessage(msgType, msg); err != nil {
				errChan <- err
				return
			}
		}
	}()

	<-errChan
}
