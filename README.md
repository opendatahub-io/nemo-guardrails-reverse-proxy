# NeMo Guardrails Reverse Proxy
 
Tiny reverse proxy to perform inbound and outbound payload processing for
NeMo Guardrails k8s deployments.

## Current Feature Set
* Dynamic setting of `guardrail.config_id` values based on a defined `X-Remote-User` -> Guardrail Config ID mapping.

## Benchmarked Latency
~12ms per request

## Config File Structure
The config file should be a yaml file, keying config IDs to user names:
```yaml
config_A:
- user-one@email.com
- service-account-one
config_B:
- user-two@email.com
...
```

This file should be mounted to `/config/user-mapping/config-user-mapping.yaml`

## Test
```shell
go test ./cmd
```

## Build
```shell
podman build -t $TAG .
```