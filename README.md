# gflow-cli ⚡

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Google Flow](https://img.shields.io/badge/Google_Flow-Imagen_4_&_Veo_3.1-4285F4?logo=google)](https://labs.google/fx/tools/flow)
[![MCP v2](https://img.shields.io/badge/MCP_v2-Supported-7057ff)](https://modelcontextprotocol.io)

**gflow** is a single-binary CLI, OpenAI-compatible API, and Model Context Protocol (MCP) server for **Google Flow** image and video generation.

- 🖼️ **Image Generation** via **Imagen 4 / Nano Banana 2** (`NARWHAL`, `HARBOR_SEAL`, `GEM_PIX_2`).
- 🎬 **Video Generation** via **Veo 3.1** (`abra_t2v` 4s, 6s, 8s, 10s, and `fast_ultra`).
- 🔍 **Video Upsampling** (720p native to 1080p and 4K).
- 🧩 **Zero-Friction Setup**: Chrome extension is embedded inside the Go binary (`gflow setup`).
- 🤖 **Native MCP Server** for Claude Desktop, Cursor, OpenCode, Cline, and Windsurf.
- ⚡ **Zero External Runtimes**: Pure native Go binary. No Python, no virtualenvs, no Node, no Selenium.

---

## Installation

### Scoop (Windows)
```powershell
# Authenticate Scoop for private release downloads
scoop config gh_token ghp_your_token

# Add bucket from this repository and install
scoop bucket add xibodev https://github.com/xibodev/gflow-cli
scoop install gflow
```

### Homebrew (macOS & Linux)
```bash
# Set your token for private asset access
export HOMEBREW_GITHUB_API_TOKEN="ghp_your_token"

# Tap directly from this repository and install
brew tap xibodev/gflow-cli https://github.com/xibodev/gflow-cli
brew install gflow
```

### Chocolatey (Windows)
```powershell
choco install gflow
```

### GitHub CLI (Fastest for Team Members)
Since you are already logged in via `gh`:
```powershell
# Windows
gh release download -R xibodev/gflow-cli --pattern "*windows_amd64.zip" -D "$HOME\.gflow\bin"

# macOS & Linux
gh release download -R xibodev/gflow-cli --pattern "*$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m | sed 's/x86_64/amd64/').tar.gz" -D "$HOME/.gflow/bin"
```

### One-Liner Install Scripts
```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/xibodev/gflow-cli/main/scripts/install.ps1 | iex

# macOS & Linux (Bash)
curl -fsSL https://raw.githubusercontent.com/xibodev/gflow-cli/main/scripts/install.sh | bash
```

### From Source (`go install`)
```bash
go install github.com/xibodev/gflow-cli/cmd/gflow@latest
```

---

## 30-Second Quickstart

### 1. Run Setup (One-Time)
```bash
gflow setup
```
This extracts the bundled extension to `~/.gflow/extension` and opens Chrome:
1. Open `chrome://extensions` in Chrome.
2. Toggle on **Developer mode** (top-right).
3. Click **Load unpacked** and select the printed directory (`~/.gflow/extension`).
4. Ensure you are signed in on [Google Flow](https://labs.google/fx/tools/flow).

### 2. Verify Connection
```bash
gflow status
```
```text
Server:             Running on http://127.0.0.1:8001
Extension Status:   ✔ Connected
Google Flow Token:  ✔ Captured / Ready
Active Workers:     1
Overall Health:     healthy
```

---

## CLI Usage

### Generate Images (Imagen 4 / Nano Banana 2)
```bash
# Generate landscape image
gflow image "a cyberpunk robot drinking coffee in Tokyo at night"

# Square aspect ratio with Nano Banana Pro
gflow image "minimalist origami eagle logo" -a square -m pro

# Generate 4 variations
gflow image "ancient floating library among clouds" -c 4 -o ./my_images/

# Image-to-image style transfer using reference
gflow image "restyle as an oil painting" --ref portrait.png
```

**Options**:
- `-a, --aspect`: `landscape` (16:9), `square` (1:1), `portrait` (9:16), `4:3`, `3:4`
- `-c, --count`: Number of variations (1–4)
- `-m, --model`: `narwhal` (standard), `harbor_seal` (lite), `gem_pix_2` (pro)
- `-o, --output`: Output file or directory
- `--ref`: Reference image path or media ID
- `--seed`: Reproducible seed integer
- `--json`: Machine-readable JSON output

---

### Generate Videos (Veo 3.1)
```bash
# 10s landscape video
gflow video "a dragon soaring over snowy mountain peaks" -d 10 -a landscape

# 4s video delivered at 1080p
gflow video "neon flower blooming in slow motion" -d 4 -r 1080p -o flower.mp4

# Video starting from an image
gflow video "the car accelerates into the sunset" --start car.png

# Video transitioning between first and last frame
gflow video "scene change from day to night" --start day.png --end night.png
```

**Options**:
- `-d, --duration`: `4`, `6`, `8`, or `10` seconds (default: 10)
- `-a, --aspect`: `landscape`, `portrait`, `square`
- `-r, --resolution`: `720p` (native), `1080p` (upsampled), `4k`
- `-o, --output`: Output path or directory
- `--start`: Start frame image path or media ID
- `--end`: End frame image path or media ID
- `--json`: Machine-readable JSON output

---

### Upsample Existing Video
```bash
# Upsample a finished 720p video to 1080p or 4K
gflow upsample 0143adf4-5864-4cb4-abb5-fe4254ad0dc7 -r 4k -o 4k_clip.mp4
```

---

### View Generation History
```bash
gflow history
```
```text
TIME         TYPE   ID            PROMPT                                LOCAL PATH
----         ----   --            ------                                ----------
09/04 12:21  video  e214a5d2-0bf  a small puppy running in the grass    ./output/video_e214a5d2.mp4
09/04 12:19  image  img_121952_1  a small golden retriever puppy si...  ./output/image_img_121952.png
```

---

## Model Context Protocol (MCP)

**gflow** includes a native stdio MCP server for AI coding assistants and desktop agents:

### Claude Desktop
Add to your `claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "gflow": {
      "command": "gflow",
      "args": ["mcp"]
    }
  }
}
```

### Cursor / OpenCode / Cline / Windsurf
Add to your MCP settings:
```json
{
  "mcpServers": {
    "gflow": {
      "command": "gflow",
      "args": ["mcp"]
    }
  }
}
```

**Available MCP Tools**:
- `generate_flow_image`: Text & reference image generation.
- `generate_flow_video`: Text, start/end frame video generation with upsampling.
- `upsample_flow_video`: Upsample video to 1080p/4K.
- `get_flow_status`: Check connection and token readiness.
- `get_flow_history`: Retrieve recent generation records.

---

## OpenAI-Compatible HTTP API

Start the server:
```bash
gflow serve --port 8001
```

### Generate Images (`POST /v1/images/generations`)
```bash
curl http://127.0.0.1:8001/v1/images/generations \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "a cybernetic tiger in a futuristic forest",
    "n": 1,
    "size": "1024x1024"
  }'
```

### Submit Video (`POST /v1/videos/generations`)
```bash
curl http://127.0.0.1:8001/v1/videos/generations \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "ocean waves crashing against rocky cliffs",
    "duration": 6,
    "aspect": "landscape"
  }'
```

---

## How It Works

Google Flow secures all generative APIs with **reCAPTCHA Enterprise v3**. Headless browsers and fresh automation profiles get assigned low trust scores (<0.3), causing Google to return `403 Forbidden`.

`gflow` solves this with an elegant two-tier architecture:
1. **Lightweight Extension Bridge**: Runs inside your everyday, logged-in browser session on `labs.google/fx/tools/flow`.
2. **Authentic reCAPTCHA Execution**: When a generation command is issued, `gflow` requests a reCAPTCHA token inside the live Flow page context, ensuring a **1.0 trust score**.
3. **Pure Go Execution**: 100% of the API communication, job polling, media streaming, and file management is handled natively in Go.

---

## License

[MIT License](LICENSE)
