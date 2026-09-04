package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Session represents an active connected browser extension.
type Session struct {
	ID        string
	FlowKey   string
	LastSeen  time.Time
	WSConn    *websocket.Conn
	mu        sync.Mutex
}

// ExtensionBridge coordinates communication between the Go agent and Chrome extension(s).
type ExtensionBridge struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	commandQueue map[string][]models.ExtensionCommand
	pending      map[string]chan *models.ExtensionCallback
	secret       string
}

// NewExtensionBridge creates a new bridge instance.
func NewExtensionBridge() *ExtensionBridge {
	secretBytes := make([]byte, 16)
	_, _ = rand.Read(secretBytes)
	return &ExtensionBridge{
		sessions:     make(map[string]*Session),
		commandQueue: make(map[string][]models.ExtensionCommand),
		pending:      make(map[string]chan *models.ExtensionCallback),
		secret:       hex.EncodeToString(secretBytes),
	}
}

// IsConnected returns whether at least one browser extension is connected and active.
func (b *ExtensionBridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	now := time.Now()
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < 30*time.Second {
			return true
		}
	}
	return false
}

// HasFlowKey returns whether a valid Bearer token has been captured.
func (b *ExtensionBridge) HasFlowKey() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.sessions {
		if s.FlowKey != "" {
			return true
		}
	}
	return false
}

// GetFlowKey returns the best available captured Bearer token.
func (b *ExtensionBridge) GetFlowKey() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.sessions {
		if s.FlowKey != "" {
			return s.FlowKey
		}
	}
	return ""
}

// ActiveSessionCount returns the number of alive sessions.
func (b *ExtensionBridge) ActiveSessionCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	now := time.Now()
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < 30*time.Second {
			count++
		}
	}
	return count
}

func (b *ExtensionBridge) selectSession() (*Session, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	now := time.Now()
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < 30*time.Second {
			return s, nil
		}
	}
	return nil, errors.New("no active Chrome extension connected (load extension and open https://labs.google/fx/tools/flow)")
}

// ExecuteAPIRequest sends an api_request command to the extension and blocks until completion.
func (b *ExtensionBridge) ExecuteAPIRequest(ctx context.Context, urlPath string, body any, captchaAction string, method string, headers map[string]string) (*models.ExtensionCallback, error) {
	sess, err := b.selectSession()
	if err != nil {
		return nil, err
	}

	reqID := randomID()
	respChan := make(chan *models.ExtensionCallback, 1)

	b.mu.Lock()
	b.pending[reqID] = respChan
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, reqID)
		b.mu.Unlock()
	}()

	cmd := models.ExtensionCommand{
		ID:     reqID,
		Method: "api_request",
		Params: map[string]any{
			"url":           urlPath,
			"method":        method,
			"headers":       headers,
			"body":          body,
			"captchaAction": captchaAction,
		},
	}

	// Try sending via WebSocket if open
	sentViaWS := false
	sess.mu.Lock()
	if sess.WSConn != nil {
		if err := sess.WSConn.WriteJSON(cmd); err == nil {
			sentViaWS = true
		}
	}
	sess.mu.Unlock()

	if !sentViaWS {
		// Enqueue for HTTP poll
		b.mu.Lock()
		b.commandQueue[sess.ID] = append(b.commandQueue[sess.ID], cmd)
		b.mu.Unlock()
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respChan:
		if resp == nil {
			return nil, errors.New("empty response received from extension")
		}
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return resp, nil
	}
}

// RequestMediaURL asks the extension to resolve a signed media redirect.
func (b *ExtensionBridge) RequestMediaURL(ctx context.Context, mediaID string) (string, error) {
	sess, err := b.selectSession()
	if err != nil {
		return "", err
	}

	reqID := randomID()
	respChan := make(chan *models.ExtensionCallback, 1)

	b.mu.Lock()
	b.pending[reqID] = respChan
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, reqID)
		b.mu.Unlock()
	}()

	cmd := models.ExtensionCommand{
		ID:     reqID,
		Method: "get_media_url",
		Params: map[string]any{
			"media_id": mediaID,
		},
	}

	sess.mu.Lock()
	if sess.WSConn != nil {
		_ = sess.WSConn.WriteJSON(cmd)
	} else {
		b.mu.Lock()
		b.commandQueue[sess.ID] = append(b.commandQueue[sess.ID], cmd)
		b.mu.Unlock()
	}
	sess.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case resp := <-respChan:
		if resp == nil {
			return "", errors.New("empty response for media URL")
		}
		if u, ok := resp.Result["url"].(string); ok && u != "" {
			return u, nil
		}
		return "", fmt.Errorf("media URL not found in result: %v", resp.Result)
	}
}

