# gflow-cli ⚡

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev)
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
This extracts the bundled extension to `~/.gflow/extension`, writes its local
endpoint configuration, and opens Chrome:
1. Open `chrome://extensions` in Chrome.
2. Toggle on **Developer mode** (top-right).
3. Click **Load unpacked** and select the printed directory (`~/.gflow/extension`).
4. Ensure you are signed in on [Google Flow](https://labs.google/fx/tools/flow).
5. Set your Flow project for generation (status/setup work without it):
   `DEFAULT_PROJECT=<your-flow-project-id>`.

Setup generates persistent local credentials (`~/.gflow/auth.json`) for the
CLI/MCP client and the extension. If you change host/port (`FLOW_HOST` /
`FLOW_PORT` or `gflow serve --host/--port`), re-run `gflow setup` and click
**Reload** on the unpacked extension so it picks up the new endpoints.

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
gflow upsample <MEDIA_ID> -r 4k -o 4k_clip.mp4
```
Video generation always renders native 720p first; `1080p`/`4k` are delivered
via this separate upsample step. If upsampling fails, the original native
video ID is reported so you can retry without regenerating.

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

**Available MCP Tools** (served over stdio by `gflow mcp`, backed by the same
local daemon as the CLI):
- `generate_flow_image`: Prompt, aspect, model, count, `reference_image`
  (file path or media ID), and `seed`.
- `generate_flow_video`: Prompt, duration, aspect, `resolution` (720p native;
  1080p/4k trigger the upsample step), `start_image`/`end_image`, `seed`.
- `upsample_flow_video`: Upsample video to 1080p/4K.
- `get_flow_status`: Check daemon, extension, and token readiness.
- `get_flow_history`: Retrieve recent generation records.

---

## OpenAI-Compatible HTTP API

Start the server:
```bash
gflow serve --port 8001
```

The daemon requires a local API token on all `/v1/*` routes. `gflow setup`
writes it to `~/.gflow/auth.json`; pass it as a Bearer token (placeholder
below — never commit real tokens):

### Generate Images (`POST /v1/images/generations`)
```bash
curl http://127.0.0.1:8001/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $GFLOW_API_TOKEN" \
  -d '{
    "prompt": "a cybernetic tiger in a futuristic forest",
    "n": 1,
    "aspect": "square",
    "reference_media_ids": [],
    "seed": 42
  }'
```
(`size` accepts OpenAI dimensions like `1024x1024`; explicit `aspect` wins.
`response_format` is `url` (default) or `b64_json`. Uploads use multipart
`POST /v1/upload` with a `file` field — server-side file paths are rejected.)

### Submit Video (`POST /v1/videos/generations`)
```bash
curl http://127.0.0.1:8001/v1/videos/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $GFLOW_API_TOKEN" \
  -d '{
    "prompt": "ocean waves crashing against rocky cliffs",
    "duration": 6,
    "aspect": "landscape"
  }'
```
Submit native resolution only; request `1080p`/`4k` via
`POST /v1/videos/upsample` after the job succeeds. Polling
`GET /v1/videos/generations/<job_id>` returns `processing`, `succeeded`
(with assets), or `failed` (with a structured error) — failures are never
reported as `processing`.

---

## How It Works

Google Flow secures generative APIs with **reCAPTCHA Enterprise v3**.
Headless browsers and fresh automation profiles often score poorly, causing
Google to return `403 Forbidden`.

`gflow` uses a two-tier architecture:
1. **Lightweight Extension Bridge**: Runs inside your everyday, logged-in
   browser session on `labs.google/fx/tools/flow`.
2. **Authentic reCAPTCHA Execution**: When a generation command is issued,
   the extension requests a reCAPTCHA token inside the live Flow page
   context. (No specific trust score is guaranteed; upstream behavior may change.)
3. **Go Daemon + Browser Fetch**: Go builds request payloads, coordinates
   polling, and manages files; the actual upstream API calls execute as
   `fetch()` in the logged-in browser session, which supplies the Google
   session and CAPTCHA token.

---

## License

[MIT License](LICENSE)
