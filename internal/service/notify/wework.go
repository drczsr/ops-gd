// Package notify 封装企业微信群机器人消息发送。
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WeworkBot 企业微信群机器人(webhook)。
type WeworkBot struct {
	webhook string
	client  *http.Client
}

// NewWeworkBot 用群机器人 webhook URL 构造。
func NewWeworkBot(webhook string) *WeworkBot {
	return &WeworkBot{webhook: webhook, client: &http.Client{Timeout: 5 * time.Second}}
}

type textPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content       string   `json:"content"`
		MentionedList []string `json:"mentioned_list,omitempty"`
	} `json:"text"`
}

// Send 发送文本消息;mentionUserIDs 去空、去重后填入 mentioned_list(@对应成员)。
func (b *WeworkBot) Send(content string, mentionUserIDs []string) error {
	var p textPayload
	p.MsgType = "text"
	p.Text.Content = content
	p.Text.MentionedList = dedup(mentionUserIDs)

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wework webhook http %d", resp.StatusCode)
	}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if r.ErrCode != 0 {
		return fmt.Errorf("wework webhook errcode %d: %s", r.ErrCode, r.ErrMsg)
	}
	return nil
}

// dedup 去空白、去重,保持首次出现顺序。
func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
