package caddy_trusted_cloudfront

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

const (
	cloudFrontEdgeFetchURL                 = "https://d7uri8nf7uskq.cloudfront.net/tools/list-cloudfront-ips"
	awsIPRangesFetchURL                    = "https://ip-ranges.amazonaws.com/ip-ranges.json"
	cloudFrontOriginFacingService          = "CLOUDFRONT_ORIGIN_FACING"
	globalRegion                           = "GLOBAL"
	ipFamilyDualStack             IPFamily = "dual_stack"
	ipFamilyIPv4                  IPFamily = "ipv4"
	ipFamilyIPv6                  IPFamily = "ipv6"
)

func init() {
	caddy.RegisterModule(CaddyTrustedCloudFront{})
	caddy.RegisterModule(CaddyTrustedCloudFrontOriginFacing{})
}

// The module that auto trusted_proxies `AWS CloudFront EDGE servers` from CloudFront.
// Doc: https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/LocationsOfEdgeServers.html
// Range from: https://d7uri8nf7uskq.cloudfront.net/tools/list-cloudfront-ips
type CaddyTrustedCloudFront struct {
	// Interval to update the trusted proxies list. default: 1d
	Interval caddy.Duration `json:"interval,omitempty"`
	ranges   []netip.Prefix
	ctx      caddy.Context
	lock     *sync.RWMutex
}

func (CaddyTrustedCloudFront) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.ip_sources.cloudfront",
		New: func() caddy.Module { return new(CaddyTrustedCloudFront) },
	}
}

func (s *CaddyTrustedCloudFront) Provision(ctx caddy.Context) error {
	s.ctx = ctx
	s.lock = new(sync.RWMutex)
	if s.Interval == 0 {
		s.Interval = caddy.Duration(24 * time.Hour) // default to 24 hours
	}
	if time.Duration(s.Interval) <= 0 {
		return fmt.Errorf("interval must be greater than 0")
	}

	// update cron
	go func() {
		ticker := time.NewTicker(time.Duration(s.Interval))
		s.lock.Lock()
		s.ranges, _ = s.fetchPrefixes()
		s.lock.Unlock()
		for {
			select {
			case <-ticker.C:
				prefixes, err := s.fetchPrefixes()
				if err != nil {
					break
				}
				s.lock.Lock()
				s.ranges = prefixes
				s.lock.Unlock()
			case <-s.ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()
	return nil
}

type CloudFrontIPSource struct {
	CloudFrontGlobalIPList       []string `json:"CLOUDFRONT_GLOBAL_IP_LIST"`
	CloudFrontRegionalEdgeIPList []string `json:"CLOUDFRONT_REGIONAL_EDGE_IP_LIST"`
}

func (s *CaddyTrustedCloudFront) fetchPrefixes() ([]netip.Prefix, error) {
	data, err := fetchJSON[CloudFrontIPSource](s.ctx, cloudFrontEdgeFetchURL)
	if err != nil {
		return nil, err
	}
	return parseCloudFrontEdgePrefixes(data)
}

func parseCloudFrontEdgePrefixes(data CloudFrontIPSource) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, v := range data.CloudFrontGlobalIPList {
		prefix, err := caddyhttp.CIDRExpressionToPrefix(v)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	for _, v := range data.CloudFrontRegionalEdgeIPList {
		prefix, err := caddyhttp.CIDRExpressionToPrefix(v)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func (s *CaddyTrustedCloudFront) GetIPRanges(_ *http.Request) []netip.Prefix {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.ranges
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler. Syntax:
//
//	cloudfront {
//	   interval <duration>
//	}
func (m *CaddyTrustedCloudFront) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // consume directive name

	if d.NextArg() {
		return d.ArgErr()
	}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "interval":
			if !d.NextArg() {
				return d.ArgErr()
			}
			val, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			if val <= 0 {
				return fmt.Errorf("interval must be greater than 0")
			}
			m.Interval = caddy.Duration(val)
		default:
			return d.ArgErr()
		}
	}

	return nil
}

// CaddyTrustedCloudFrontOriginFacing trusts CloudFront origin-facing proxies from AWS ip-ranges.json.
// It uses only service=CLOUDFRONT_ORIGIN_FACING and region=GLOBAL entries.
type CaddyTrustedCloudFrontOriginFacing struct {
	// Interval to update the trusted proxies list. default: 1d
	Interval caddy.Duration `json:"interval,omitempty"`
	// IPFamily controls which address family is loaded: dual_stack (default), ipv4, ipv6.
	IPFamily IPFamily `json:"ip_family,omitempty"`
	ranges   []netip.Prefix
	ctx      caddy.Context
	lock     *sync.RWMutex
}

func (CaddyTrustedCloudFrontOriginFacing) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.ip_sources.cloudfront_origin_facing",
		New: func() caddy.Module { return new(CaddyTrustedCloudFrontOriginFacing) },
	}
}

func (s *CaddyTrustedCloudFrontOriginFacing) Provision(ctx caddy.Context) error {
	s.ctx = ctx
	s.lock = new(sync.RWMutex)
	if s.Interval == 0 {
		s.Interval = caddy.Duration(24 * time.Hour)
	}
	if time.Duration(s.Interval) <= 0 {
		return fmt.Errorf("interval must be greater than 0")
	}
	if s.IPFamily == "" {
		s.IPFamily = ipFamilyDualStack
	}
	if !s.IPFamily.Valid() {
		return fmt.Errorf("unsupported ip_family %q", s.IPFamily)
	}

	go func() {
		ticker := time.NewTicker(time.Duration(s.Interval))
		s.lock.Lock()
		s.ranges, _ = s.fetchPrefixes()
		s.lock.Unlock()
		for {
			select {
			case <-ticker.C:
				prefixes, err := s.fetchPrefixes()
				if err != nil {
					break
				}
				s.lock.Lock()
				s.ranges = prefixes
				s.lock.Unlock()
			case <-s.ctx.Done():
				ticker.Stop()
				return
			}
		}
	}()

	return nil
}

func (s *CaddyTrustedCloudFrontOriginFacing) fetchPrefixes() ([]netip.Prefix, error) {
	data, err := fetchJSON[AWSIPRanges](s.ctx, awsIPRangesFetchURL)
	if err != nil {
		return nil, err
	}
	return parseCloudFrontOriginFacingPrefixes(data, s.IPFamily)
}

func (s *CaddyTrustedCloudFrontOriginFacing) GetIPRanges(_ *http.Request) []netip.Prefix {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.ranges
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler. Syntax:
//
//	cloudfront_origin_facing {
//	   interval <duration>
//	   ip_family dual_stack|ipv4|ipv6
//	}
func (m *CaddyTrustedCloudFrontOriginFacing) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()

	if d.NextArg() {
		return d.ArgErr()
	}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "interval":
			if !d.NextArg() {
				return d.ArgErr()
			}
			val, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			if val <= 0 {
				return fmt.Errorf("interval must be greater than 0")
			}
			m.Interval = caddy.Duration(val)
		case "ip_family":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.IPFamily = IPFamily(d.Val())
			if !m.IPFamily.Valid() {
				return fmt.Errorf("unsupported ip_family %q", m.IPFamily)
			}
		default:
			return d.ArgErr()
		}
	}

	return nil
}

