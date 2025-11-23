#!/bin/bash

# Browser Lab Manager - Complete background runner with Tailscale Funnel support
# Usage: ./browser-lab.sh [command]

APP_NAME="browser-lab"
BINARY_NAME="browser-server"
RUNTIME_DIR="runtime"
APP_PID_FILE="$RUNTIME_DIR/$APP_NAME.pid"
FUNNEL_PID_FILE="$RUNTIME_DIR/$APP_NAME-funnel.pid"
LOG_DIR="$RUNTIME_DIR/logs"
LOG_FILE="$LOG_DIR/$APP_NAME.log"
FUNNEL_LOG_FILE="$LOG_DIR/$APP_NAME-funnel.log"
TAILSCALE_HOST="shanur-pc.tailbe68d6.ts.net"

# Function to build the application
build_app() {
    echo "Building $APP_NAME..."
    if go build -o $BINARY_NAME .; then
        echo "Build successful"
        return 0
    else
        echo "Build failed"
        return 1
    fi
}

# Function to ensure runtime directories exist
ensure_runtime_dirs() {
    if [ ! -d "$RUNTIME_DIR" ]; then
        mkdir -p "$RUNTIME_DIR"
        echo "Created runtime directory: $RUNTIME_DIR"
    fi
    if [ ! -d "$LOG_DIR" ]; then
        mkdir -p "$LOG_DIR"
        echo "Created logs directory: $LOG_DIR"
    fi
}

