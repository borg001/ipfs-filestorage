#!/bin/sh
set -e

# Bootstrap node init — lightweight peer discovery node
# No user data, only DHT routing and peer discovery

echo "=== IPFS Bootstrap Node Init ==="

# Initialize IPFS repo if not exists
if [ ! -f "$IPFS_PATH/config" ]; then
    ipfs init --profile=server

    # Configure API to listen on all interfaces
    ipfs config Addresses.API /ip4/0.0.0.0/tcp/5001
    ipfs config Addresses.Gateway /ip4/0.0.0.0/tcp/8080

    # Configure swarm addresses — MUST use --json for array values in modern kubo
    ipfs config --json Addresses.Swarm '["/ip4/0.0.0.0/tcp/4001", "/ip6/::/tcp/4001"]'

    # Full DHT — bootstrap node must be a DHT server to provide routing
    ipfs config Routing.Type dht

    # Disable AutoConf — incompatible with private networks (Kubo 0.41+)
    ipfs config --json AutoConf.Enabled false

    # Remove all default public bootstrap peers (private network)
    ipfs bootstrap rm --all

    # Enable pubsub for peer discovery
    ipfs config --json Pubsub.Enabled true

    # Add swarm key for private network if provided
    if [ -n "$IPFS_SWARM_KEY" ] && [ -f "$IPFS_SWARM_KEY" ]; then
        cp "$IPFS_SWARM_KEY" "$IPFS_PATH/swarm.key"
    fi
fi

# Start IPFS daemon
echo "Starting bootstrap daemon..."
ipfs daemon --migrate=true &
DAEMON_PID=$!

# Wait for API to be ready
for i in $(seq 1 60); do
    if wget -q --spider http://localhost:5001/api/v0/id 2>/dev/null; then
        break
    fi
    sleep 1
done

# Print Peer ID for reference
PEER_ID=$(wget -qO- http://localhost:5001/api/v0/id 2>/dev/null | grep -o '"ID":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "=== Bootstrap Node Ready ==="
echo "Peer ID: $PEER_ID"
echo "Address: /dns4/ipfs-bootstrap/tcp/4001/p2p/$PEER_ID"

wait $DAEMON_PID