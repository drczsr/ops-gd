package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSend_PostsTextWithMentions(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	bot := NewWeworkBot(srv.URL)
	if err := bot.Send("hello", []string{"zhangsan", "", "zhangsan", "lisi"}); err != nil {
		t.Fatalf("Send err: %v", err)
	}
	if got["msgtype"] != "text" {
		t.Fatalf("msgtype = %v, want text", got["msgtype"])
	}
	text := got["text"].(map[string]any)
	if text["content"] != "hello" {
		t.Fatalf("content = %v", text["content"])
	}
	ml := text["mentioned_list"].([]any)
	if len(ml) != 2 || ml[0] != "zhangsan" || ml[1] != "lisi" {
		t.Fatalf("mentioned_list = %v, want dedup [zhangsan lisi]", ml)
	}
}

func TestSend_ErrcodeNonZeroIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook"}`))
	}))
	defer srv.Close()
	if err := NewWeworkBot(srv.URL).Send("x", nil); err == nil {
		t.Fatal("expected error on errcode!=0")
	}
}
