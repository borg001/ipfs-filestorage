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
    
    # Add bootstrap peers if provided
    if [ -n "$IPFS_BOOTSTRAP" ]; then
        ipfs bootstrap rm --all
        for peer in $(echo "$IPFS_BOOTSTRAP" | tr ',' '\n'); do
            ipfs bootstrap add "$peer"
        done
    fi
    
    # Add swarm key if provided
    if [ -n "$IPFS_SWARM_KEY" ]; then
        echo "$IPFS_SWARM_KEY" > "$IPFS_PATH/swarm.key"
    fi
    
    # Enable pubsub for peer discovery
    ipfs config --json Pubsub.Enabled true
    
    # Configure routing for better peer discovery
    ipfs config Routing.Type dhtclient
fi

# Start IPFS daemon
exec ipfs daemon --migrate=true "$@"
