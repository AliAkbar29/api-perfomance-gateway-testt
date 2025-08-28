package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	pb "backend-grpc/proto"

	"github.com/DataDog/datadog-go/statsd"
)

type HelloServer struct {
	pb.UnimplementedHelloServiceServer
	mutex             sync.RWMutex
	activeConnections int64
	statsdClient      *statsd.Client // Menggunakan pointer ke statsd.Client
}

func NewHelloServer() *HelloServer {
	// Konfigurasi StatsD client
	statsdClient, err := statsd.New("localhost:8125")
	if err != nil {
		log.Printf("Warning: Failed to create StatsD client: %v", err)
		statsdClient = nil
	}

	return &HelloServer{
		statsdClient: statsdClient,
	}
}

func (s *HelloServer) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
	// Increment active connections untuk monitoring
	s.mutex.Lock()
	s.activeConnections++
	current := s.activeConnections
	s.mutex.Unlock()

	// Simulasi processing time yang ringan
	time.Sleep(1 * time.Millisecond)

	response := &pb.HelloResponse{
		Message:   fmt.Sprintf("Hello, %s! (gRPC)", req.Name),
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Decrement connections
	s.mutex.Lock()
	s.activeConnections--
	s.mutex.Unlock()

	// Send metrics to StatsD
	if s.statsdClient != nil {
		latency := time.Since(time.Now()) // Calculate latency
		s.statsdClient.Timing("grpc.request.latency", latency, []string{"method:SayHello"}, 1)
		s.statsdClient.Incr("grpc.request.count", []string{"method:SayHello", "status:success"}, 1)
		s.statsdClient.Gauge("grpc.connections.active", float64(current), []string{}, 1)
	}

	return response, nil
}

func (s *HelloServer) SayHelloStream(req *pb.HelloRequest, stream pb.HelloService_SayHelloStreamServer) error {
	// Stream implementation untuk testing advanced scenarios
	for i := 0; i < 5; i++ {
		response := &pb.HelloResponse{
			Message:   fmt.Sprintf("Hello %s! Stream message %d (gRPC)", req.Name, i+1),
			Timestamp: time.Now().Format(time.RFC3339),
		}

		if err := stream.Send(response); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func main() {
	// Set GOMAXPROCS untuk concurrency optimal
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Listen pada port 9002 (berbeda dari REST yang 9001)
	lis, err := net.Listen("tcp", ":9002")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Konfigurasi gRPC server dengan optimasi concurrency
	opts := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(1000),                 // Max concurrent streams
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),            // 4MB max message
		grpc.MaxSendMsgSize(4 * 1024 * 1024),            // 4MB max message
		grpc.NumStreamWorkers(uint32(runtime.NumCPU())), // Worker threads
	}

	server := grpc.NewServer(opts...)

	// Register service
	helloServer := NewHelloServer() // Gunakan fungsi NewHelloServer
	pb.RegisterHelloServiceServer(server, helloServer)

	// Enable reflection untuk debugging
	reflection.Register(server)

	log.Printf("gRPC server starting on port :9002")
	log.Printf("Using %d CPU cores", runtime.NumCPU())

	// Start server
	if err := server.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
