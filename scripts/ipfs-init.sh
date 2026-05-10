#!/bin/sh
set -e

# IPFS storage node init — connects to bootstrap for peer discovery
echo "=== IPFS Storage Node Init ==="

# Initialize IPFS repo if not exists
if [ ! -f "$IPFS_PATH/config" ]; then
    ipfs init --profile=$IPFS_PROFILE

    # Configure API to listen on all interfaces
    ipfs config Addresses.API /ip4/0.0.0.0/tcp/5001
    ipfs config Addresses.Gateway /ip4/0.0.0.0/tcp/8080

    # Configure swarm to listen on all interfaces
    ipfs config Addresses.Swarm '["/ip4/0.0.0.0/tcp/4001", "/ip6/::/tcp/4001"]'

    # Full DHT — storage nodes MUST provide content, not just query
    # dhtclient only queries — files added on this node won't be announced!
    ipfs config Routing.Type dht

    # Remove all default public bootstrap peers (private network)
    ipfs bootstrap rm --all

    # Enable pubsub for peer discovery
    ipfs config --json Pubsub.Enabled true

    # Add swarm key for private network if provided
    if [ -n "$IPFS_SWARM_KEY" ] && [ -f "$IPFS_SWARM_KEY" ]; then
        cp "$IPFS_SWARM_KEY" "$IPFS_PATH/swarm.key"
    fi
fi

# Start IPFS daemon in background
ipfs daemon --migrate=true &
DAEMON_PID=$!

# Wait for API to be ready
for i in $(seq 1 60); do
    if wget -q --spider http://localhost:5001/api/v0/id 2>/dev/null; then
        break
    fi
    sleep 1
done

# Connect to bootstrap node for peer discovery
# IPFS_BOOTSTRAP_HOST is the hostname of the bootstrap container (e.g. ipfs-bootstrap)
if [ -n "$IPFS_BOOTSTRAP_HOST" ]; then
    echo "Discovering bootstrap node at $IPFS_BOOTSTRAP_HOST..."

    # Wait for bootstrap API to be reachable
    for i in $(seq 1 30); do
        if wget -q --spider "http://$IPFS_BOOTSTRAP_HOST:5001/api/v0/id" 2>/dev/null; then
            break
        fi
        echo "Waiting for bootstrap API... ($i/30)"
        sleep 2
    done

    # Get bootstrap node's Peer ID from its API
    BOOTSTRAP_ID=$(wget -qO- "http://$IPFS_BOOTSTRAP_HOST:5001/api/v0/id" 2>/dev/null \
        | grep -o '"ID":"[^"]*"' | head -1 | cut -d'"' -f4)

    if [ -n "$BOOTSTRAP_ID" ]; then
        BOOTSTRAP_ADDR="/dns4/$IPFS_BOOTSTRAP_HOST/tcp/4001/p2p/$BOOTSTRAP_ID"
        echo "Bootstrap Peer ID: $BOOTSTRAP_ID"
        echo "Bootstrap Address: $BOOTSTRAP_ADDR"

        # Add as bootstrap peer (persistent across restarts)
        ipfs bootstrap add "$BOOTSTRAP_ADDR" 2>/dev/null || true

        # Connect immediately
        ipfs swarm connect "$BOOTSTRAP_ADDR" || true
        echo "Connected to bootstrap node"
    else
        echo "WARNING: Could not get bootstrap Peer ID"
    fi
fi

# Also connect to specific swarm peers if provided (legacy support)
if [ -n "$IPFS_SWARM_CONNECT" ]; then
    for peer in $(echo "$IPFS_SWARM_CONNECT" | tr ',' '\n'); do
        echo "Connecting to peer: $peer"
        ipfs swarm connect "$peer" || true
    done
fi

# Wait a bit for DHT routing to stabilize
sleep 3

echo "=== Storage Node Ready ==="
wait $DAEMON_PID
