package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/gorilla/websocket"
)

type WebSocketServer struct {
	upgrader     websocket.Upgrader
	connections  map[*websocket.Conn]bool
	mutex        sync.RWMutex
	activeConns  int64
	statsdClient *statsd.Client
}

type Message struct {
	Type      string    `json:"type"`
	Name      string    `json:"name,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func NewWebSocketServer() *WebSocketServer {
	// Setup StatsD client
	statsdClient, err := statsd.New("localhost:8125")
	if err != nil {
		log.Printf("Warning: Failed to create StatsD client: %v", err)
		statsdClient = nil
	}

	return &WebSocketServer{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow connections from any origin
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		connections:  make(map[*websocket.Conn]bool),
		statsdClient: statsdClient,
	}
}

func (ws *WebSocketServer) handleConnection(w http.ResponseWriter, r *http.Request) {

	// Upgrade HTTP connection to WebSocket
	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Add connection to active connections
	ws.mutex.Lock()
	ws.connections[conn] = true
	activeCount := atomic.AddInt64(&ws.activeConns, 1)
	ws.mutex.Unlock()

	log.Printf("New WebSocket connection established. Active connections: %d", activeCount)

	// Send metrics to StatsD
	if ws.statsdClient != nil {
		ws.statsdClient.Incr("websocket.connection.established", []string{}, 1)
		ws.statsdClient.Gauge("websocket.connections.active", float64(activeCount), []string{}, 1)
	}

	defer func() {
		// Remove connection when done
		ws.mutex.Lock()
		delete(ws.connections, conn)
		activeCount := atomic.AddInt64(&ws.activeConns, -1)
		ws.mutex.Unlock()

		if ws.statsdClient != nil {
			ws.statsdClient.Incr("websocket.connection.closed", []string{}, 1)
			ws.statsdClient.Gauge("websocket.connections.active", float64(activeCount), []string{}, 1)
		}
	}()

	// Handle messages in a loop
	for {
		messageStart := time.Now()

		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Process message based on type
		switch msg.Type {
		case "hello":
			response := Message{
				Type:      "hello_response",
				Message:   fmt.Sprintf("Hello, %s! (WebSocket)", msg.Name),
				Timestamp: time.Now(),
			}

			err = conn.WriteJSON(response)
			if err != nil {
				log.Printf("Write error: %v", err)
				break
			}

		case "ping":
			response := Message{
				Type:      "pong",
				Message:   "pong",
				Timestamp: time.Now(),
			}

			err = conn.WriteJSON(response)
			if err != nil {
				log.Printf("Write error: %v", err)
				break
			}

		default:
			response := Message{
				Type:      "error",
				Message:   "Unknown message type",
				Timestamp: time.Now(),
			}

			err = conn.WriteJSON(response)
			if err != nil {
				log.Printf("Write error: %v", err)
				break
			}
		}

		// Send latency metrics to StatsD
		if ws.statsdClient != nil {
			latency := time.Since(messageStart)
			ws.statsdClient.Timing("websocket.message.latency", latency, []string{"type:" + msg.Type}, 1)
			ws.statsdClient.Incr("websocket.message.count", []string{"type:" + msg.Type, "status:success"}, 1)
		}
	}
}

func (ws *WebSocketServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	activeCount := atomic.LoadInt64(&ws.activeConns)

	response := map[string]interface{}{
		"status":             "healthy",
		"active_connections": activeCount,
		"timestamp":          time.Now(),
		"server_type":        "websocket",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	// Set GOMAXPROCS untuk concurrency optimal
	runtime.GOMAXPROCS(runtime.NumCPU())

	server := NewWebSocketServer()

	// Setup HTTP routes
	http.HandleFunc("/ws", server.handleConnection)
	http.HandleFunc("/health", server.handleHealth)

	// Serve static files for simple WebSocket client testing
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		html := `<!DOCTYPE html>
<html>
<head>
    <title>WebSocket Test Client</title>
</head>
<body>
    <div id="messages"></div>
    <input type="text" id="messageInput" placeholder="Enter message">
    <button onclick="sendMessage()">Send</button>
    
    <script>
        const ws = new WebSocket('ws://localhost:9003/ws');
        const messages = document.getElementById('messages');
        
        ws.onmessage = function(event) {
            const msg = JSON.parse(event.data);
            messages.innerHTML += '<div>' + JSON.stringify(msg) + '</div>';
        };
        
        function sendMessage() {
            const input = document.getElementById('messageInput');
            const msg = {
                type: 'hello',
                name: input.value,
                timestamp: new Date()
            };
            ws.send(JSON.stringify(msg));
            input.value = '';
        }
    </script>
</body>
</html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	})

	log.Printf("WebSocket server starting on port :9003")
	log.Printf("Using %d CPU cores", runtime.NumCPU())
	log.Printf("WebSocket endpoint: ws://localhost:9003/ws")
	log.Printf("Health check: http://localhost:9003/health")
	log.Printf("Test client: http://localhost:9003/")

	// Start server
	if err := http.ListenAndServe(":9003", nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
