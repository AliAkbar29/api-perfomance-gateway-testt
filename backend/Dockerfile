# Stage 1: build binary
FROM golang:1.21 AS builder
WORKDIR /app

# copy semua file
COPY . .

# download dependency
RUN go mod tidy

# build binary (static lebih aman)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o backend .

# Stage 2: minimal runtime image
FROM debian:bullseye-slim
WORKDIR /app

# copy binary hasil build
COPY --from=builder /app/backend .

EXPOSE 9000
CMD ["./backend"]
