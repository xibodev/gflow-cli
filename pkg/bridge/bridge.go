package bridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"

	"github.com/gorilla/websocket"
)

// Ownership rule: mu protects sessions metadata, command queues and pending
// maps. Per-session wsMu serializes WebSocket writes. Never hold mu while
// doing network I/O.
var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  64 * 1024,
		WriteBufferSize: 64 * 1024,
		CheckOrigin:     checkWSOrigin,
	}

	maxQueuePerSession = 64
	maxSessions        = 256
	sessionLiveness    = 30 * time.Second
	sessionRetention   = 5 * time.Minute
	pendingTTL         = 5 * time.Minute
)

// Session represents one authenticated browser extension client. HTTP and WS
// transports attach to the same session; a generation counter prevents an old
// connection from cleaning up a newer one.
type Session struct {
	ID       string
	FlowKey  string
	LastSeen time.Time
	WSConn   *websocket.Conn
	wsMu     sync.Mutex
	wsGen    uint64
}

type pendingReq struct {
	ch      chan *models.ExtensionCallback
	owner   string
	expires time.Time
}

// ExtensionBridge coordinates communication between the Go agent and Chrome extension(s).
type ExtensionBridge struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	commandQueue map[string][]models.ExtensionCommand
	pending      map[string]*pendingReq
	bridgeToken  string
	now          func() time.Time
}

// NewExtensionBridge creates a bridge with a random local token. Prefer
// NewExtensionBridgeWithToken with the persisted config token.
func NewExtensionBridge() *ExtensionBridge {
	return NewExtensionBridgeWithToken(randomID() + randomID())
}

// NewExtensionBridgeWithToken creates a bridge authenticated by bridgeToken.
func NewExtensionBridgeWithToken(bridgeToken string) *ExtensionBridge {
	return &ExtensionBridge{
		sessions:     make(map[string]*Session),
		commandQueue: make(map[string][]models.ExtensionCommand),
		pending:      make(map[string]*pendingReq),
		bridgeToken:  bridgeToken,
		now:          time.Now,
	}
}

func (b *ExtensionBridge) checkBridgeAuth(r *http.Request) bool {
	if b.bridgeToken == "" {
		return false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return false
	}
	got := h[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(b.bridgeToken)) == 1
}

func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // native clients, curl, service workers without Origin
	}
	// Only the extension itself may open bridge sockets.
	if len(origin) >= 19 && origin[:19] == "chrome-extension://" {
		return true
	}
	return false
}

func (b *ExtensionBridge) liveLocked(now time.Time) []*Session {
	var out []*Session
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness {
			out = append(out, s)
		}
	}
	return out
}

// IsConnected returns whether at least one browser extension is live.
func (b *ExtensionBridge) IsConnected() bool {
	now := b.now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness {
			return true
		}
	}
	return false
}

// HasFlowKey returns whether a live session captured a Flow token.
func (b *ExtensionBridge) HasFlowKey() bool {
	now := b.now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness && s.FlowKey != "" {
			return true
		}
	}
	return false
}

// GetFlowKey is test/debug convenience; never log its result.
func (b *ExtensionBridge) GetFlowKey() string {
	now := b.now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	ids := make([]string, 0, len(b.sessions))
	for id, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness && s.FlowKey != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return b.sessions[ids[0]].FlowKey
}

// ActiveSessionCount returns the number of live sessions.
func (b *ExtensionBridge) ActiveSessionCount() int {
	now := b.now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness {
			count++
		}
	}
	return count
}

func (b *ExtensionBridge) selectSession() (*Session, error) {
	now := b.now()
	b.mu.RLock()
	defer b.mu.RUnlock()
	var withKey, anyLive []*Session
	for _, s := range b.sessions {
		if now.Sub(s.LastSeen) < sessionLiveness {
			anyLive = append(anyLive, s)
			if s.FlowKey != "" {
				withKey = append(withKey, s)
			}
		}
	}
	pool := withKey
	if len(pool) == 0 {
		pool = anyLive
	}
	if len(pool) == 0 {
		return nil, errors.New("no active Chrome extension connected (load extension and open https://labs.google/fx/tools/flow)")
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].ID < pool[j].ID })
	return pool[0], nil
}

