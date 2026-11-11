package satori

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

func TestSentMessageCanBeReadBackAndQuoted(t *testing.T) {
	for _, rawID := range []string{"12345", "987654321012345678", "satori-message-id"} {
		for _, kind := range []string{"group", "private", "group-forward", "private-forward"} {
			t.Run(rawID+"/"+kind, func(t *testing.T) {
				channelID := "123"
				if kind == "private" || kind == "private-forward" {
					channelID = "private-channel"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/message.create":
						json.NewEncoder(w).Encode([]map[string]interface{}{{"id": rawID}})
					case "/v1/message.get":
						var params map[string]string
						if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
							t.Error(err)
						}
						if params["message_id"] != rawID || params["channel_id"] != channelID {
							t.Errorf("incorrect message lookup: %v", params)
						}
						json.NewEncoder(w).Encode(map[string]interface{}{"id": rawID, "content": "sent content"})
					default:
						t.Errorf("unexpected request %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				a := NewSatoriAdapter(&adapter.AdapterConfig{WSAddr: server.URL, Timeout: time.Second})
				a.directChannelMap[456] = channelID
				message := []adapter.MessageSegment{{Type: "text", Data: map[string]interface{}{"text": "sent content"}}}
				var id int32
				var err error
				switch kind {
				case "group":
					id, err = a.SendGroupMessage(123, message)
				case "private":
					id, err = a.SendPrivateMessage(456, message)
				case "group-forward":
					id, _, err = a.SendGroupForwardMessage(123, nil, nil)
				case "private-forward":
					id, _, err = a.SendPrivateForwardMessage(456, nil, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.GetMsgOrg(id); err != nil {
					t.Fatal(err)
				}
				msg, err := a.GetMsg(id)
				if err != nil {
					t.Fatal(err)
				}
				if msg.RawMessage != "sent content" {
					t.Errorf("unexpected content %q", msg.RawMessage)
				}
				quote := a.renderMessageContent([]adapter.MessageSegment{{Type: "reply", Data: map[string]interface{}{"id": id}}})
				if want := `<quote id="` + rawID + `"/>`; quote != want {
					t.Errorf("quote = %q, want %q", quote, want)
				}
			})
		}
	}
}
