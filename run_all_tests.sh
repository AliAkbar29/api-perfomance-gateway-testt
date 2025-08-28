#!/bin/bash

echo "=== Starting Comprehensive Load Testing ==="
echo "Testing REST vs gRPC vs WebSocket"

# Start all backends
echo "Starting backends..."
cd backend && go run main.go &
BACKEND_REST_PID=$!
if ! kill -0 $BACKEND_REST_PID; then
    echo "Failed to start REST backend"
    exit 1
fi

cd ../backend-grpc/server && go run main.go &
BACKEND_GRPC_PID=$!
if ! kill -0 $BACKEND_GRPC_PID; then
    echo "Failed to start gRPC backend"
    exit 1
fi

cd ../../backend-websocket/server && go run main.go &
BACKEND_WS_PID=$!
if ! kill -0 $BACKEND_WS_PID; then
    echo "Failed to start WebSocket backend"
    exit 1
fi

# Wait for backends to start
sleep 20  # Perpanjang waktu tunggu agar backend siap

# Run tests
echo "Running REST load test..."
cd ../../client
go run . --scenario high --target backend

echo "Running gRPC load test..."
cd ../backend-grpc/client
go run grpc_client.go

echo "Running WebSocket load test..."
cd ../backend-websocket/client
go run websocket_client.go

# Cleanup
kill $BACKEND_REST_PID $BACKEND_GRPC_PID $BACKEND_WS_PID

echo "=== All tests completed ==="
echo "Check Grafana dashboard for results: http://localhost:3000"
