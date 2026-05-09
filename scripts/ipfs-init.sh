#!/bin/sh
set -e

# IPFS init script for docker containers
# Usage: ipfs-init.sh <peer_multiaddr>

REPO=${IPFS_PATH:-/data/ipfs}
BOOTSTRAP_PEER=${1:-}

if [ ! -f "$REPO/config" ]; then
    echo "Initializing IPFS repo at $REPO"
    ipfs init --profile=lowpower
    
    # Configure API to listen on all interfaces
    ipfs config Addresses.API /ip4/0.0.0.0/tcp/5001
    ipfs config Addresses.Gateway /ip4/0.0.0.0/tcp/8080
    
    # Disable unwanted services for storage-only node
    ipfs config --json Swarm.DisableNatPortMap true
    ipfs config --json Swarm.RelayClient.Enabled false
    ipfs config --json Swarm.RelayService.Enabled false
    ipfs config --json AutoNAT.ServiceMode false
    ipfs config --json Discovery.MDNS.Enabled false
    
    # Bootstrap to peer if provided
    if [ -n "$BOOTSTRAP_PEER" ]; then
        echo "Adding bootstrap peer: $BOOTSTRAP_PEER"
        ipfs bootstrap rm --all
        ipfs bootstrap add "$BOOTSTRAP_PEER"
    fi
    
    # Copy swarm key if present
    if [ -f /key/swarm.key ]; then
        echo "Installing swarm key"
        mkdir -p "$REPO"
        cp /key/swarm.key "$REPO/swarm.key"
    fi
fi

# Start daemon
exec ipfs daemon --migrate=true
