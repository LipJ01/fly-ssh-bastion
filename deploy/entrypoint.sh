#!/bin/sh
set -e

mkdir -p /data/keys /data/db

# Generate server keypair on first boot (for upstream SSH auth)
if [ ! -f /data/server-key ]; then
    echo "Generating server SSH keypair..."
    ssh-keygen -t ed25519 -f /data/server-key -N "" -C "bastion-server"
fi

# Setup bastion user authorized_keys from server public key
# (machines authenticate with their own keys via sshpiper,
#  but the bastion user needs to accept tunnel connections)
mkdir -p /home/bastion/.ssh
cp /data/server-key.pub /home/bastion/.ssh/authorized_keys

# Also add any registered machine keys to bastion authorized_keys
# so machines can establish reverse tunnels
if [ -d /data/keys ] && ls /data/keys/*.pub 1>/dev/null 2>&1; then
    cat /data/keys/*.pub >> /home/bastion/.ssh/authorized_keys
fi

chmod 700 /home/bastion/.ssh
chmod 600 /home/bastion/.ssh/authorized_keys
chown -R bastion:bastion /home/bastion/.ssh

echo "Starting bastiond..."
exec /usr/local/bin/bastiond \
    --db /data/db/bastion.db \
    --keys-dir /data/keys \
    --config-path /data/sshpiper.yaml \
    --server-key /data/server-key \
    --listen :8080
