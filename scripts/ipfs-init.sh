#!/bin/sh
set -e

# Initialize IPFS repo if not exists
if [ ! -f "$IPFS_PATH/config" ]; then
    ipfs init --profile=server
    
    # Configure API to listen on all interfaces
    ipfs config Addresses.API /ip4/0.0.0.0/tcp/5001
    ipfs config Addresses.Gateway /ip4/0.0.0.0/tcp/8080
    
    # Configure swarm to listen on all interfaces
    ipfs config Addresses.Swarm '["/ip4/0.0.0.0/tcp/4001", "/ip6/::/tcp/4001"]'
    
    # Enable pubsub for peer discovery
    ipfs config --json Pubsub.Enabled true
    
    # Configure routing
    ipfs config Routing.Type dhtclient
    
    # Add swarm key for private network if provided
    if [ -n "$IPFS_SWARM_KEY" ] && [ -f "$IPFS_SWARM_KEY" ]; then
        cp "$IPFS_SWARM_KEY" "$IPFS_PATH/swarm.key"
    fi
fi

# Start IPFS daemon in background
ipfs daemon --migrate=true &
DAEMON_PID=$!

# Wait for API to be ready
for i in $(seq 1 30); do
    if wget -q --spider http://localhost:5001/api/v0/id 2>/dev/null; then
        break
    fi
    sleep 1
done

# Connect to swarm peers if specified
if [ -n "$IPFS_SWARM_CONNECT" ]; then
    for peer in $(echo "$IPFS_SWARM_CONNECT" | tr ',' '\n'); do
        echo "Connecting to peer: $peer"
        ipfs swarm connect "$peer" || true
    done
fi

# Wait for daemon to finish
wait $DAEMON_PID