type IPFamily string

func (f IPFamily) Valid() bool {
	switch f {
	case ipFamilyDualStack, ipFamilyIPv4, ipFamilyIPv6:
		return true
	default:
		return false
	}
}

type AWSIPRanges struct {
	Prefixes     []AWSIPv4Prefix `json:"prefixes"`
	IPv6Prefixes []AWSIPv6Prefix `json:"ipv6_prefixes"`
}

type AWSIPv4Prefix struct {
	IPPrefix string `json:"ip_prefix"`
	Region   string `json:"region"`
	Service  string `json:"service"`
}

type AWSIPv6Prefix struct {
	IPv6Prefix string `json:"ipv6_prefix"`
	Region     string `json:"region"`
	Service    string `json:"service"`
}

func parseCloudFrontOriginFacingPrefixes(data AWSIPRanges, family IPFamily) ([]netip.Prefix, error) {
	if family == "" {
		family = ipFamilyDualStack
	}
	if !family.Valid() {
		return nil, fmt.Errorf("unsupported ip_family %q", family)
	}

	prefixes := make([]netip.Prefix, 0, len(data.Prefixes)+len(data.IPv6Prefixes))
	if family == ipFamilyDualStack || family == ipFamilyIPv4 {
		for _, v := range data.Prefixes {
			if v.Service != cloudFrontOriginFacingService || v.Region != globalRegion {
				continue
			}
			prefix, err := caddyhttp.CIDRExpressionToPrefix(v.IPPrefix)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, prefix)
		}
	}

	if family == ipFamilyDualStack || family == ipFamilyIPv6 {
		for _, v := range data.IPv6Prefixes {
			if v.Service != cloudFrontOriginFacingService || v.Region != globalRegion {
				continue
			}
			prefix, err := caddyhttp.CIDRExpressionToPrefix(v.IPv6Prefix)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, prefix)
		}
	}

	return prefixes, nil
}

func fetchJSON[T any](ctx context.Context, url string) (T, error) {
	var data T
	fetchCtx, cancel := context.WithTimeout(ctx, time.Duration(time.Minute))
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return data, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return data, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return data, err
	}
	return data, nil
}

// Interface guards
var (
	_ caddy.Module            = (*CaddyTrustedCloudFront)(nil)
	_ caddy.Provisioner       = (*CaddyTrustedCloudFront)(nil)
	_ caddyfile.Unmarshaler   = (*CaddyTrustedCloudFront)(nil)
	_ caddyhttp.IPRangeSource = (*CaddyTrustedCloudFront)(nil)

	_ caddy.Module            = (*CaddyTrustedCloudFrontOriginFacing)(nil)
	_ caddy.Provisioner       = (*CaddyTrustedCloudFrontOriginFacing)(nil)
	_ caddyfile.Unmarshaler   = (*CaddyTrustedCloudFrontOriginFacing)(nil)
	_ caddyhttp.IPRangeSource = (*CaddyTrustedCloudFrontOriginFacing)(nil)
)
