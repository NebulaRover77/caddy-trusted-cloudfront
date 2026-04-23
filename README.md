[![Build Status](https://github.com/xcaddyplugins/caddy-trusted-cloudfront/workflows/update/badge.svg)](https://github.com/xcaddyplugins/caddy-trusted-cloudfront)
[![Licenses](https://img.shields.io/github/license/xcaddyplugins/caddy-trusted-cloudfront)](LICENSE)
[![donate](https://img.shields.io/badge/Donate-PayPal-green.svg)](https://www.buymeacoffee.com/illi)

# trusted_proxies modules for `Caddy`

This project now provides two trusted proxies modules:

- `cloudfront` (existing behavior): trusts `AWS CloudFront EDGE servers` from <https://d7uri8nf7uskq.cloudfront.net/tools/list-cloudfront-ips>
- `cloudfront_origin_facing` (new behavior): trusts CloudFront **origin-facing** addresses from AWS <https://ip-ranges.amazonaws.com/ip-ranges.json>

## Why `cloudfront_origin_facing` exists

The legacy CloudFront list endpoint is edge-focused and does not represent the CloudFront origin-facing ranges you typically need when trusting requests at origin.

The `cloudfront_origin_facing` module solves this by filtering AWS `ip-ranges.json` entries to only:

- `service == "CLOUDFRONT_ORIGIN_FACING"`
- `region == "GLOBAL"`

and supports both:

- `ip_prefix` (IPv4)
- `ipv6_prefix` (IPv6)

## Requirements

- [Go installed](https://golang.org/doc/install)
- [xcaddy](https://github.com/caddyserver/xcaddy)

## Install

> [!IMPORTANT]
> `cloudfront_origin_facing` is currently a fork-only feature until this work is merged upstream.
> Upstream releases at <https://github.com/xcaddyplugins/caddy-trusted-cloudfront/releases> may not include this module yet.

To test or use `cloudfront_origin_facing` right now, build Caddy with your fork module path using `xcaddy`.

## Build from source

Requirements:

- [Go installed](https://golang.org/doc/install)
- [xcaddy](https://github.com/caddyserver/xcaddy)

Build from upstream module path:

```bash
$ xcaddy build --with github.com/xcaddyplugins/caddy-trusted-cloudfront
```

Build from your fork (required while this feature is unmerged upstream):

```bash
$ xcaddy build --with github.com/<your-github-user>/caddy-trusted-cloudfront
```

## `Caddyfile` syntax

### Existing module: `cloudfront`

```caddyfile
trusted_proxies cloudfront {
	interval <duration>
}
```

- `interval` How often to fetch the latest IP list. format is [caddy.Duration](https://caddyserver.com/docs/conventions#durations). For example `12h` represents **12 hours**, and `1d` represents **one day**. default value `1d`.

### New module: `cloudfront_origin_facing`

```caddyfile
trusted_proxies cloudfront_origin_facing {
	interval <duration>
	ip_family dual_stack|ipv4|ipv6
}
```

- `interval` Same refresh interval behavior as `cloudfront` (default `1d`).
- `ip_family` Controls which AWS ranges are trusted:
  - `dual_stack` (default): include both `ip_prefix` and `ipv6_prefix`
  - `ipv4`: include only `ip_prefix`
  - `ipv6`: include only `ipv6_prefix`

## `Caddyfile` examples

### Use new module with defaults (`dual_stack`)

```caddyfile
trusted_proxies cloudfront_origin_facing
```

### New module with explicit `dual_stack`

```caddyfile
trusted_proxies cloudfront_origin_facing {
	interval 12h
	ip_family dual_stack
}
```

### New module IPv4 only

```caddyfile
trusted_proxies cloudfront_origin_facing {
	interval 12h
	ip_family ipv4
}
```

### New module IPv6 only

```caddyfile
trusted_proxies cloudfront_origin_facing {
	interval 12h
	ip_family ipv6
}
```

### Global trusted proxies example

```caddyfile
{
	servers {
		trusted_proxies cloudfront_origin_facing {
			interval 12h
			ip_family dual_stack
		}
	}
}
```
