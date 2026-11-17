// Copyright (c) 2026 Vassel Goleyu. MIT License.

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal Telegram Bot API client (getUpdates and sendMessage). The bot token is
// part of every request URL, so it is scrubbed from every error this package returns.
type Client struct {
	Token   string
	BaseURL string // default https://api.telegram.org
	HTTP    *http.Client
}

// APIError is an error answer from Telegram.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration // set on 429
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

// Update is one incoming event; only text messages are used.
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is a Telegram message.
type Message struct {
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
}

type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) scrub(err error) error {
	if err == nil || c.Token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.Token, "<token>"))
}

func (c *Client) call(ctx context.Context, method string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+c.Token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return c.scrub(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 40 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return c.scrub(fmt.Errorf("telegram %s: %w", method, err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return c.scrub(fmt.Errorf("read telegram response: %w", err))
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return &APIError{Code: resp.StatusCode, Description: "unparsable response"}
	}
	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Code: code, Description: env.Description, RetryAfter: time.Duration(env.Parameters.RetryAfter) * time.Second}
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode telegram result: %w", err)
		}
	}
	return nil
}

// GetUpdates long-polls for updates at or after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeoutSec, "allowed_updates": []string{"message"}}, &ups)
	return ups, err
}

// SendMessage sends HTML text to a chat.
func (c *Client) SendMessage(ctx context.Context, chatID, text string) error {
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true}, nil)
}
