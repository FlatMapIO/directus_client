package directus_client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rs/zerolog/log"
	"net"
	"net/http"
	"sync"
	"time"
)

type WebhookEvent struct {
	Event string `json:"event"`
	// Accountability WebhookEventAccountability `json:"-"`
	Payload    json.RawMessage `json:"payload"`
	Key        string          `json:"key"`
	Collection string          `json:"collection"`
}

type WebhookEventServer struct {
	mu  sync.RWMutex
	svr *http.Server
	// map of collection name to function
	observes map[string]func(WebhookEvent)
}

func NewWebhookEventServer(addr string, path string) (*WebhookEventServer, error) {
	s := &WebhookEventServer{
		observes: make(map[string]func(WebhookEvent)),
	}
	if err := s.serve(addr, path); err != nil {
		return nil, err
	}
	return s, nil
}

func (wes *WebhookEventServer) AddObserver(collection string, f func(WebhookEvent)) error {
	wes.mu.Lock()
	defer wes.mu.Unlock()
	if _, ok := wes.observes[collection]; ok {
		return errors.New("collection already exists")
	}
	wes.observes[collection] = f
	return nil
}
func (wes *WebhookEventServer) RemoveObserver(collection string) {
	wes.mu.RLock()
	delete(wes.observes, collection)
}

func (wes *WebhookEventServer) serve(addr string, path string) error {
	mux := http.NewServeMux()

	// NOTE begin directus bug, should be fixed in next release
	done := make(chan struct{})

	fluxInput, fluxOutput := makeTimedBufferTransferChan[*WebhookEvent](time.Second, done)
	go func() {
		for {
			select {
			case <-done:
				return
			case ex := <-fluxOutput:
				uniqueEvents := make(map[string]*WebhookEvent)
				for _, e := range ex {
					uniqueEvents[e.Collection+":"+e.Event+":"+e.Key] = e
				}
				wes.mu.RLock()
				for _, e := range uniqueEvents {
					if f, ok := wes.observes[e.Collection]; ok {
						f(*e)
					}
					if f, ok := wes.observes["*"]; ok {
						f(*e)
					}
				}
				wes.mu.RUnlock()
			}
		}
	}()
	// end of directus bug

	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "content type must be application/json", http.StatusBadRequest)
			return
		}

		we := new(WebhookEvent)

		if err := json.NewDecoder(r.Body).Decode(we); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		fluxInput <- we

		w.WriteHeader(http.StatusOK)
	}))
	wes.svr = &http.Server{Addr: addr, Handler: mux}

	listen, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Logger.Info().Msg("webhook server listening on " + addr + path)

	wes.svr.RegisterOnShutdown(func() {
		done <- struct{}{}
	})
	go wes.svr.Serve(listen)

	return nil
}
func (wes *WebhookEventServer) Shutdown() error {
	return wes.svr.Shutdown(context.Background())
}

func makeTimedBufferTransferChan[T any](duration time.Duration, done <-chan struct{}) (in chan<- T, out <-chan []T) {
	inChan := make(chan T, 4)
	outChan := make(chan []T, 4)
	go func() {
		buf := make([]T, 0, 2)
		for {
			select {
			case <-done:
				close(inChan)
				close(outChan)
				return
			case e := <-inChan:
				buf = append(buf, e)
			case <-time.Tick(duration):
				if len(buf) > 0 {
					outChan <- buf
					buf = make([]T, 0, 2)
				}
			}
		}
	}()
	return inChan, outChan
}