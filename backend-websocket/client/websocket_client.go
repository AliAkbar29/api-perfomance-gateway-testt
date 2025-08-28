package main

import (
	"fmt"
	"log"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type WebSocketLoadTester struct {
	serverURL string
	stats     *LoadTestStats
}

type LoadTestStats struct {
	totalRequests  int64
	successfulReqs int64
	failedReqs     int64
	totalLatency   int64 // in microseconds
	maxLatency     int64
	minLatency     int64
	mutex          sync.RWMutex
}

type TestMessage struct {
	Type      string    `json:"type"`
	Name      string    `json:"name,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type ResponseMessage struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func NewWebSocketLoadTester(serverAddr string) *WebSocketLoadTester {
	stats := &LoadTestStats{
		minLatency: int64(^uint64(0) >> 1), // Max int64 value
	}

	return &WebSocketLoadTester{
		serverURL: serverAddr,
		stats:     stats,
	}
}

func (wlt *WebSocketLoadTester) sendRequest(conn *websocket.Conn, name string) error {
	start := time.Now()

	// Send message
	msg := TestMessage{
		Type:      "hello",
		Name:      name,
		Timestamp: time.Now(),
	}

	err := conn.WriteJSON(msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	// Read response
	var response ResponseMessage
	err = conn.ReadJSON(&response)
	if err != nil {
		return fmt.Errorf("failed to read response: %v", err)
	}

	latency := time.Since(start).Microseconds()

	// Update statistics
	atomic.AddInt64(&wlt.stats.totalRequests, 1)
	atomic.AddInt64(&wlt.stats.totalLatency, latency)

	wlt.stats.mutex.Lock()
	if latency > wlt.stats.maxLatency {
		wlt.stats.maxLatency = latency
	}
	if latency < wlt.stats.minLatency {
		wlt.stats.minLatency = latency
	}
	wlt.stats.mutex.Unlock()

	if response.Type == "hello_response" {
		atomic.AddInt64(&wlt.stats.successfulReqs, 1)
		return nil
	}

	atomic.AddInt64(&wlt.stats.failedReqs, 1)
	return fmt.Errorf("unexpected response type: %s", response.Type)
}

func (wlt *WebSocketLoadTester) runWorker(workerID int, requestsPerWorker int, wg *sync.WaitGroup) {
	defer wg.Done()

	// Parse WebSocket URL
	u, err := url.Parse(wlt.serverURL)
	if err != nil {
		log.Printf("Worker %d: Invalid URL: %v", workerID, err)
		return
	}

	// Connect to WebSocket server
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Printf("Worker %d: Failed to connect: %v", workerID, err)
		return
	}
	defer conn.Close()

	// Send requests
	for i := 0; i < requestsPerWorker; i++ {
		name := fmt.Sprintf("Worker%d-Req%d", workerID, i)

		if err := wlt.sendRequest(conn, name); err != nil {
			log.Printf("Worker %d request %d failed: %v", workerID, i, err)
			atomic.AddInt64(&wlt.stats.failedReqs, 1)
		}

		// Small delay to prevent overwhelming the server
		time.Sleep(1 * time.Millisecond)
	}
}

func (wlt *WebSocketLoadTester) RunLoadTest(concurrency int, totalRequests int) {
	var wg sync.WaitGroup
	requestsPerWorker := totalRequests / concurrency

	log.Printf("Starting WebSocket load test: %d workers, %d requests total", concurrency, totalRequests)

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go wlt.runWorker(i, requestsPerWorker, &wg)
	}

	wg.Wait()
	totalTime := time.Since(startTime)

	// Print results
	wlt.PrintResults(totalTime)
}

func (wlt *WebSocketLoadTester) PrintResults(duration time.Duration) {
	total := atomic.LoadInt64(&wlt.stats.totalRequests)
	successful := atomic.LoadInt64(&wlt.stats.successfulReqs)
	failed := atomic.LoadInt64(&wlt.stats.failedReqs)
	totalLatency := atomic.LoadInt64(&wlt.stats.totalLatency)

	avgLatency := float64(0)
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
	}

	successRate := float64(successful) / float64(total) * 100
	throughput := float64(total) / duration.Seconds()

	fmt.Printf("\\n=== WebSocket Load Test Results ===\\n")
	fmt.Printf("Duration: %v\\n", duration)
	fmt.Printf("Total Requests: %d\\n", total)
	fmt.Printf("Successful: %d\\n", successful)
	fmt.Printf("Failed: %d\\n", failed)
	fmt.Printf("Success Rate: %.2f%%\\n", successRate)
	fmt.Printf("Throughput: %.2f RPS\\n", throughput)
	fmt.Printf("Avg Latency: %.2f μs\\n", avgLatency)
	fmt.Printf("Min Latency: %d μs\\n", wlt.stats.minLatency)
	fmt.Printf("Max Latency: %d μs\\n", wlt.stats.maxLatency)
	fmt.Printf("====================================\\n")
}

func main() {
	tester := NewWebSocketLoadTester("ws://localhost:9003/ws")

	// Test dengan berbagai skenario
	scenarios := []struct {
		name        string
		concurrency int
		requests    int
	}{
		{"Light Load", 10, 100},
		{"Medium Load", 50, 500},
		{"High Load", 100, 1000},
		{"Peak Load", 200, 2000},
	}

	for _, scenario := range scenarios {
		fmt.Printf("\\n--- Running %s ---\\n", scenario.name)

		// Reset stats for each scenario
		tester.stats = &LoadTestStats{
			minLatency: int64(^uint64(0) >> 1),
		}

		tester.RunLoadTest(scenario.concurrency, scenario.requests)
		time.Sleep(5 * time.Second) // Cooldown between tests
	}
}