func (b *ExtensionBridge) pruneExpired(now time.Time) {
	for id, p := range b.pending {
		if now.After(p.expires) {
			delete(b.pending, id)
		}
	}
	if len(b.sessions) <= maxSessions {
		return
	}
	type aged struct {
		id   string
		seen time.Time
	}
	var all []aged
	for id, s := range b.sessions {
		all = append(all, aged{id, s.LastSeen})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].seen.Before(all[j].seen) })
	for i := 0; i < len(all)-maxSessions; i++ {
		delete(b.sessions, all[i].id)
		delete(b.commandQueue, all[i].id)
	}
}

func (b *ExtensionBridge) removeQueued(sessionID, reqID string) {
	q := b.commandQueue[sessionID]
	for i, cmd := range q {
		if cmd.ID == reqID {
			b.commandQueue[sessionID] = append(q[:i], q[i+1:]...)
			return
		}
	}
}

func (b *ExtensionBridge) sendCommand(ctx context.Context, sess *Session, cmd models.ExtensionCommand) (*models.ExtensionCallback, error) {
	respChan := make(chan *models.ExtensionCallback, 1)
	now := b.now()
	b.mu.Lock()
	b.pruneExpired(now)
	b.pending[cmd.ID] = &pendingReq{ch: respChan, owner: sess.ID, expires: now.Add(pendingTTL)}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.pending, cmd.ID)
		b.removeQueued(sess.ID, cmd.ID)
		b.mu.Unlock()
	}()

	// Try WebSocket first with a write deadline; never hold b.mu during I/O.
	sentViaWS := false
	sess.wsMu.Lock()
	conn := sess.WSConn
	if conn != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(cmd); err == nil {
			sentViaWS = true
		}
	}
	sess.wsMu.Unlock()

	if !sentViaWS {
		b.mu.Lock()
		if len(b.commandQueue[sess.ID]) >= maxQueuePerSession {
			b.mu.Unlock()
			return nil, fmt.Errorf("%w: extension command queue full", models.ErrUpstream)
		}
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

// ExecuteAPIRequest sends an api_request command to the extension and blocks until completion.
func (b *ExtensionBridge) ExecuteAPIRequest(ctx context.Context, urlPath string, body any, captchaAction string, method string, headers map[string]string) (*models.ExtensionCallback, error) {
	sess, err := b.selectSession()
	if err != nil {
		return nil, err
	}
	cmd := models.ExtensionCommand{
		ID:     randomID(),
		Method: "api_request",
		Params: map[string]any{
			"url":           urlPath,
			"method":        method,
			"headers":       headers,
			"body":          body,
			"captchaAction": captchaAction,
		},
	}
	return b.sendCommand(ctx, sess, cmd)
}

// RequestMediaURL asks the extension to resolve a signed media redirect.
func (b *ExtensionBridge) RequestMediaURL(ctx context.Context, mediaID string) (string, error) {
	sess, err := b.selectSession()
	if err != nil {
		return "", err
	}
	cmd := models.ExtensionCommand{
		ID:     randomID(),
		Method: "get_media_url",
		Params: map[string]any{
			"media_id": mediaID,
		},
	}
	resp, err := b.sendCommand(ctx, sess, cmd)
	if err != nil {
		return "", err
	}
	if resp.Status != 0 && resp.Status != 200 {
		return "", fmt.Errorf("media URL lookup failed (status %d): %s", resp.Status, resp.Error)
	}
	if u, ok := resp.Result["url"].(string); ok && u != "" {
		return u, nil
	}
	return "", fmt.Errorf("media URL not found in result: %v", resp.Result)
}

// RegisterRoutes registers the extension bridge HTTP and WebSocket handlers.
func (b *ExtensionBridge) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ext/hello", b.handleHello)
	mux.HandleFunc("/api/ext/poll", b.handlePoll)
	mux.HandleFunc("/api/ext/callback", b.handleCallback)
	mux.HandleFunc("/ws", b.handleWebSocket)
}

