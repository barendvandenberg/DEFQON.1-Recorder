package mixlr

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChannelParsesArtworkURL(t *testing.T) {
	// Minimal payload matching the shape of the Mixlr JSON:API response.
	payload := `{
		"data": {
			"id": "171669",
			"type": "channel_view",
			"attributes": {
				"username": "UV",
				"live": true,
				"artwork_url": "https://mixlr.com/rails/active_storage/blobs/proxy/abc/cover.jpg"
			},
			"relationships": {
				"current_broadcast": {
					"data": { "id": "42", "type": "broadcast" }
				}
			}
		},
		"included": [
			{
				"id": "42",
				"type": "broadcast",
				"attributes": {
					"progressive_stream_url": "https://listen.mixlr.com/live",
					"listener_count": 123
				}
			}
		]
	}`

	var resp channelViewResponse
	if err := json.NewDecoder(strings.NewReader(payload)).Decode(&resp); err != nil {
		t.Fatalf("decode: %s", err)
	}

	ch := resp.channel()
	if ch.Username != "UV" || !ch.Live {
		t.Fatalf("unexpected basics: %+v", ch)
	}
	if ch.ArtworkURL == "" || !strings.HasSuffix(ch.ArtworkURL, "cover.jpg") {
		t.Fatalf("artwork not parsed: %q", ch.ArtworkURL)
	}
	if ch.StreamURL != "https://listen.mixlr.com/live" || ch.ListenerCount != 123 {
		t.Fatalf("broadcast not parsed: %+v", ch)
	}
}
