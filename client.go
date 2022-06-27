package directus_client

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const ITEMS_MAX_LIMIT = 1000

type DirectusClient struct {
	client  *http.Client
	baseURL *url.URL
	token   string
	cache   QueryCache
}

type DirectusResult[T any] struct {
	Meta   *MetaResult     `json:"meta,omitempty"`
	Data   T               `json:"data"`
	Errors []DirectusError `json:"errors,omitempty"`
}

func (d *DirectusResult[T]) Err() bool {
	return len(d.Errors) > 0
}
func ReadResult[T any](r *http.Response) DirectusResult[T] {
	if r.StatusCode != http.StatusOK {
		return DirectusResult[T]{
			Errors: []DirectusError{
				{
					Message: r.Status,
				},
			},
		}
	}

	var result DirectusResult[T]
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		return DirectusResult[T]{Errors: []DirectusError{{Message: err.Error()}}}
	}
	return result
}
func NewDirectusClient(baseURL string, token string, cache QueryCache) (*DirectusClient, error) {
	if token == "" {
		return nil, errors.New("token is required")
	}
	if strings.HasSuffix(baseURL, "/") {
		baseURL = baseURL[:len(baseURL)-1]
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	return &DirectusClient{
		client: &http.Client{
			Timeout: time.Second * 10,
		},
		baseURL: u,
		token:   token,
		cache:   cache,
	}, nil
}

func (d *DirectusClient) Call(r *http.Request) (*http.Response, error) {
	switch r.Method {
	case "GET", "POST", "PATCH", "DELETE":
		break
	default:
		return nil, errors.New("invalid method")
	}
	if r.URL == nil {
		return nil, errors.New("url is required")
	}
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.Header.Set("Authorization", "Bearer "+d.token)
	r.Header.Set("Content-Type", "application/json")
	r.RequestURI = ""
	r.URL.Scheme = d.baseURL.Scheme
	r.URL.Host = d.baseURL.Host
	r.Host = d.baseURL.Host
	if r.URL.RawQuery == "" {
		r.URL.RawQuery = "limit=" + strconv.Itoa(ITEMS_MAX_LIMIT)
	}

	split := strings.SplitN(r.URL.Path, "items/", 2)
	if len(split) != 2 {
		return nil, errors.New("invalid url")
	}
	collection := split[1]

	data, err := d.cache.Get(collection, r.URL.RawQuery)
	if err == nil {
		log.Warn().Err(err).Msg("cache hit")
		return &http.Response{StatusCode: http.StatusOK, Body: data}, nil
	}
	resp, err := d.client.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		data, err := d.cache.Set(collection, r.URL.RawQuery, resp.Body)
		if err != nil {
			log.Warn().Err(err).Str("path", r.URL.Path).Msg("failed to set cache")
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
	}

	return resp, nil
}

func (d *DirectusClient) Proxy(stripN int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.SplitN(r.URL.Path, "/", stripN+2)
		if len(p) == stripN+2 {
			r.URL.Path = "/" + p[len(p)-1]
		}
		resp, err := d.Call(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(resp.StatusCode)
		for k, v := range resp.Header {
			for _, v := range v {
				w.Header().Add(k, v)
			}
		}
		io.Copy(w, resp.Body)
	})
}

func (d *DirectusClient) Query(method string, collection string, query DirectusQuery, input io.Reader) (*http.Response, error) {
	if err := query.validate(); err != nil {
		return nil, err
	}
	v, err := query.BuildQuery()
	if err != nil {
		return nil, err
	}
	u := new(url.URL)
	*u = *d.baseURL
	u.Path = "/items/" + collection
	u.RawQuery = v.Encode()
	r := &http.Request{Method: method, URL: u}
	if input != nil {
		r.Body = io.NopCloser(input)
	}
	return d.Call(r)
}

type DirectusError struct {
	Message string `json:"message"`
}
type Fields []string
type MetaField string

const (
	MetaQueryAll         MetaField = "*"
	MetaQueryTotalCount  MetaField = "total_count"
	MetaQueryFilterCount MetaField = "filter_count"
)

func (m *MetaField) Unmarshal(s string) error {
	switch s {
	case string(MetaQueryAll):
		*m = MetaQueryAll
	case string(MetaQueryTotalCount):
		*m = MetaQueryTotalCount
	case string(MetaQueryFilterCount):
		*m = MetaQueryFilterCount
	default:
		return errors.New("invalid meta field")
	}
	return nil
}

