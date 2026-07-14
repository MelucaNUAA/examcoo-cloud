package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WsMessage is the message format sent to clients
type WsMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// WsHub manages WebSocket connections grouped by task_id
type WsHub struct {
	mu    sync.RWMutex
	conns map[string]map[*websocket.Conn]bool // task_id -> set of connections
}

// NewWsHub creates a new WebSocket hub
func NewWsHub() *WsHub {
	return &WsHub{
		conns: make(map[string]map[*websocket.Conn]bool),
	}
}

// Broadcast sends a message to all clients subscribed to a specific task
func (h *WsHub) Broadcast(taskID string, msg WsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	conns, ok := h.conns[taskID]
	if !ok {
		return
	}
	data, _ := json.Marshal(msg)
	for conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws write error: %v", err)
		}
	}
}

// BroadcastAll sends a message to all connected clients (for non-task events like bank-stats)
func (h *WsHub) BroadcastAll(msg WsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	for _, conns := range h.conns {
		for conn := range conns {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("ws write error: %v", err)
			}
		}
	}
}

// HandleWS upgrades HTTP connection to WebSocket and registers it
func (h *WsHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		taskID = "_global"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	h.mu.Lock()
	if h.conns[taskID] == nil {
		h.conns[taskID] = make(map[*websocket.Conn]bool)
	}
	h.conns[taskID][conn] = true
	h.mu.Unlock()

	log.Printf("ws client connected: task_id=%s", taskID)

	// Read loop to detect disconnection
	defer func() {
		h.mu.Lock()
		delete(h.conns[taskID], conn)
		if len(h.conns[taskID]) == 0 {
			delete(h.conns, taskID)
		}
		h.mu.Unlock()
		conn.Close()
		log.Printf("ws client disconnected: task_id=%s", taskID)
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
