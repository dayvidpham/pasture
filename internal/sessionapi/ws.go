package sessionapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsRefreshInterval = time.Second
	wsWriteTimeout    = 10 * time.Second
	wsPongTimeout     = 60 * time.Second
	wsPingInterval    = 30 * time.Second
	wsReadLimit       = 64 << 10
)

type wsTopic string

const (
	wsTopicSessions wsTopic = "sessions"
	wsTopicEpochs   wsTopic = "epochs"
)

func (t wsTopic) valid() bool {
	return t == wsTopicSessions || t == wsTopicEpochs
}

type wsClientMessage struct {
	Type  string  `json:"type"`
	Topic wsTopic `json:"topic,omitempty"`
	Err   error   `json:"-"`
}

type wsReadyFrame struct {
	Type   string   `json:"type"`
	Topics []string `json:"topics"`
}

type wsAckFrame struct {
	Type  string  `json:"type"`
	Topic wsTopic `json:"topic,omitempty"`
}

type wsSnapshotFrame struct {
	Type     string    `json:"type"`
	Revision int64     `json:"revision"`
	AsOf     time.Time `json:"asOf"`
	Payload  any       `json:"payload"`
}

type wsErrorFrame struct {
	Type  string    `json:"type"`
	Error errorBody `json:"error"`
}

type wsSubscription struct {
	fingerprint [32]byte
	hasFrame    bool
	lastError   string
}

type wsClient struct {
	conn    *websocket.Conn
	service *Service
	send    chan []byte
	subs    map[wsTopic]*wsSubscription

	done     chan struct{}
	closeOne sync.Once
}

func (c *wsClient) shutdown() {
	c.closeOne.Do(func() { close(c.done) })
}

// enqueue implements the bounded latest-frame rule: at most one unread frame
// is retained. A newer frame replaces the older one instead of growing a
// backlog when the client is slower than the refresh tick.
func (c *wsClient) enqueue(data []byte) {
	for {
		select {
		case c.send <- data:
			return
		default:
		}
		select {
		case <-c.send:
		default:
		}
	}
}

func (c *wsClient) sendJSON(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	c.enqueue(data)
}

func (c *wsClient) sendError(code, message string) {
	c.sendJSON(wsErrorFrame{Type: "error", Error: errorBody{Code: code, Message: message}})
}

func (c *wsClient) writeLoop() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case data := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.shutdown()
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.shutdown()
				return
			}
		case <-c.done:
			return
		}
	}
}

func readWSMessages(conn *websocket.Conn, out chan<- wsClientMessage, done <-chan struct{}) {
	defer close(out)
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongTimeout))
	})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var message wsClientMessage
		if err := json.Unmarshal(data, &message); err != nil {
			message = wsClientMessage{Type: "__invalid", Err: fmt.Errorf("message must be one JSON object: %w", err)}
		}
		select {
		case out <- message:
		case <-done:
			return
		}
	}
}

func (c *wsClient) subscribe(ctx context.Context, topic wsTopic) {
	c.subs[topic] = &wsSubscription{}
	c.refresh(ctx, topic)
}

func (c *wsClient) refresh(ctx context.Context, topic wsTopic) {
	sub, ok := c.subs[topic]
	if !ok {
		return
	}
	switch topic {
	case wsTopicSessions:
		page, err := c.service.List(ctx, ListOptions{Limit: 100})
		if err != nil {
			c.publishError(topic, sub, err)
			return
		}
		payload := struct {
			Items []SessionView `json:"items"`
			Next  string        `json:"next,omitempty"`
		}{Items: page.Items, Next: page.Next}
		c.publish(topic, sub, page.Revision, page.AsOf, payload)
	case wsTopicEpochs:
		page, err := c.service.Epochs(ctx)
		if err != nil {
			c.publishError(topic, sub, err)
			return
		}
		payload := struct {
			Items []EpochView `json:"items"`
		}{Items: page.Items}
		c.publish(topic, sub, page.Revision, page.AsOf, payload)
	}
}

func (c *wsClient) publish(topic wsTopic, sub *wsSubscription, revision int64, asOf time.Time, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		c.publishError(topic, sub, err)
		return
	}
	fingerprint := sha256.Sum256(raw)
	if sub.hasFrame && sub.fingerprint == fingerprint {
		return
	}
	sub.fingerprint = fingerprint
	sub.hasFrame = true
	sub.lastError = ""
	c.sendJSON(wsSnapshotFrame{Type: string(topic), Revision: revision, AsOf: asOf, Payload: payload})
}

func (c *wsClient) publishError(topic wsTopic, sub *wsSubscription, err error) {
	code, message := wsErrorFor(err)
	key := code + "\x00" + message
	if sub.lastError == key {
		return
	}
	sub.lastError = key
	sub.hasFrame = false
	c.sendError(code, message)
}

func wsErrorFor(err error) (string, string) {
	switch {
	case errors.Is(err, ErrStaleCache):
		return "stale_session_cache", "the session cache is behind the journal; restart serve or run the rebuild"
	case errors.Is(err, ErrInvalidFilter):
		return "invalid_filter", err.Error()
	default:
		return "storage_fault", "the session store could not be read; inspect the serve log and repair the database"
	}
}

func (h *handler) ws(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "the session API has no service")
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			parsed, err := url.Parse(origin)
			return err == nil && loopbackHost(parsed.Host)
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{
		conn:    conn,
		service: h.service,
		send:    make(chan []byte, 1),
		subs:    make(map[wsTopic]*wsSubscription, 2),
		done:    make(chan struct{}),
	}
	defer func() {
		client.shutdown()
		_ = conn.Close()
	}()

	go client.writeLoop()
	client.sendJSON(wsReadyFrame{Type: "ready", Topics: []string{string(wsTopicSessions), string(wsTopicEpochs)}})

	incoming := make(chan wsClientMessage, 8)
	go readWSMessages(conn, incoming, client.done)

	ticker := time.NewTicker(wsRefreshInterval)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case message, ok := <-incoming:
			if !ok {
				return
			}
			if message.Type == "__invalid" {
				client.sendError("invalid_message", message.Err.Error())
				continue
			}
			switch message.Type {
			case "subscribe":
				if !message.Topic.valid() {
					client.sendError("invalid_topic", "topic must be sessions or epochs")
					continue
				}
				client.subscribe(ctx, message.Topic)
			case "unsubscribe":
				if !message.Topic.valid() {
					client.sendError("invalid_topic", "topic must be sessions or epochs")
					continue
				}
				delete(client.subs, message.Topic)
				client.sendJSON(wsAckFrame{Type: "unsubscribed", Topic: message.Topic})
			case "ping":
				client.sendJSON(wsAckFrame{Type: "pong"})
			default:
				client.sendError("invalid_message", "type must be subscribe, unsubscribe, or ping")
			}
		case <-ticker.C:
			for topic := range client.subs {
				client.refresh(ctx, topic)
			}
		case <-ctx.Done():
			return
		case <-client.done:
			return
		}
	}
}
