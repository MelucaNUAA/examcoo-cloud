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
	pingPeriod     = 50 * time.Second
	maxMessageSize = 512
)

// WsMessage is the message format sent to clients
type WsMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// client represents a single WebSocket client
type client struct {
	conn   *websocket.Conn
	taskID string
}

// WsHub manages WebSocket connections
type WsHub struct {
	mu      sync.RWMutex
	clients map[*client]bool
}

// NewWsHub creates a new WebSocket hub
func NewWsHub() *WsHub {
	return &WsHub{
		clients: make(map[*client]bool),
	}
}

// Broadcast sends a message to all clients subscribed to a specific task
func (h *WsHub) Broadcast(taskID string, msg WsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	count := 0
	for c := range h.clients {
		if c.taskID == taskID {
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("ws write error: %v", err)
			}
			count++
		}
	}
	if count == 0 {
		log.Printf("ws broadcast: no clients for task_id=%s", taskID)
	} else {
		log.Printf("ws broadcast: task_id=%s, clients=%d, type=%s", taskID, count, msg.Type)
	}
}

// BroadcastAll sends a message to all connected clients
func (h *WsHub) BroadcastAll(msg WsMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	data, _ := json.Marshal(msg)
	for c := range h.clients {
		c.conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws write error: %v", err)
		}
	}
}

// removeClient removes a client from the hub
func (h *WsHub) removeClient(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
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

	c := &client{conn: conn, taskID: taskID}

	h.mu.Lock()
	h.clients[c] = true
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
					close(done)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read pump - detects disconnection
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()

	// Wait for disconnection
	<-done

	// Cleanup
	h.removeClient(c)
	conn.Close()
	log.Printf("ws client disconnected: task_id=%s", taskID)
}
