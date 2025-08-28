#!/bin/bash

# OS Level Tuning Script
# Based on TIMEOUT_ANALYSIS.md recommendations

echo "🔧 Applying OS-level optimizations for load testing..."

# Check current limits
echo "📊 Current file descriptor limits:"
ulimit -n

echo "📊 Current TCP settings:"
sysctl net.core.somaxconn
sysctl net.ipv4.tcp_max_syn_backlog
sysctl net.ipv4.ip_local_port_range

echo ""
echo "🚀 Applying recommended optimizations..."

# Increase file descriptor limits (temporary, for current session)
echo "Setting file descriptor limit to 65536..."
ulimit -n 65536

# Check if running as root for system-wide changes
if [ "$EUID" -eq 0 ]; then
    echo "Running as root - applying system-wide TCP optimizations..."
    
    # Backup current settings
    echo "📋 Backing up current sysctl settings..."
    sysctl net.core.somaxconn net.ipv4.tcp_max_syn_backlog net.ipv4.ip_local_port_range > /tmp/sysctl_backup_$(date +%Y%m%d_%H%M%S).txt
    
    # Apply TCP optimizations
    echo "Setting TCP connection limits..."
    sysctl -w net.core.somaxconn=65536
    sysctl -w net.ipv4.tcp_max_syn_backlog=65536
    sysctl -w net.ipv4.ip_local_port_range="1024 65535"
    
    echo "✅ System-wide optimizations applied successfully!"
    echo "🔄 To make changes permanent, add to /etc/sysctl.conf:"
    echo "net.core.somaxconn = 65536"
    echo "net.ipv4.tcp_max_syn_backlog = 65536"
    echo "net.ipv4.ip_local_port_range = 1024 65535"
else
    echo "⚠️  Not running as root - only session-level optimizations applied"
    echo "🔐 To apply system-wide TCP optimizations, run with sudo:"
    echo "sudo ./optimize_system.sh"
fi

echo ""
echo "📊 New settings:"
ulimit -n
sysctl net.core.somaxconn
sysctl net.ipv4.tcp_max_syn_backlog  
sysctl net.ipv4.ip_local_port_range

echo ""
echo "✅ System optimization complete!"
echo "💡 Run your load tests now for improved performance"
