# gflow-cli ⚡

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Google Flow](https://img.shields.io/badge/Google_Flow-Imagen_4_&_Veo_3.1-4285F4?logo=google)](https://labs.google/fx/tools/flow)
[![MCP v2](https://img.shields.io/badge/MCP_v2-Supported-7057ff)](https://modelcontextprotocol.io)

**gflow-cli** is a single-binary CLI and Model Context Protocol (MCP) server for generative media: image, video, audio, and chat. Its executable/CLI command is `gflow`.

- **Image Generation:** Gemini Imagen 3 and Google Flow Imagen 4 (Pro and Lite models).
- **Video Generation:** Gemini Veo, MiniMax H3 (768P and 2K), and Google Flow Veo 3.1.
- **Audio & Music:** Native music track synthesis via Gemini Lyria / MusicFX (`gflow audio`).
- **Terminal Chat:** Direct conversational interface with Gemini Flash (`gflow chat`).
- **MCP Server:** Native stdio integration for Claude Desktop, Cursor, OpenCode, Cline, and Windsurf.
- **Unified Backends:** Switch between `gemini` (default), `minimax`, and `flow` via `--provider`.

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

### One-Liner Install Scripts (No Extraction, Automatic PATH)
```powershell
# Windows (PowerShell)
irm https://xibodev.github.io/gflow-cli/install.ps1 | iex

# macOS & Linux (Bash)
curl -fsSL https://xibodev.github.io/gflow-cli/install.sh | bash
```

### From Source (`go install`)
```bash
go install github.com/xibodev/gflow-cli/cmd/gflow@latest
```

---

## 30-Second Quickstart

### 1. Zero-Setup Mode (Gemini & MiniMax)
If you have the **Gemini desktop app** or **MiniMax Design** logged in on your machine, zero setup is needed. You can start generating immediately:
```bash
# Instant conversational check
gflow chat "What is quantum computing in one sentence?"

# Generate high-res image (Imagen 3, extension-free)
gflow image "a golden origami butterfly on black velvet"
```

### 2. Google Flow Mode
If you prefer generating via **Google Flow** (Imagen 4 / Veo 3.1):
- **Option A (Extension-Free via CDP, recommended):**
  ```bash
  gflow browser
  ```
  Launches Chrome with a dedicated Flow profile on port 9222. Sign in once, then run `gflow image "..." -P flow`.
- **Option B (Extension Bridge):**
  ```bash
  gflow setup
  ```
  Extracts the bundled Chrome extension to `~/.gflow/extension` and guides you to load it unpacked in `chrome://extensions`.

### 3. Check Provider Status
```bash
gflow status
```
```text
=== AI Providers Status ===

[Gemini] (Default: Imagen 3, Veo, Audio, Chat)
  App Installed:    ✔ Found
  Session State:    ✔ Ready

[MiniMax Design] (Direct Cloud: H3 Video)
  Session State:    ✔ Ready

[Google Flow] (Direct CDP / Daemon)
  Daemon Running:   ✖ Stopped
```

---

## Provider Selection

Select your backend via `--provider` (`-P`) or `export GFLOW_PROVIDER=gemini`:
- `gemini` (**default**): Pure HTTPS requests powered by your local Gemini Desktop session. Generates Imagen 3 images, Veo video, Lyria audio, and terminal chat.
- `minimax`: Direct cloud generation to MiniMax H3 using your desktop app credentials.
- `flow`: Google Flow backend (Imagen 4 and Veo 3.1) via direct CDP or local daemon.

---

## CLI Usage

### 1. Terminal Chat (Gemini)
```bash
# Instant conversational response
gflow chat "What is the airspeed velocity of an unladen swallow?"
```

### 2. Audio & Music Generation (Gemini Lyria)
```bash
# Generate a music track
gflow audio "upbeat acoustic guitar melody with gentle drums"
```

### 3. Generate Images (Imagen 3 / Imagen 4)
```bash
# Generate image via Gemini (default, extension-free)
gflow image "a golden origami butterfly on black velvet"

# Generate image via Google Flow
gflow image "cyberpunk robot in Tokyo at night" --provider flow -a square -m pro
```

### 4. Generate Videos (Veo / MiniMax H3)
```bash
# Generate video via Gemini Veo (default)
gflow video "ocean waves crashing against stormy cliffs"

# Generate video via MiniMax H3
gflow video "a dragon soaring over snowy mountain peaks" --provider minimax -d 4

# Generate video via Google Flow
gflow video "neon flower blooming in slow motion" --provider flow -d 4 -r 1080p
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

**gflow-cli** includes a native stdio MCP server for AI coding assistants and desktop agents.

### Automated Setup (Recommended)
Automatically configure gflow in all installed AI assistants (OpenCode, Claude Desktop, Cursor, Claude Code) with a single command:
```bash
gflow mcp setup
```

### Manual Configuration

#### OpenCode (`~/.config/opencode/opencode.json` or `./opencode.json`)
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "gflow": {
      "type": "local",
      "command": ["gflow", "mcp"],
      "enabled": true
    }
  }
}
```

#### Claude Desktop & Cursor
Add to your `claude_desktop_config.json` or `.cursor/mcp.json`:
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

The **gflow-cli** project's `gflow` executable/CLI command uses a two-tier architecture:
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
