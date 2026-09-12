package minimax

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session holds the token and device credentials for MiniMax Design cloud.
type Session struct {
	AccessToken string `json:"access_token"`
	GroupID     string `json:"group_id"`
	DeviceID    string `json:"device_id"`
	UserID      string `json:"user_id,omitempty"`
}

// LoadSession resolves the active MiniMax session from local files.
// It checks in order:
// 1. ~/.gflow/minimax.json (explicitly saved/configured)
// 2. %TEMP%/minimax-hub-gateway-token-global (live desktop app token)
// 3. %APPDATA%/@hilo/MiniMax Hub Global/hub-config-global.json
func LoadSession() (*Session, error) {
	// 1. Check ~/.gflow/minimax.json
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".gflow", "minimax.json")
		if data, err := os.ReadFile(p); err == nil {
			var sess Session
			if jerr := json.Unmarshal(data, &sess); jerr == nil && sess.AccessToken != "" {
				return &sess, nil
			}
		}
	}

	// 2. Check %TEMP% live token files
	tempDir := os.TempDir()
	tokenFile := filepath.Join(tempDir, "minimax-hub-gateway-token-global")
	groupIDFile := filepath.Join(tempDir, "minimax-hub-gateway-group-id-global")

	liveToken, err := os.ReadFile(tokenFile)
	if err == nil && len(strings.TrimSpace(string(liveToken))) > 20 {
		tok := strings.TrimSpace(string(liveToken))
		gid := ""
		if gBytes, err := os.ReadFile(groupIDFile); err == nil {
			gid = strings.TrimSpace(string(gBytes))
		}

		devID := resolveDeviceID()
		return &Session{
			AccessToken: tok,
			GroupID:     gid,
			DeviceID:    devID,
		}, nil
	}

	// 3. Check %APPDATA%/@hilo/MiniMax Hub Global/hub-config-global.json
	appData := os.Getenv("APPDATA")
	if appData != "" {
		confPath := filepath.Join(appData, "@hilo", "MiniMax Hub Global", "hub-config-global.json")
		if data, err := os.ReadFile(confPath); err == nil {
			var conf struct {
				Tokens struct {
					AccessToken string `json:"accessToken"`
				} `json:"tokens"`
				DeviceID string `json:"deviceId"`
				User     struct {
					UserID string `json:"userID"`
				} `json:"user"`
			}
			if jerr := json.Unmarshal(data, &conf); jerr == nil && conf.Tokens.AccessToken != "" {
				return &Session{
					AccessToken: conf.Tokens.AccessToken,
					DeviceID:    conf.DeviceID,
					UserID:      conf.User.UserID,
				}, nil
			}
		}
	}

	return nil, errors.New("no MiniMax Design session found. Please run MiniMax Design once to log in, or set ~/.gflow/minimax.json")
}

// SaveSession saves session tokens to ~/.gflow/minimax.json.
func SaveSession(sess *Session) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".gflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "minimax.json"), data, 0600)
}

func resolveDeviceID() string {
	appData := os.Getenv("APPDATA")
	if appData != "" {
		confPath := filepath.Join(appData, "@hilo", "MiniMax Hub Global", "hub-config-global.json")
		if data, err := os.ReadFile(confPath); err == nil {
			var conf struct {
				DeviceID string `json:"deviceId"`
			}
			if jerr := json.Unmarshal(data, &conf); jerr == nil && conf.DeviceID != "" {
				return conf.DeviceID
			}
		}
	}
	return fmt.Sprintf("dev_%d", time.Now().UnixNano())
}
