# Stage 1: Get sshpiper binary with explicit amd64 platform (Fly.io uses amd64)
FROM --platform=linux/amd64 farmer1992/sshpiperd:full AS sshpiper

# Stage 2: Build Go API server
FROM --platform=linux/amd64 golang:1.23-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=1 go build -o bastiond ./cmd/bastiond

# Stage 3: Runtime
FROM --platform=linux/amd64 alpine:3.19

RUN apk add --no-cache openssh-server openssh-client ca-certificates sqlite

# Copy sshpiper binary and yaml plugin
COPY --from=sshpiper /sshpiperd/sshpiperd /usr/local/bin/sshpiperd
COPY --from=sshpiper /sshpiperd/plugins/yaml /usr/local/bin/yaml
RUN chmod 755 /usr/local/bin/sshpiperd /usr/local/bin/yaml

# Copy bastiond server binary
COPY --from=builder /app/bastiond /usr/local/bin/bastiond

# Create directories
RUN mkdir -p /etc/sshpiper /var/run/sshd /data/keys /data/db

# Create host key for sshpiper
RUN ssh-keygen -t ed25519 -f /etc/sshpiper/ssh_host_ed25519_key -N ""

# Setup bastion user (no shell — tunnel-only)
RUN adduser -D -s /sbin/nologin bastion && passwd -u bastion

# Configure sshd for reverse tunnels (port 2222)
RUN ssh-keygen -A && \
    sed -i 's/#Port 22/Port 2222/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config && \
    sed -i 's/AllowTcpForwarding no/AllowTcpForwarding yes/' /etc/ssh/sshd_config && \
    sed -i 's/GatewayPorts no/GatewayPorts no/' /etc/ssh/sshd_config && \
    echo "AllowUsers bastion" >> /etc/ssh/sshd_config && \
    echo "Match User bastion" >> /etc/ssh/sshd_config && \
    echo "  AllowTcpForwarding remote" >> /etc/ssh/sshd_config && \
    echo "  X11Forwarding no" >> /etc/ssh/sshd_config && \
    echo "  AllowAgentForwarding no" >> /etc/ssh/sshd_config

EXPOSE 2222 2223 8080

COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
