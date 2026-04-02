#!/bin/sh
set -e

mkdir -p /data/keys /data/db /data/host-keys

# Generate server keypair on first boot (for upstream SSH auth)
if [ ! -f /data/server-key ]; then
    echo "Generating server SSH keypair..."
    ssh-keygen -t ed25519 -f /data/server-key -N "" -C "bastion-server"
fi

# Persist sshd host keys on data volume so deploys don't break tunnels
if [ ! -f /data/host-keys/ssh_host_ed25519_key ]; then
    echo "Generating persistent sshd host keys..."
    ssh-keygen -t ed25519 -f /data/host-keys/ssh_host_ed25519_key -N ""
    ssh-keygen -t rsa -b 4096 -f /data/host-keys/ssh_host_rsa_key -N ""
    ssh-keygen -t ecdsa -b 256 -f /data/host-keys/ssh_host_ecdsa_key -N ""
fi
cp /data/host-keys/ssh_host_* /etc/ssh/
chmod 600 /etc/ssh/ssh_host_*_key
chmod 644 /etc/ssh/ssh_host_*_key.pub

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
