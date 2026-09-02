# NeMo Guardrails Reverse Proxy
 
Tiny reverse proxy to perform inbound and outbound payload processing for
NeMo Guardrails k8s deployments.

## Current Feature Set
### Username -> Guardrail Config Mapping
Given a config file (mounted to `/config/user-mapping/config-user-mapping.yaml`) that maps usernames to NeMo Guardrails Config IDs, e.g.:
```yaml
config_A:
- alice
- user-one@email.com
- service-account-one
config_B:
- bob
- user-two@email.com
...
```

The proxy intercepts each request, looks up the `X-Remote-User` header in the config mapping, and injects (or removes) `guardrails.config_id` before forwarding to the upstream NeMo Guardrails server:
```
Client              Kube RBAC Proxy               NeMo Reverse Proxy                 NeMo Guardrails
  |                       |                               |                                 |
  |-- POST /v1/...        |                               |                                 |
  |   Authorization:      |                               |                                 |
  |     Bearer <token> -->| 1) validate token             |                                 |
  |                       | 2) extract identity = alice   |                                 |
  |                       |                               |                                 |
  |                       |-- POST /v1/...                |                                 |
  |                       |   X-Remote-User: alice        |                                 |
  |                       |   {                           |                                 |
  |                       |     "model": "some-model"     |                                 |
  |                       |   } ------------------------> | look up user "alice" in mapping |
  |                       |                               | -> match to "config_A"          |
  |                       |                               |                                 |
  |                       |                               |-- POST /v1/...                  |  
  |                       |                               |   {                             |
  |                       |                               |     "model": "some-model",      |
  |                       |                               |     "guardrails": {             |
  |                       |                               |       "config_id":              |
  |                       |                               |         "config_A"              |
  |                       |                               |     }                           |
  |                       |                               |   } --------------------------> |
  |                       |                               |                                 |
  |                       |                               |                                 |
  | <-- response -------- | <-- response ---------------- | <-- response ------------------ |
  |                       |                               |                                 |
  |                       |                               |                                 |  
```

If the user is not found in the mapping, any existing `config_id` is stripped from the request before forwarding, enforcing that 
unmapped users fall back to the default configuration.

## Benchmarked Latency
~12ms per request

## Test
```shell
go test ./cmd
```

## Build
```shell
podman build -t $TAG .
```

Alternatively, a pre-built docker image lives at `quay.io/trustyai/nemo-guardrails-reverse-proxy:latest`
