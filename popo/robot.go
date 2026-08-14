// Package popo is a slim POPO robot client: token acquisition + text message
// sending. Ported from github.com/REDACTED_USER/goutils/popo_sdk (SendMessage path only)
// to avoid pulling the full goutils module's heavy transitive dependencies.
package popo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	baseURL    = "https://open.popo.netease.com/open-apis/robots/v1"
	tokenURL   = baseURL + "/token"
	sendMsgURL = baseURL + "/im/send-msg"
)

type tokenResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int64  `json:"expiresIn"`
	} `json:"data"`
}

type sendMsgRequest struct {
	Receiver string      `json:"receiver"`
	Message  messageBody `json:"message"`
	MsgType  string      `json:"msgType"`
}

type messageBody struct {
	Content string `json:"content"`
}

type sendMsgResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// Robot is a POPO robot client that caches its access token.
type Robot struct {
	appKey      string
	appSecret   string
	accessToken string
	tokenExpiry time.Time
	client      *http.Client
	mu          sync.RWMutex
}

// NewRobot creates a Robot with the given appKey and appSecret.
func NewRobot(appKey, appSecret string) *Robot {
	return &Robot{
		appKey:    appKey,
		appSecret: appSecret,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getAccessToken returns a cached token or fetches a new one.
func (r *Robot) getAccessToken() (string, error) {
	r.mu.RLock()
	if r.accessToken != "" && time.Now().Before(r.tokenExpiry) {
		token := r.accessToken
		r.mu.RUnlock()
		return token, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.accessToken != "" && time.Now().Before(r.tokenExpiry) {
		return r.accessToken, nil
	}

	body, err := json.Marshal(map[string]string{
		"appKey":    r.appKey,
		"appSecret": r.appSecret,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", tokenURL, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get token failed, HTTP %d: %s", resp.StatusCode, string(b))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if tr.Code != 0 {
		return "", fmt.Errorf("get token failed, code %d: %s", tr.Code, tr.Message)
	}

	r.accessToken = tr.Data.AccessToken
	r.tokenExpiry = time.Now().Add(time.Duration(tr.Data.ExpiresIn)*time.Second - 5*time.Minute)
	return r.accessToken, nil
}

// SendMessage sends a text message to receiver (email or group ID).
func (r *Robot) SendMessage(receiver, content string) error {
	token, err := r.getAccessToken()
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	body, err := json.Marshal(sendMsgRequest{
		Receiver: receiver,
		Message:  messageBody{Content: content},
		MsgType:  "text",
	})
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", sendMsgURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Open-Access-Token", token)

	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("send message failed, HTTP %d: %s", resp.StatusCode, string(b))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var sr sendMsgResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if sr.Code != 0 || sr.ErrCode != 0 {
		return fmt.Errorf("send message failed, code %d errcode %d: %s %s",
			sr.Code, sr.ErrCode, sr.Message, sr.ErrMsg)
	}
	return nil
}
