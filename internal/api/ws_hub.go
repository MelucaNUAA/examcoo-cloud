package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 50 * time.Second // Must be less than pongWait
	maxMessageSize = 512
)

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
		log.Printf("ws broadcast: no clients for task_id=%s", taskID)
		return
	}
	data, _ := json.Marshal(msg)
	log.Printf("ws broadcast: task_id=%s, clients=%d, type=%s", taskID, len(conns), msg.Type)
	for conn := range conns {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws write error: %v", err)
		}
	}
}

// BroadcastAll sends a message to all connected clients
func (h *WsHub) BroadcastAll(msg WsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	for _, conns := range h.conns {
		for conn := range conns {
			conn.SetWriteDeadline(time.Now().Add(writeWait))
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

	// Set connection parameters
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	h.mu.Lock()
	if h.conns[taskID] == nil {
		h.conns[taskID] = make(map[*websocket.Conn]bool)
	}
	h.conns[taskID][conn] = true
	h.mu.Unlock()

	log.Printf("ws client connected: task_id=%s", taskID)

	// Start ping ticker
	ticker := time.NewTicker(pingPeriod)
	done := make(chan struct{})

	// Write pump - sends pings
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read pump - detects disconnection
	defer func() {
		close(done)
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