type MetaResult struct {
	Meta struct {
		TotalCount  *int `json:"total_count"`
		FilterCount *int `json:"filter_count"`
	}
}

type DirectusQuery struct {
	Fields      Fields
	Filter      Filter
	Sort        Fields
	Limit       int
	Offset      int
	offsetIsSet bool
	Page        int
	pageIsSet   bool
	Meta        *MetaField
}

func (d *DirectusQuery) validate() error {
	if d.Limit > ITEMS_MAX_LIMIT {
		return errors.New("limit must be less than 100")
	} else {
		if d.Filter == nil {
			return errors.New("limit must be set if filter is not set")
		}
	}
	if d.offsetIsSet && d.pageIsSet {
		return errors.New("cannot specify both offset and page")
	}
	return nil
}

func ParseQuery(q url.Values) (*DirectusQuery, error) {
	var d DirectusQuery
	fields := q.Get("fields")
	if fields != "" {
		d.Fields = strings.Split(fields, ",")
	}
	filter := q.Get("filter")
	if filter != "" {
		if err := json.Unmarshal([]byte(filter), &d.Filter); err != nil {
			return nil, err
		}
	}
	sort := q.Get("sort")
	if sort != "" {
		d.Sort = strings.Split(sort, ",")
	}

	limit := q.Get("limit")
	if limit != "" {
		i, err := strconv.Atoi(limit)
		if err != nil {
			return nil, err
		}
		d.Limit = i
	} else {
		d.Limit = ITEMS_MAX_LIMIT
	}
	offset := q.Get("offset")
	if offset != "" {
		i, err := strconv.Atoi(offset)
		if err != nil {
			return nil, err
		}
		d.Offset = i
		d.offsetIsSet = true
	}
	page := q.Get("page")
	if page != "" {
		i, err := strconv.Atoi(page)
		if err != nil {
			return nil, err
		}
		d.Page = i
		d.pageIsSet = true
	}
	if meta := q.Get("meta"); meta != "" {
		var metaField MetaField
		if err := metaField.Unmarshal(meta); err != nil {
			return nil, err
		}
		d.Meta = &metaField
	}

	if err := d.validate(); err != nil {
		return nil, err
	}

	return &d, nil
}
func (d *DirectusQuery) BuildQuery() (url.Values, error) {
	v := url.Values{}
	if len(d.Fields) > 0 {
		v.Set("fields", strings.Join(d.Fields, ","))
	}
	if d.Filter != nil {
		b, err := json.Marshal(d.Filter)
		if err != nil {
			return nil, err
		}
		v.Set("filter", string(b))
	}
	if len(d.Sort) > 0 {
		v.Set("sort", strings.Join(d.Sort, ","))
	}
	if d.Limit == 0 {
		d.Limit = ITEMS_MAX_LIMIT
	}
	v.Set("limit", strconv.Itoa(d.Limit))
	if d.offsetIsSet {
		v.Set("offset", strconv.Itoa(d.Offset))
	}
	if d.pageIsSet {
		v.Set("page", strconv.Itoa(d.Page))
	}
	if d.Meta != nil {
		v.Set("meta", string(*d.Meta))
	}
	return v, nil
}

type DirectusQueryRewriter func(*DirectusQuery) *DirectusQuery

type FilterOperator string

const (
	OP_eq           FilterOperator = "_eq"
	OP_neq          FilterOperator = "_neq"
	OP_lt           FilterOperator = "_lt"
	OP_lte          FilterOperator = "_lte"
	OP_gt           FilterOperator = "_gt"
	OP_gte          FilterOperator = "_gte"
	OP_in           FilterOperator = "_in"
	OP_nin          FilterOperator = "_nin"
	OP_null         FilterOperator = "_null"
	OP_nnull        FilterOperator = "_nnull"
	OP_contains     FilterOperator = "_contains"
	OP_ncontains    FilterOperator = "_ncontains"
	OP_starts_with  FilterOperator = "_starts_with"
	OP_nstarts_with FilterOperator = "_nstarts_with"
	OP_ends_with    FilterOperator = "_ends_with"
	OP_nends_with   FilterOperator = "_nends_with"
	OP_between      FilterOperator = "_between"
	OP_nbetween     FilterOperator = "_nbetween"
	OP_empty        FilterOperator = "_empty"
	OP_nempty       FilterOperator = "_nempty"
)

type Filter map[string]map[FilterOperator]any