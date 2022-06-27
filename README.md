# Directus Client
> **WARN** Limited directus api spec support

```
GOPRIVATE=gitlab.enkuchat.com \
go get -u gitlab.enkuchat.com/backend/directus_client
```

## Features

- Webhook Server
- Cache, eviction based on TTL + Webhooks

## NOTE
 - Directus Webhooks has duplicate request bug, temporary solution already used [#13933](https://github.com/directus/directus/issues/13933)