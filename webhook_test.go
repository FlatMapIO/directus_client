package directus_client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestWebhook(t *testing.T) {
	wes, err := NewWebhookEventServer(":8080", "/webhook")
	if err != nil {
		t.Error(err)
	}

	wes.AddObserver("*", func(we WebhookEvent) {
		data, _ := json.MarshalIndent(&we, "", "  ")
		t.Logf("user event: %s", data)
	})

	for i := 5; i > 0; i-- {
		t.Logf("%d", i)
		time.Sleep(time.Second)
		payload := []byte(`{
          "event": "",
          "payload": null,
          "key": "",
          "collection": "random"
        }`)
		http.Post("http://localhost:8080/webhook", "application/json", io.NopCloser(bytes.NewReader(payload)))
	}
}