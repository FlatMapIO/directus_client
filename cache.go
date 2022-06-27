package directus_client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/cespare/xxhash/v2"
	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CacheService interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Del(key string) error
	Clear() error
}

type redisCacheService struct {
	r        redis.UniversalClient
	keyspace string
	timeout  time.Duration
	ttl      time.Duration
}

var _ CacheService = (*redisCacheService)(nil)

type RedisCacheServiceOption struct {
	Keyspace    string
	ExecTimeout time.Duration
	CacheTTL    time.Duration
}

func (r *RedisCacheServiceOption) applyDefault() {
	if r.Keyspace == "" {
		r.Keyspace = "directus"
	}
	if r.ExecTimeout == 0 {
		r.ExecTimeout = time.Second * 5
	}
	if r.CacheTTL == 0 {
		r.CacheTTL = time.Minute * 10
	}
}
func NewRedisCacheService(r redis.UniversalClient, option RedisCacheServiceOption) (CacheService, error) {
	option.applyDefault()
	cs := redisCacheService{r, option.Keyspace, option.ExecTimeout, option.CacheTTL}
	return &cs, nil
}
func (r redisCacheService) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.r.Get(ctx, r.keyspace+":"+key).Bytes()
}
func (r redisCacheService) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.r.Set(ctx, r.keyspace+":"+key, value, r.ttl).Err()
}
func (r redisCacheService) Del(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.r.Del(ctx, r.keyspace+":"+key).Err()
}
func (r redisCacheService) Clear() error {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	_, err := r.r.Pipelined(ctx, func(p redis.Pipeliner) error {
		keys, err := p.Keys(ctx, r.keyspace+":*").Result()
		if err != nil {
			return err
		}
		return p.Del(ctx, keys...).Err()
	})
	return err
}

type QueryCache interface {
	Get(collection, rawQuery string) (io.ReadCloser, error)
	Set(collection, rawQuery string, value io.Reader) ([]byte, error)
}
type noopCacheService int

func (noopCacheService) Get(collection, rawQuery string) (io.ReadCloser, error) {
	return nil, errors.New("get item from noop cache")
}
func (noopCacheService) Set(collection, rawQuery string, value io.Reader) ([]byte, error) {
	return io.ReadAll(value)
}

type refreshableQueryCache struct {
	mu                  sync.RWMutex
	store               CacheService
	observedCollections map[string]struct{}
	wes                 *WebhookEventServer
}

func NewNoopQueryCache() QueryCache {
	return noopCacheService(0)
}
func NewRefreshableQueryCache(store CacheService, wes *WebhookEventServer) (QueryCache, error) {
	r := &refreshableQueryCache{
		store:               store,
		observedCollections: make(map[string]struct{}),
		wes:                 wes,
	}
	return r, nil
}

func queryKey(c string, q string) string {
	split := strings.Split(c, "/")
	if len(split) == 2 {
		c = split[0]
		q = fmt.Sprintf(`{"id": {"_eq": %s}}`, split[1])
	}
	h := xxhash.New()
	h.Write([]byte(q))

	return c + ":" + strconv.FormatUint(h.Sum64(), 16)
}
func (q *refreshableQueryCache) Get(collection string, rawQuery string) (io.ReadCloser, error) {
	key := queryKey(collection, rawQuery)
	data, err := q.store.Get(key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
func (q *refreshableQueryCache) Set(collection string, rawQuery string, data io.Reader) ([]byte, error) {

	b, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	if err := q.store.Set(queryKey(collection, rawQuery), b); err != nil {
		return nil, err
	}

	q.mu.Lock()
	if _, ok := q.observedCollections[collection]; ok {
		return b, nil
	}
	q.observedCollections[collection] = struct{}{}
	q.mu.Unlock()

	err = q.wes.AddObserver(collection, func(we WebhookEvent) {
		q.pruneCollection(collection)
	})
	if err != nil {
		log.Warn().Str("collection", collection).Msg("failed to add observer")
	}

	return b, nil
}
func (q *refreshableQueryCache) pruneCollection(c string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.observedCollections, c)
	return q.store.Del(c + ":" + "*")
}