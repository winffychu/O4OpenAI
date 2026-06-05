#!/bin/bash
# Install o4openai as a systemd service on Linux
# Usage: sudo ./scripts/install-linux.sh [path-to-binary]
#
# Prerequisites:
#   - Run as root (sudo)
#   - Binary already placed at /usr/local/bin/o4openai (or pass path as arg)
#   - config.yaml at /etc/o4openai/config.yaml

set -euo pipefail

BIN_PATH="${1:-/usr/local/bin/o4openai}"
CONFIG_PATH="/etc/o4openai/config.yaml"
SERVICE_USER="o4openai"

# Create service user
if ! id "$SERVICE_USER" &>/dev/null; then
    useradd --system --shell /usr/sbin/nologin --home /var/lib/o4openai "$SERVICE_USER"
    echo "Created user: $SERVICE_USER"
fi

# Create config dir
mkdir -p /etc/o4openai
mkdir -p /var/log/o4openai
mkdir -p /var/lib/o4openai/temp-images

# Install config if not present
if [ ! -f "$CONFIG_PATH" ]; then
    echo "Config not found at $CONFIG_PATH — please create it before starting the service"
fi

# Install systemd unit
cat > /etc/systemd/system/o4openai.service <<EOF
[Unit]
Description=O4OpenAI API Gateway
Documentation=https://github.com/o4openai/o4openai
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=/var/lib/o4openai
ExecStart=$BIN_PATH -config $CONFIG_PATH
Restart=always
RestartSec=5
LimitNOFILE=65535
Environment=GIN_MODE=release

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/o4openai /var/log/o4openai

[Install]
WantedBy=multi-user.target
EOF

# Permissions
chown -R "$SERVICE_USER:$SERVICE_USER" /var/lib/o4openai /var/log/o4openai
chmod 0640 "$CONFIG_PATH" 2>/dev/null || true

# Enable & start
systemctl daemon-reload
systemctl enable o4openai.service
systemctl restart o4openai.service

echo ""
echo "Service installed and started."
echo ""
echo "Useful commands:"
echo "  systemctl status o4openai       # status"
echo "  journalctl -u o4openai -f       # logs"
echo "  systemctl restart o4openai      # restart"
echo "  systemctl stop o4openai         # stop"
