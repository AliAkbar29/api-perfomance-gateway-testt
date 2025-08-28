#!/bin/bash

# System Optimization Script for Load Testing
# Addresses timeout issues and improves system performance

echo "🚀 Starting system optimization for load testing..."

# 1. INCREASE FILE DESCRIPTOR LIMITS
echo "📁 Configuring file descriptor limits..."
echo "* soft nofile 65536" | sudo tee -a /etc/security/limits.conf
echo "* hard nofile 65536" | sudo tee -a /etc/security/limits.conf
echo "root soft nofile 65536" | sudo tee -a /etc/security/limits.conf
echo "root hard nofile 65536" | sudo tee -a /etc/security/limits.conf

# Apply to current session
ulimit -n 65536

# 2. TCP NETWORK OPTIMIZATION
echo "🌐 Optimizing TCP network settings..."
sudo tee -a /etc/sysctl.conf << EOF

# Load testing optimizations
net.core.somaxconn = 65536
net.core.netdev_max_backlog = 5000
net.ipv4.tcp_max_syn_backlog = 65536
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_probes = 3
net.ipv4.tcp_keepalive_intvl = 15
net.ipv4.tcp_rmem = 4096 16384 16777216
net.ipv4.tcp_wmem = 4096 16384 16777216
net.core.rmem_default = 262144
net.core.rmem_max = 16777216
net.core.wmem_default = 262144
net.core.wmem_max = 16777216
net.ipv4.tcp_congestion_control = bbr
EOF

# Apply sysctl settings
sudo sysctl -p

# 3. INCREASE PROCESS LIMITS
echo "⚡ Configuring process limits..."
echo "* soft nproc 65536" | sudo tee -a /etc/security/limits.conf
echo "* hard nproc 65536" | sudo tee -a /etc/security/limits.conf

# 4. OPTIMIZE FOR DOCKER (if using)
echo "🐳 Optimizing Docker settings..."
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json << EOF
{
  "default-ulimits": {
    "nofile": {
      "Name": "nofile",
      "Hard": 65536,
      "Soft": 65536
    },
    "nproc": {
      "Name": "nproc",
      "Hard": 65536,
      "Soft": 65536
    }
  },
  "max-concurrent-downloads": 10,
  "max-concurrent-uploads": 10
}
EOF

# 5. VERIFY OPTIMIZATIONS
echo "✅ Verifying optimizations..."
echo "Current file descriptor limit: $(ulimit -n)"
echo "Current process limit: $(ulimit -u)"
echo "Current TCP settings:"
sysctl net.core.somaxconn
sysctl net.ipv4.ip_local_port_range
sysctl net.ipv4.tcp_fin_timeout

# 6. SET ENVIRONMENT VARIABLES FOR GO
echo "🐹 Setting Go runtime optimizations..."
export GOMAXPROCS=$(nproc)
export GOGC=100
export GOMEMLIMIT=$(free -b | awk '/^Mem:/{printf "%.0f", $2*0.8}')

echo "export GOMAXPROCS=$(nproc)" >> ~/.bashrc
echo "export GOGC=100" >> ~/.bashrc
echo "export GOMEMLIMIT=$(free -b | awk '/^Mem:/{printf "%.0f", $2*0.8}')" >> ~/.bashrc

echo ""
echo "🎉 System optimization completed!"
echo ""
echo "⚠️  IMPORTANT: Please reboot your system or restart services for all changes to take effect"
echo ""
echo "📋 To verify after reboot:"
echo "   ulimit -n    # Should show 65536"
echo "   ulimit -u    # Should show 65536"
echo "   sysctl net.core.somaxconn    # Should show 65536"
echo ""
echo "🔧 Next steps:"
echo "   1. Reboot system: sudo reboot"
echo "   2. Update client HTTP configuration"
echo "   3. Run load tests with new settings"
