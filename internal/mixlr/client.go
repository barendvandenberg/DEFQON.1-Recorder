package mixlr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Channel is the decoded state of a Mixlr channel.
type Channel struct {
	Username      string
	Live          bool // authoritative online flag from data.attributes.live
	StreamURL     string
	ListenerCount int
	ArtworkURL    string // channel artwork used as ID3 cover art
}

// Fetch queries a channel and returns its decoded state.
func (c *Client) Fetch(ctx context.Context, channel string) (Channel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+channel, nil)
	if err != nil {
		return Channel{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Channel{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Channel{}, fmt.Errorf("api status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Channel{}, err
	}

	var data channelViewResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return Channel{}, err
	}

	return data.channel(), nil
}

// channelViewResponse models the relevant subset of the Mixlr JSON:API payload.
// The "attributes" objects are open maps and the "included" array is polymorphic,
// so they are decoded loosely and accessed via typed helpers.
type channelViewResponse struct {
	Data     resource   `json:"data"`
	Included []resource `json:"included"`
}

type resource struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Attributes    map[string]any `json:"attributes"`
	Relationships relationships  `json:"relationships"`
}

type relationships struct {
	CurrentBroadcast *relation `json:"current_broadcast"`
}

type relation struct {
	Data *reference `json:"data"`
}

type reference struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (r *channelViewResponse) channel() Channel {
	ch := Channel{
		Username:   r.username(),
		Live:       r.Data.attrBool("live"),
		ArtworkURL: r.Data.attrString("artwork_url"),
	}
	if b := r.currentBroadcast(); b != nil {
		ch.StreamURL = b.attrString("progressive_stream_url")
		ch.ListenerCount = b.attrInt("listener_count")
	}
	return ch
}

func (r *channelViewResponse) username() string {
	if u, ok := r.Data.Attributes["username"].(string); ok {
		return u
	}
	return ""
}

func (r *channelViewResponse) currentBroadcast() *resource {
	rel := r.Data.Relationships.CurrentBroadcast
	if rel == nil || rel.Data == nil {
		return nil
	}
	id := rel.Data.ID
	for i := range r.Included {
		if r.Included[i].ID == id {
			return &r.Included[i]
		}
	}
	return nil
}

func (r *resource) attrBool(key string) bool {
	b, _ := r.Attributes[key].(bool)
	return b
}

func (r *resource) attrString(key string) string {
	s, _ := r.Attributes[key].(string)
	return s
}

func (r *resource) attrInt(key string) int {
	switch n := r.Attributes[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
