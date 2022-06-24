package directus_client

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func createClient(t *testing.T) *DirectusClient {
	_url := os.Getenv("DIRECTUS_URL")
	token := os.Getenv("DIRECTUS_TOKEN")
	client, err := NewDirectusClient(_url, token, NewNoopQueryCache())
	require.NoError(t, err)
	return client
}

func TestDo(t *testing.T) {
	client := createClient(t)

	resp, err := client.Query("GET", "user", DirectusQuery{
		Fields: Fields{"id", "email"},
		Filter: Filter{
			"email": {
				OP_eq: "dev@cloudswan.io",
			},
		},
		// Sort: Fields{"id", "-email"},
	}, nil)

	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	t.Logf("%s\n", body)

	u, _ := url.Parse("https://localhost:8050/items/user?limit=100")
	resp, err = client.Call(&http.Request{
		Method: "GET",
		URL:    u,
	})
	data, _ := io.ReadAll(resp.Body)
	t.Logf("%s\n", data)
}

func createDirectusClient() (*DirectusClient, error) {
	var (
		token      = os.Getenv("DIRECTUS_TOKEN")
		redisAddrs = []string{
			"localhost:6379",
		}
		redisDB          = 8
		baseUrl          = "https://localhost:8050"
		redisKeyspace    = "directus"
		redisExecTimeout = time.Minute
		redisCacheTTL    = time.Minute * 10
		webhookAddr      = "127.0.0.1:9999"
		webhookPath      = "/webhook"
	)

	wes, err := NewWebhookEventServer(webhookAddr, webhookPath)
	if err != nil {
		return nil, err
	}

	universalClient := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: redisAddrs,
		DB:    redisDB,
	})

	cs, err := NewRedisCacheService(universalClient, RedisCacheServiceOption{
		Keyspace:    redisKeyspace,
		ExecTimeout: redisExecTimeout,
		CacheTTL:    redisCacheTTL,
	})
	if err != nil {
		return nil, err
	}
	cache, err := NewRefreshableQueryCache(cs, wes)
	if err != nil {
		return nil, err
	}
	return NewDirectusClient(baseUrl, token, cache)
}
func TestCache(t *testing.T) {
	client, err := createDirectusClient()
	require.NoError(t, err)

	resp, err := client.Query("GET", "user", DirectusQuery{
		Fields: Fields{"id", "email"},
		Filter: Filter{
			"email": {
				OP_eq: "dev@dev.io",
			},
		},
		// Sort: Fields{"id", "-email"},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
}

func Benchmark(b *testing.B) {
	client, err := createDirectusClient()
	require.NoError(b, err)

	router := chi.NewRouter()
	router.Handle("/forward/aaafff/*", client.Proxy(2))

	http.ListenAndServe(":9090", router)
}