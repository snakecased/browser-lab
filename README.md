# Browser Server

This project provides a browser server capable of creating and managing headless browser sessions, streaming their content via WebRTC, and performing automated actions.

## Features

*   **Headless Browser Sessions**: Create isolated Chrome browser instances in headless mode.
*   **WebRTC Streaming**: Stream the browser's content in real-time to a client via WebRTC.
*   **Automated Actions**: Perform actions within the browser session, such as scrolling, to demonstrate liveness and interactivity.
*   **Configurable Host**: Easily configure the host URL for CDP and Preview URLs, useful for deployment in environments like AWS EC2.
*   **Admin Dashboard**: A simple web-based dashboard to manage sessions and view streams.

## Getting Started

### Prerequisites

*   Go (version 1.21 or higher)
*   Google Chrome (or Chromium) installed on the system where the server will run.

### Build and Run

1.  **Clone the repository (if you haven't already):**
    ```bash
    git clone <repository_url>
    cd pyro-newton
    ```

2.  **Build the application:**
    ```bash
    go build -o browser-server .
    ```

3.  **Run the server:**
    ```bash
    ./browser-server
    ```

    The server will start on `http://localhost:8080` by default.

### Running with a Custom Host (e.g., on EC2)

If you're running the server on a machine with a public IP or a custom domain (e.g., an EC2 instance), you can specify the `APP_HOST` environment variable so that the generated CDP and Preview URLs use this host instead of `localhost`.

For example, if your EC2 instance has a public DNS of `ec2-xx-xx-xx-xx.compute-1.amazonaws.com` and your server is listening on port `8080`, you would run:

```bash
APP_HOST=ec2-xx-xx-xx-xx.compute-1.amazonaws.com:8080 ./browser-server
```

Or, if accessing directly via the public IP:

```bash
APP_HOST=XX.XX.XX.XX:8080 ./browser-server
```

If `APP_HOST` is not set, the server will attempt to use the `Host` header from the incoming request.

### Accessing the Dashboard

Once the server is running, open your web browser and navigate to:

```
http://localhost:8080
```

(Or `http://your-ec2-url.com:8080` if you configured `APP_HOST`)

From the dashboard, you can:
*   Create new browser sessions.
*   View real-time WebRTC streams of the browser content (streaming starts automatically).
*   Stop existing sessions.
*   Access the Chrome DevTools Protocol (CDP) URL for advanced debugging.

## Project Structure

*   `main.go`: Main server logic, API endpoints, and session management.
*   `webrtc.go`: Handles WebRTC signaling and streaming of browser content.
*   `session/manager.go`: Manages the lifecycle of browser sessions.
*   `session/session.go`: Defines a single browser session, including launching Chrome.
*   `proxy/proxy.go`: Handles CDP proxying.
*   `dashboard/index.html`: The web-based admin interface.

## Development Notes

*   **Headless Mode**: Chrome is launched in `--headless=new` mode by default.
*   **Automated Scrolling**: Each browser session will automatically scroll to demonstrate active streaming.
*   **CDP Communication**: The server communicates with Chrome via the Chrome DevTools Protocol to initiate screencasting and perform actions.
*   **WebRTC**: Uses Pion WebRTC for establishing peer-to-peer connections and streaming video frames.
