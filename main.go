package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"browser-server/proxy"
	"browser-server/session"

	"github.com/gorilla/mux"
)

var sessionManager *session.Manager

type CreateSessionRequest struct {
	DurationMinutes int `json:"duration_minutes"`
}

type SessionResponse struct {
	ID         string    `json:"id"`
	CDPURL     string    `json:"cdp_url"`
	PreviewURL string    `json:"preview_url"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func main() {
	sessionManager = session.NewManager()
	r := mux.NewRouter()

	// API Endpoints
	r.HandleFunc("/sessions", createSessionHandler).Methods("POST")
	r.HandleFunc("/sessions", listSessionsHandler).Methods("GET")
	r.HandleFunc("/sessions/{id}", stopSessionHandler).Methods("DELETE")

	// Proxy & Preview
	r.HandleFunc("/sessions/{id}/cdp", cdpProxyHandler)

	// WHIP (WebRTC-HTTP Ingestion Protocol) endpoints
	r.HandleFunc("/sessions/{id}/whip", whipHandler).Methods("POST")
	r.HandleFunc("/sessions/{id}/whip/{resourceId}", whipResourceHandler).Methods("PATCH", "DELETE")

	// Static files for dashboard
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./dashboard")))

	port := ":8080"
	log.Printf("Server listening on %s", port)
	log.Fatal(http.ListenAndServe(port, r))
}

func resolveHost(r *http.Request) string {
	if host := os.Getenv("APP_HOST"); host != "" {
		return host
	}
	return r.Host
}

// resolveScheme determines if the request came over HTTPS or HTTP
func resolveScheme(r *http.Request) string {
	// Check X-Forwarded-Proto header (set by reverse proxies like Tailscale Funnel)
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}

	// Check if TLS is being used
	if r.TLS != nil {
		return "https"
	}

	// Check if APP_HOST is set (indicates Tailscale Funnel usage)
	if os.Getenv("APP_HOST") != "" {
		return "https"
	}

	return "http"
}

// resolveWSScheme returns the WebSocket scheme (ws or wss)
func resolveWSScheme(r *http.Request) string {
	if resolveScheme(r) == "https" {
		return "wss"
	}
	return "ws"
}

func createSessionHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to 10 minutes if parsing fails or empty
		req.DurationMinutes = 5
	}
	if req.DurationMinutes <= 0 {
		req.DurationMinutes = 5
	}

	duration := time.Duration(req.DurationMinutes) * time.Minute
	sess, err := sessionManager.CreateSession(duration)
	if err != nil {
		http.Error(w, "Failed to create session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	host := resolveHost(r)
	scheme := resolveScheme(r)
	wsScheme := resolveWSScheme(r)

	resp := SessionResponse{
		ID:         sess.ID,
		CDPURL:     fmt.Sprintf("%s://%s/sessions/%s/cdp", wsScheme, host, sess.ID),
		PreviewURL: fmt.Sprintf("%s://%s/sessions/%s/preview", scheme, host, sess.ID),
		CreatedAt:  sess.CreatedAt,
		ExpiresAt:  sess.ExpiresAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func listSessionsHandler(w http.ResponseWriter, r *http.Request) {
	sessions := sessionManager.ListSessions()
	host := resolveHost(r)
	scheme := resolveScheme(r)
	wsScheme := resolveWSScheme(r)

	resp := make([]SessionResponse, 0)
	for _, s := range sessions {
		resp = append(resp, SessionResponse{
			ID:         s.ID,
			CDPURL:     fmt.Sprintf("%s://%s/sessions/%s/cdp", wsScheme, host, s.ID),
			PreviewURL: fmt.Sprintf("%s://%s/sessions/%s/preview", scheme, host, s.ID),
			CreatedAt:  s.CreatedAt,
			ExpiresAt:  s.ExpiresAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func stopSessionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	sessionManager.DeleteSession(id)
	w.WriteHeader(http.StatusOK)
}

func cdpProxyHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	sess, ok := sessionManager.GetSession(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	proxy.ProxyCDP(w, r, sess.GetWSURL())
}
