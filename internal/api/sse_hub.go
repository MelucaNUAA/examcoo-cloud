package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// SseMessage is the message format sent to clients
type SseMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// sseClient represents a single SSE client
type sseClient struct {
	ch     chan []byte
	taskID string
}

// SseHub manages SSE connections
type SseHub struct {
	mu      sync.RWMutex
	clients map[*sseClient]bool
}

// NewSseHub creates a new SSE hub
func NewSseHub() *SseHub {
	return &SseHub{
		clients: make(map[*sseClient]bool),
	}
}

// Broadcast sends a message to all clients subscribed to a specific task
func (h *SseHub) Broadcast(taskID string, msg SseMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	count := 0
	for c := range h.clients {
		if c.taskID == taskID {
			select {
			case c.ch <- data:
				count++
			default:
				// Client buffer full, skip
			}
		}
	}
	if count == 0 {
		log.Printf("sse broadcast: no clients for task_id=%s", taskID)
	} else {
		log.Printf("sse broadcast: task_id=%s, clients=%d, type=%s", taskID, count, msg.Type)
	}
}

// BroadcastAll sends a message to all connected clients
func (h *SseHub) BroadcastAll(msg SseMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	for c := range h.clients {
		select {
		case c.ch <- data:
		default:
			// Client buffer full, skip
		}
	}
}

// removeClient removes a client from the hub
func (h *SseHub) removeClient(c *sseClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	close(c.ch)
}

// HandleSSE handles SSE connections
func (h *SseHub) HandleSSE(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		taskID = "_global"
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	c := &sseClient{
		ch:     make(chan []byte, 64),
		taskID: taskID,
	}

	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	log.Printf("sse client connected: task_id=%s", taskID)

	// Cleanup on disconnect
	defer func() {
		h.removeClient(c)
		log.Printf("sse client disconnected: task_id=%s", taskID)
	}()

	// Send initial comment to establish connection
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-c.ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
