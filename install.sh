#!/bin/bash

set -e

GO_CHAT_SERVICE="[Unit]
Description=Go Chat Daemon

[Service]
ExecStart=/usr/local/bin/go-chat -d
Restart=always
User=$(whoami)

[Install]
WantedBy=default.target"

echo "This script will set up the Go Chat application and optionally configure it to run as a systemd service."

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "Go is not installed. Please install Go and try again."
    exit 1
fi

# Get user input for name and AI name
read -p "Enter your name: " user_name
read -p "Enter the AI's name: " ai_name
read -p "Enter a bio for yourself: " user_bio
read -p "Enter a personality for the AI: " ai_personality

# Build the Go binary
echo "Compiling the Go Chat application..."
go build -o go-chat go-chat.go

# Copy the binary to /usr/local/bin
echo "Copying the binary to /usr/local/bin..."
sudo cp go-chat /usr/local/bin/

# Save configuration
config_file="$HOME/.go-chat-config"
echo "Saving configuration..."
echo "{\"user_name\": \"$user_name\", \"ai_name\": \"$ai_name\", \"bio\": \"$user_bio\", \"personality\": \"$ai_personality\"}" > "$config_file"

# Prompt the user for systemd setup
echo -e "\nThe Go Chat application can be configured to run as a daemon using systemd."
echo "This means it will start automatically on boot and run in the background, periodically checking in with you."
echo -e "\nWould you like to enable this feature? (y/n)"
read -r enable_systemd

if [[ $enable_systemd == "y" || $enable_systemd == "Y" ]]; then
    # Create systemd service file
    echo "Creating systemd service file..."
    echo "$GO_CHAT_SERVICE" | sudo tee /etc/systemd/system/go-chat.service > /dev/null

    # Enable and start the service
    echo "Enabling and starting the go-chat service..."
    sudo systemctl enable go-chat.service
    sudo systemctl start go-chat.service

    echo -e "\nThe go-chat service has been enabled and started."
else
    echo -e "\nSkipping systemd service setup."
fi

echo -e "\nSetup complete! You can now use the Go Chat application with the command 'go-chat'."