# Function to start the application
start_app() {
    # First check if port 8080 is already in use
    if netstat -tlnp 2>/dev/null | grep -q ":8080 "; then
        echo "Error: Port 8080 is already in use"
        echo "Run './browser-lab.sh stop' to stop existing instances"
        return 1
    fi
    
    if [ -f "$APP_PID_FILE" ]; then
        PID=$(cat "$APP_PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "$APP_NAME is already running (PID: $PID)"
            return 1
        else
            echo "Removing stale PID file"
            rm -f "$APP_PID_FILE"
        fi
    fi

    echo "Starting $APP_NAME..."
    
    # Ensure logs directory exists
    ensure_runtime_dirs
    
    # Build first
    if ! build_app; then
        return 1
    fi

    # Set APP_HOST with Tailscale URL
    export APP_HOST="${TAILSCALE_HOST}:8443"
    echo "Using Tailscale Funnel URL: https://${TAILSCALE_HOST}:8443"

    # Start the application in background
    nohup ./$BINARY_NAME > "$LOG_FILE" 2>&1 &
    PID=$!
    
    # Save PID
    echo $PID > "$APP_PID_FILE"
    
    # Verify it's running and port is bound
    sleep 2
    if ps -p $PID > /dev/null 2>&1 && netstat -tlnp 2>/dev/null | grep -q ":8080.*$PID"; then
        echo "$APP_NAME started successfully (PID: $PID)"
        echo "Logs: $LOG_FILE"
        echo "Local Dashboard: http://localhost:8080"
        echo "Tailscale Dashboard: https://${TAILSCALE_HOST}:8443"
        return 0
    else
        echo "Failed to start $APP_NAME or bind to port 8080"
        rm -f "$APP_PID_FILE"
        return 1
    fi
}

# Function to start Tailscale Funnel
start_funnel() {
    if [ -f "$FUNNEL_PID_FILE" ]; then
        PID=$(cat "$FUNNEL_PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "Tailscale Funnel is already running (PID: $PID)"
            return 1
        else
            echo "Removing stale funnel PID file"
            rm -f "$FUNNEL_PID_FILE"
        fi
    fi

    echo "Starting Tailscale Funnel..."
    
    # Ensure logs directory exists
    ensure_runtime_dirs
    
    # Check if there are any existing funnel processes
    EXISTING_FUNNEL=$(pgrep -f "tailscale funnel --https=8443")
    if [ -n "$EXISTING_FUNNEL" ]; then
        echo "Note: Found existing Tailscale Funnel process (will clean up after authentication)"
    fi
    
    echo ""
    echo "========================================="
    echo "Tailscale Funnel requires sudo access"
    echo "Please enter your password when prompted"
    echo "========================================="
    
    # Prompt for sudo password with a simple command that requires TTY
    # This will cache the credentials for the next sudo command
    if ! sudo echo "✓ Authentication successful"; then
        echo "✗ Failed to authenticate with sudo"
        return 1
    fi
    
    # Clean up existing funnel processes if found (now we have sudo credentials)
    if [ -n "$EXISTING_FUNNEL" ]; then
        echo ""
        echo "Found existing Tailscale Funnel process (PID: $EXISTING_FUNNEL)"
        echo "Stopping it first..."
        sudo kill $EXISTING_FUNNEL 2>/dev/null
        sleep 2
        # Force kill if still running
        if ps -p $EXISTING_FUNNEL > /dev/null 2>&1; then
            sudo kill -9 $EXISTING_FUNNEL 2>/dev/null
            sleep 1
        fi
        echo "✓ Cleaned up existing funnel process"
    fi
    
    echo ""
    echo "Starting funnel process..."
    
    # Now start the funnel in background using the cached sudo credentials
    nohup sudo tailscale funnel --https=8443 localhost:8080 > "$FUNNEL_LOG_FILE" 2>&1 &
    FUNNEL_PID=$!
    
    # Save PID
    echo $FUNNEL_PID > "$FUNNEL_PID_FILE"
    
    # Wait a moment for funnel to initialize
    sleep 3
    
    # Verify it's running
    if ps -p $FUNNEL_PID > /dev/null 2>&1; then
        echo "✓ Tailscale Funnel started successfully (PID: $FUNNEL_PID)"
        echo "  Funnel logs: $FUNNEL_LOG_FILE"
        echo "  Public URL: https://${TAILSCALE_HOST}:8443"
        return 0
    else
        echo "✗ Failed to start Tailscale Funnel"
        echo "  Check the funnel logs at: $FUNNEL_LOG_FILE"
        rm -f "$FUNNEL_PID_FILE"
        return 1
    fi
}

# Function to stop the application
stop_app() {
    # First try to stop using PID file
    if [ -f "$APP_PID_FILE" ]; then
        PID=$(cat "$APP_PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "Stopping $APP_NAME (PID: $PID)..."
            kill $PID
            
            # Wait for graceful shutdown
            for i in {1..10}; do
                if ! ps -p $PID > /dev/null 2>&1; then
                    echo "$APP_NAME stopped successfully"
                    rm -f "$APP_PID_FILE"
                    return 0
                fi
                sleep 1
            done
            
            # Force kill if still running
            echo "Force killing $APP_NAME..."
            kill -9 $PID
            rm -f "$APP_PID_FILE"
            echo "$APP_NAME stopped forcefully"
        else
            echo "$APP_NAME is not running (stale PID file)"
            rm -f "$APP_PID_FILE"
        fi
    fi
    
    # Also check for any remaining browser-server processes
    PIDS=$(pgrep -f "$BINARY_NAME" 2>/dev/null)
    if [ -n "$PIDS" ]; then
        echo "Found additional browser-server processes: $PIDS"
        echo "Stopping them..."
        for pid in $PIDS; do
            echo "Killing process $pid..."
            kill $pid 2>/dev/null
        done
        
        # Wait and force kill if needed
        sleep 2
        REMAINING_PIDS=$(pgrep -f "$BINARY_NAME" 2>/dev/null)
        if [ -n "$REMAINING_PIDS" ]; then
            echo "Force killing remaining processes: $REMAINING_PIDS"
            for pid in $REMAINING_PIDS; do
                kill -9 $pid 2>/dev/null
            done
        fi
    fi
    
    # Check if port 8080 is still in use
    sleep 1
    if netstat -tlnp 2>/dev/null | grep -q ":8080 "; then
        echo "Warning: Port 8080 is still in use"
        echo "You may need to manually kill the process using: sudo fuser -k 8080/tcp"
    else
        echo "Port 8080 is now free"
    fi
}

# Function to stop Tailscale Funnel
stop_funnel() {
    if [ ! -f "$FUNNEL_PID_FILE" ]; then
        echo "Tailscale Funnel is not running (no PID file found)"
        return 1
    fi

    PID=$(cat "$FUNNEL_PID_FILE")
    
    if ps -p $PID > /dev/null 2>&1; then
        echo "Stopping Tailscale Funnel (PID: $PID)..."
        sudo kill $PID
        
        # Wait for graceful shutdown
        for i in {1..5}; do
            if ! ps -p $PID > /dev/null 2>&1; then
                echo "Tailscale Funnel stopped successfully"
                rm -f "$FUNNEL_PID_FILE"
                return 0
            fi
            sleep 1
        done
        
        # Force kill if still running
        echo "Force killing Tailscale Funnel..."
        sudo kill -9 $PID
        rm -f "$FUNNEL_PID_FILE"
        echo "Tailscale Funnel stopped forcefully"
    else
        echo "Tailscale Funnel is not running (stale PID file)"
        rm -f "$FUNNEL_PID_FILE"
    fi
}

# Function to start everything
start_all() {
    start_app
    if [ $? -eq 0 ]; then
        sleep 2
        start_funnel
    fi
}

# Function to stop everything
stop_all() {
    stop_funnel
    stop_app
}

# Function to restart everything
restart_all() {
    stop_all
    sleep 2
    start_all
}

# Function to check status
show_status() {
    echo "=== Browser Lab Status ==="
    
    # Check app status
    if [ -f "$APP_PID_FILE" ]; then
        PID=$(cat "$APP_PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "✓ Browser Lab is running (PID: $PID)"
            echo "  Local Dashboard: http://localhost:8080"
            echo "  Tailscale Dashboard: https://${TAILSCALE_HOST}:8443"
        else
            echo "✗ Browser Lab is not running (stale PID file)"
        fi
    else
        echo "✗ Browser Lab is not running"
    fi
    
    # Check funnel status
    if [ -f "$FUNNEL_PID_FILE" ]; then
        PID=$(cat "$FUNNEL_PID_FILE")
        if ps -p $PID > /dev/null 2>&1; then
            echo "✓ Tailscale Funnel is running (PID: $PID)"
            echo "  Public URL: https://${TAILSCALE_HOST}:8443"
        else
            echo "✗ Tailscale Funnel is not running (stale PID file)"
        fi
    else
        echo "✗ Tailscale Funnel is not running"
    fi
    
    echo ""
    echo "Log files:"
    echo "  App logs: $LOG_FILE"
    echo "  Funnel logs: $FUNNEL_LOG_FILE"
}

# Function to show URLs
show_urls() {
    echo "Local Dashboard: http://localhost:8080"
    echo "Public URL: https://${TAILSCALE_HOST}:8443"
}

# Function to show logs
show_logs() {
    case "$1" in
        app)
            if [ -f "$LOG_FILE" ]; then
                echo "=== App Logs ==="
                tail -f "$LOG_FILE"
            else
                echo "No app log file found"
            fi
            ;;
        funnel)
            if [ -f "$FUNNEL_LOG_FILE" ]; then
                echo "=== Funnel Logs ==="
                tail -f "$FUNNEL_LOG_FILE"
            else
                echo "No funnel log file found"
            fi
            ;;
        *)
            echo "Usage: $0 logs {app|funnel}"
            ;;
    esac
}

# Main script logic
case "$1" in
    start)
        start_app
        ;;
    start-funnel)
        start_all
        ;;
    stop)
        stop_app
        ;;
    stop-funnel)
        stop_funnel
        ;;
    stop-all)
        stop_all
        ;;
    restart)
        restart_all
        ;;
    status)
        show_status
        ;;
    url)
        show_urls
        ;;
    logs)
        show_logs "$2"
        ;;
    build)
        build_app
        ;;
    cleanup)
        echo "Cleaning up all browser-server processes and PID files..."
        stop_all
        rm -f "$RUNTIME_DIR"/*.pid
        echo "Cleanup complete"
        ;;
    *)
        echo "Browser Lab Manager - Complete control script"
        echo ""
        echo "Usage: $0 {command}"
        echo ""
        echo "Commands:"
        echo "  start         - Start the browser lab locally only"
        echo "  start-funnel  - Start both app and Tailscale Funnel (recommended)"
        echo "  stop          - Stop the browser lab only"
        echo "  stop-funnel   - Stop Tailscale Funnel only"
        echo "  stop-all      - Stop both app and Tailscale Funnel"
        echo "  restart       - Restart both app and Tailscale Funnel"
        echo "  status        - Show status of all services"
        echo "  url           - Show current access URLs"
        echo "  logs {app|funnel} - Follow logs for app or funnel"
        echo "  build         - Build the application only"
        echo "  cleanup       - Kill all processes and remove PID files"
        echo ""
        echo "Examples:"
        echo "  $0 start-funnel    # Start everything with public access"
        echo "  $0 status          # Check if everything is running"
        echo "  $0 logs app        # Follow application logs"
        echo "  $0 logs funnel     # Follow Tailscale Funnel logs"
        exit 1
        ;;
esac