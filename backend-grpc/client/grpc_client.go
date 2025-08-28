package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "backend-grpc/proto" // Sesuaikan dengan path proto Anda
)

type GRPCLoadTester struct {
	client pb.HelloServiceClient
	conn   *grpc.ClientConn
	stats  *LoadTestStats
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

func NewGRPCLoadTester(serverAddr string) (*GRPCLoadTester, error) {
	// Konfigurasi connection dengan optimasi
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithInitialWindowSize(1024 * 1024),     // 1MB window
		grpc.WithInitialConnWindowSize(1024 * 1024), // 1MB connection window
	}

	conn, err := grpc.Dial(serverAddr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}

	client := pb.NewHelloServiceClient(conn)
	stats := &LoadTestStats{
		minLatency: int64(^uint64(0) >> 1), // Max int64 value
	}

	return &GRPCLoadTester{
		client: client,
		conn:   conn,
		stats:  stats,
	}, nil
}

func (g *GRPCLoadTester) SendRequest(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	req := &pb.HelloRequest{Name: name}
	_, err := g.client.SayHello(ctx, req)

	latency := time.Since(start).Microseconds()

	// Update statistics
	atomic.AddInt64(&g.stats.totalRequests, 1)
	atomic.AddInt64(&g.stats.totalLatency, latency)

	g.stats.mutex.Lock()
	if latency > g.stats.maxLatency {
		g.stats.maxLatency = latency
	}
	if latency < g.stats.minLatency {
		g.stats.minLatency = latency
	}
	g.stats.mutex.Unlock()

	if err != nil {
		atomic.AddInt64(&g.stats.failedReqs, 1)
		return err
	}

	atomic.AddInt64(&g.stats.successfulReqs, 1)
	return nil
}

func (g *GRPCLoadTester) RunLoadTest(concurrency int, totalRequests int) {
	var wg sync.WaitGroup
	requestsPerWorker := totalRequests / concurrency

	log.Printf("Starting gRPC load test: %d workers, %d requests total", concurrency, totalRequests)

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < requestsPerWorker; j++ {
				name := fmt.Sprintf("Worker%d-Req%d", workerID, j)
				if err := g.SendRequest(name); err != nil {
					log.Printf("Worker %d request failed: %v", workerID, err)
				}
			}
		}(i)
	}

	wg.Wait()
	totalTime := time.Since(startTime)

	// Print results
	g.PrintResults(totalTime)
}

func (g *GRPCLoadTester) PrintResults(duration time.Duration) {
	total := atomic.LoadInt64(&g.stats.totalRequests)
	successful := atomic.LoadInt64(&g.stats.successfulReqs)
	failed := atomic.LoadInt64(&g.stats.failedReqs)
	totalLatency := atomic.LoadInt64(&g.stats.totalLatency)

	avgLatency := float64(0)
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
	}

	successRate := float64(successful) / float64(total) * 100
	throughput := float64(total) / duration.Seconds()

	fmt.Printf("\\n=== gRPC Load Test Results ===\\n")
	fmt.Printf("Duration: %v\\n", duration)
	fmt.Printf("Total Requests: %d\\n", total)
	fmt.Printf("Successful: %d\\n", successful)
	fmt.Printf("Failed: %d\\n", failed)
	fmt.Printf("Success Rate: %.2f%%\\n", successRate)
	fmt.Printf("Throughput: %.2f RPS\\n", throughput)
	fmt.Printf("Avg Latency: %.2f μs\\n", avgLatency)
	fmt.Printf("Min Latency: %d μs\\n", g.stats.minLatency)
	fmt.Printf("Max Latency: %d μs\\n", g.stats.maxLatency)
	fmt.Printf("===============================\\n")
}

func (g *GRPCLoadTester) Close() {
	g.conn.Close()
}

func main() {
	tester, err := NewGRPCLoadTester("localhost:9002")
	if err != nil {
		log.Fatalf("Failed to create gRPC load tester: %v", err)
	}
	defer tester.Close()

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
		tester.RunLoadTest(scenario.concurrency, scenario.requests)
		time.Sleep(5 * time.Second) // Cooldown between tests
	}
}