// RegisterRoutes registers the extension bridge HTTP and WebSocket handlers.
func (b *ExtensionBridge) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ext/hello", b.handleHello)
	mux.HandleFunc("/api/ext/poll", b.handlePoll)
	mux.HandleFunc("/api/ext/callback", b.handleCallback)
	mux.HandleFunc("/ws", b.handleWebSocket)
}

func (b *ExtensionBridge) handleHello(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req models.ExtensionHello
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = req.ClientID
	}
	if sessionID == "" {
		sessionID = "client_" + randomID()
	}

	b.mu.Lock()
	sess, exists := b.sessions[sessionID]
	if !exists {
		sess = &Session{ID: sessionID}
		b.sessions[sessionID] = sess
	}
	sess.LastSeen = time.Now()
	if req.FlowKey != "" {
		sess.FlowKey = req.FlowKey
		log.Printf("[Bridge] Session %s registered with flowKey", sessionID)
	}
	b.mu.Unlock()

	resp := models.ExtensionHelloResponse{
		OK:             true,
		SessionID:      sessionID,
		Secret:         b.secret,
		CallbackURL:    "/api/ext/callback",
		PollURL:        "/api/ext/poll",
		PollIntervalMs: 1000,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (b *ExtensionBridge) handlePoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	b.mu.Lock()
	sess, exists := b.sessions[sessionID]
	if exists {
		sess.LastSeen = time.Now()
	}
	cmds := b.commandQueue[sessionID]
	b.commandQueue[sessionID] = nil
	b.mu.Unlock()

	if cmds == nil {
		cmds = []models.ExtensionCommand{}
	}

	resp := models.ExtensionPollResponse{
		OK:         true,
		Commands:   cmds,
		ServerTime: time.Now().UnixMilli(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (b *ExtensionBridge) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var cb models.ExtensionCallback
	if err := json.Unmarshal(bodyBytes, &cb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	b.mu.Lock()
	if cb.SessionID != "" {
		if sess, ok := b.sessions[cb.SessionID]; ok {
			sess.LastSeen = time.Now()
			if cb.FlowKey != "" {
				sess.FlowKey = cb.FlowKey
			}
		}
	}
	if cb.Type == "token_captured" && cb.FlowKey != "" {
		for _, s := range b.sessions {
			s.FlowKey = cb.FlowKey
		}
		log.Printf("[Bridge] Bearer token captured from extension callback")
	}
	ch, exists := b.pending[cb.ID]
	b.mu.Unlock()

	if exists && ch != nil {
		select {
		case ch <- &cb:
		default:
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (b *ExtensionBridge) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Bridge] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	sessionID := "ws_" + randomID()
	sess := &Session{
		ID:       sessionID,
		LastSeen: time.Now(),
		WSConn:   conn,
	}

	b.mu.Lock()
	b.sessions[sessionID] = sess
	b.mu.Unlock()

	log.Printf("[Bridge] Extension WebSocket client connected: %s", sessionID)

	for {
		var raw map[string]any
		if err := conn.ReadJSON(&raw); err != nil {
			break
		}

		sess.LastSeen = time.Now()
		msgType, _ := raw["type"].(string)

		if msgType == "extension_ready" {
			if cid, ok := raw["clientId"].(string); ok && cid != "" {
				b.mu.Lock()
				delete(b.sessions, sessionID)
				sessionID = cid
				sess.ID = cid
				b.sessions[sessionID] = sess
				b.mu.Unlock()
			}
			log.Printf("[Bridge] Extension client ready: %s", sessionID)
		} else if msgType == "token_captured" {
			if key, ok := raw["flowKey"].(string); ok && key != "" {
				sess.FlowKey = key
				log.Printf("[Bridge] Bearer token captured via WS from %s", sessionID)
			}
		} else {
			// Response to pending command
			if reqID, ok := raw["id"].(string); ok && reqID != "" {
				b.mu.RLock()
				ch, exists := b.pending[reqID]
				b.mu.RUnlock()
				if exists && ch != nil {
					data, _ := json.Marshal(raw)
					var cb models.ExtensionCallback
					_ = json.Unmarshal(data, &cb)
					select {
					case ch <- &cb:
					default:
					}
				}
			}
		}
	}

	b.mu.Lock()
	delete(b.sessions, sessionID)
	b.mu.Unlock()
	log.Printf("[Bridge] Extension WebSocket client disconnected: %s", sessionID)
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