func (b *ExtensionBridge) handleHello(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !b.checkBridgeAuth(r) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid bridge credentials")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req models.ExtensionHello
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid hello payload")
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
	sess.LastSeen = b.now()
	if req.FlowKey != "" {
		sess.FlowKey = req.FlowKey
	}
	b.mu.Unlock()

	resp := models.ExtensionHelloResponse{
		OK:             true,
		SessionID:      sessionID,
		CallbackURL:    "/api/ext/callback",
		PollURL:        "/api/ext/poll",
		PollIntervalMs: 1000,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (b *ExtensionBridge) handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if !b.checkBridgeAuth(r) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid bridge credentials")
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "missing session_id")
		return
	}
	b.mu.Lock()
	sess, exists := b.sessions[sessionID]
	if !exists {
		b.mu.Unlock()
		writeAPIError(w, http.StatusNotFound, "re_register", "unknown session; re-register via /api/ext/hello")
		return
	}
	sess.LastSeen = b.now()
	cmds := b.commandQueue[sessionID]
	b.commandQueue[sessionID] = nil
	b.mu.Unlock()
	if cmds == nil {
		cmds = []models.ExtensionCommand{}
	}
	resp := models.ExtensionPollResponse{OK: true, Commands: cmds, ServerTime: time.Now().UnixMilli()}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (b *ExtensionBridge) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !b.checkBridgeAuth(r) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "invalid bridge credentials")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var cb models.ExtensionCallback
	if err := json.NewDecoder(r.Body).Decode(&cb); err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "invalid callback payload")
		return
	}
	if cb.SessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "missing session_id")
		return
	}
	b.mu.Lock()
	sess, ok := b.sessions[cb.SessionID]
	if !ok {
		b.mu.Unlock()
		writeAPIError(w, http.StatusNotFound, "re_register", "unknown session; re-register via /api/ext/hello")
		return
	}
	sess.LastSeen = b.now()
	if cb.FlowKey != "" {
		sess.FlowKey = cb.FlowKey // this session only; never broadcast
	}
	var ch chan *models.ExtensionCallback
	if cb.ID != "" {
		if p, exists := b.pending[cb.ID]; exists {
			if p.owner != "" && p.owner != cb.SessionID {
				b.mu.Unlock()
				writeAPIError(w, http.StatusForbidden, "forbidden", "callback session does not own this request")
				return
			}
			ch = p.ch
		}
	}
	b.mu.Unlock()
	if ch != nil {
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
		return
	}
	defer conn.Close()
	conn.SetReadLimit(2 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	var first map[string]any
	if err := conn.ReadJSON(&first); err != nil {
		return
	}
	msgType, _ := first["type"].(string)
	if msgType != "extension_ready" {
		return
	}
	cid, _ := first["clientId"].(string)
	if cid == "" {
		cid, _ = first["client_id"].(string)
	}
	token, _ := first["bridgeToken"].(string)
	if token == "" {
		token, _ = first["bridge_token"].(string)
	}
	if cid == "" || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(b.bridgeToken)) != 1 {
		return
	}

	b.mu.Lock()
	sess, exists := b.sessions[cid]
	if !exists {
		sess = &Session{ID: cid}
		b.sessions[cid] = sess
	}
	sess.wsMu.Lock()
	if sess.WSConn != nil {
		_ = sess.WSConn.Close()
	}
	sess.WSConn = conn
	sess.wsGen++
	gen := sess.wsGen
	sess.wsMu.Unlock()
	sess.LastSeen = b.now()
	if key, ok := first["flowKey"].(string); ok && key != "" {
		sess.FlowKey = key
	}
	b.mu.Unlock()

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		b.mu.Lock()
		if s, ok := b.sessions[cid]; ok {
			s.LastSeen = b.now()
		}
		b.mu.Unlock()
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			sess.wsMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			sess.wsMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	for {
		var raw map[string]any
		if err := conn.ReadJSON(&raw); err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		b.mu.Lock()
		if s, ok := b.sessions[cid]; ok {
			s.LastSeen = b.now()
		}
		msgType, _ := raw["type"].(string)
		if msgType == "token_captured" {
			if key, ok := raw["flowKey"].(string); ok && key != "" {
				if s, ok := b.sessions[cid]; ok {
					s.FlowKey = key
				}
			}
			b.mu.Unlock()
			continue
		}
		var ch chan *models.ExtensionCallback
		if reqID, ok := raw["id"].(string); ok && reqID != "" {
			if p, exists := b.pending[reqID]; exists && (p.owner == "" || p.owner == cid) {
				ch = p.ch
			}
		}
		b.mu.Unlock()
		if ch != nil {
			data, _ := json.Marshal(raw)
			var cb models.ExtensionCallback
			_ = json.Unmarshal(data, &cb)
			if cb.SessionID == "" {
				cb.SessionID = cid
			}
			select {
			case ch <- &cb:
			default:
			}
		}
	}

	// Clear only if this connection is still current; keep the session for HTTP.
	b.mu.Lock()
	if s, ok := b.sessions[cid]; ok && s == sess && sess.wsGen == gen {
		sess.wsMu.Lock()
		sess.WSConn = nil
		sess.wsMu.Unlock()
	}
	b.mu.Unlock()
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
