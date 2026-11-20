package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const botTopic = "/v1.0/im/bot/messages/get"

type BotMessage struct {
	ID               string `json:"msgId"`
	RobotCode        string `json:"robotCode"`
	RobotCorpID      string `json:"chatbotCorpId"`
	SenderCorpID     string `json:"senderCorpId"`
	SenderUserID     string `json:"senderStaffId"`
	ConversationType string `json:"conversationType"`
	MessageType      string `json:"msgtype"`
	Text             struct {
		Content string `json:"content"`
	} `json:"text"`
}

func (c *Client) SendText(ctx context.Context, robotCode, userID, text string) error {
	token, err := c.AppToken(ctx)
	if err != nil {
		return err
	}
	params, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return err
	}
	var out struct {
		Key         string   `json:"processQueryKey"`
		Invalid     []string `json:"invalidStaffIdList"`
		RateLimited []string `json:"flowControlledStaffIdList"`
	}
	err = c.call(ctx, "DingTalk bot reply", http.MethodPost, api+"/v1.0/robot/oToMessages/batchSend", token, map[string]any{
		"robotCode": robotCode, "userIds": []string{userID}, "msgKey": "sampleText", "msgParam": string(params),
	}, &out)
	if err != nil {
		return err
	}
	if out.Key == "" || len(out.Invalid) != 0 || len(out.RateLimited) != 0 {
		return errors.New("DingTalk did not accept the bot reply for the employee")
	}
	return nil
}

type DialStream func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func (c *Client) Receive(ctx context.Context, dial DialStream, handle func(context.Context, BotMessage) error) error {
	var grant struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
	}
	err := c.call(ctx, "DingTalk Stream registration", http.MethodPost, api+"/v1.0/gateway/connections/open", "", map[string]any{
		"clientId": c.ID, "clientSecret": c.Secret,
		"subscriptions": []map[string]string{{"type": "CALLBACK", "topic": botTopic}},
	}, &grant)
	if err != nil {
		return err
	}
	u, err := url.Parse(grant.Endpoint)
	if err != nil || u.Scheme != "wss" || !strings.HasSuffix(u.Hostname(), ".dingtalk.com") ||
		u.User != nil || u.Fragment != "" || grant.Ticket == "" {
		return errors.New("DingTalk returned an invalid Stream endpoint")
	}
	q := u.Query()
	q.Set("ticket", grant.Ticket)
	u.RawQuery = q.Encode()
	if dial == nil {
		dial = websocket.DefaultDialer.DialContext
	}
	conn, resp, err := dial(ctx, u.String(), nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return errors.New("DingTalk Stream connection failed")
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	conn.SetReadLimit(1 << 20)
	keepalive := time.NewTicker(45 * time.Second)
	defer keepalive.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepalive.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(2 * time.Minute)) })
	for {
		var frame struct {
			Type    string            `json:"type"`
			Headers map[string]string `json:"headers"`
			Data    string            `json:"data"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			return errors.New("DingTalk Stream disconnected")
		}
		topic, id := frame.Headers["topic"], frame.Headers["messageId"]
		if frame.Type == "SYSTEM" && topic == "disconnect" {
			return nil
		}
		if id == "" {
			continue
		}
		data := `{"response":{}}`
		code, message := 200, "OK"
		switch {
		case frame.Type == "SYSTEM" && topic == "ping":
			data = frame.Data
		case frame.Type == "CALLBACK" && topic == botTopic:
			var msg BotMessage
			if json.Unmarshal([]byte(frame.Data), &msg) == nil {
				if err := handle(ctx, msg); err != nil {
					code, message = 500, "retry"
				}
			}
		default:
			continue
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(map[string]any{
			"code": code, "headers": map[string]string{"messageId": id, "contentType": "application/json"},
			"message": message, "data": data,
		}); err != nil {
			return errors.New("DingTalk Stream acknowledgement failed")
		}
	}
}
