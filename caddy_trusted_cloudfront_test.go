package caddy_trusted_cloudfront

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetch(t *testing.T) {
	c := &CaddyTrustedCloudFront{
		ctx: caddy.Context{
			Context: context.TODO(),
		},
		lock: new(sync.RWMutex),
	}
	prefixes, err := c.fetchPrefixes()
	if err != nil {
		t.Skipf("skipping network fetch test due to fetch error: %v", err)
	}
	assert.True(t, len(prefixes) > 0, "prefixes is empty")
}

func TestSyntax(t *testing.T) {
	err := testSyntax(`cloudfront`)
	assert.Nil(t, err, err)
	err = testSyntax(`cloudfront {
		interval 12h
		url https://d7uri8nf7uskq.cloudfront.net/tools/list-cloudfront-ips
	}`)
	assert.Nil(t, err, err)
	err = testSyntax(`cloudfront {
		url not-a-url
	}`)
	assert.NotNil(t, err, "invalid url should be invalid")
	err = testSyntax(`cloudfront {
		interval 0.8h
		invalid_name 100
	}`)
	assert.NotNil(t, err, "invalid_name should be invalid")
	err = testSyntax(`cloudfront {
		interval 0s
	}`)
	assert.NotNil(t, err, "interval 0s should be invalid")
	err = testSyntax(`cloudfront {
		interval -1h
	}`)
	assert.NotNil(t, err, "negative interval should be invalid")
}

func TestOriginFacingSyntax(t *testing.T) {
	err := testOriginFacingSyntax(`cloudfront_origin_facing`)
	assert.Nil(t, err, err)
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		interval 12h
		ip_family dual_stack
		url https://ip-ranges.amazonaws.com/ip-ranges.json
	}`)
	assert.Nil(t, err, err)
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		ip_family ipv4
	}`)
	assert.Nil(t, err, err)
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		ip_family ipv6
	}`)
	assert.Nil(t, err, err)
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		ip_family invalid
	}`)
	assert.NotNil(t, err, "invalid ip_family should be invalid")
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		url not-a-url
	}`)
	assert.NotNil(t, err, "invalid url should be invalid")
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		interval 0s
	}`)
	assert.NotNil(t, err, "interval 0s should be invalid")
	err = testOriginFacingSyntax(`cloudfront_origin_facing {
		interval -1h
	}`)
	assert.NotNil(t, err, "negative interval should be invalid")
}

func TestProvisionRejectsNonPositiveIntervalProgrammaticConfig(t *testing.T) {
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.TODO()})
	defer cancel()

	cf := &CaddyTrustedCloudFront{Interval: caddy.Duration(-1 * time.Hour)}
	err := cf.Provision(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval must be greater than 0")

	originFacing := &CaddyTrustedCloudFrontOriginFacing{Interval: caddy.Duration(-1 * time.Hour)}
	err = originFacing.Provision(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval must be greater than 0")
}

func TestParseAWSIPRangesJSON(t *testing.T) {
	payload := `{
	  "syncToken": "1",
	  "createDate": "2026-04-23-00-00-00",
	  "prefixes": [
	    {"ip_prefix": "203.0.113.0/24", "region": "GLOBAL", "service": "CLOUDFRONT_ORIGIN_FACING"},
	    {"ip_prefix": "198.51.100.0/24", "region": "us-east-1", "service": "CLOUDFRONT_ORIGIN_FACING"}
	  ],
	  "ipv6_prefixes": [
	    {"ipv6_prefix": "2001:db8::/48", "region": "GLOBAL", "service": "CLOUDFRONT_ORIGIN_FACING"}
	  ]
	}`

	var data AWSIPRanges
	err := jsonDecodeString(payload, &data)
	require.NoError(t, err)
	require.Len(t, data.Prefixes, 2)
	require.Len(t, data.IPv6Prefixes, 1)
}

func TestParseCloudFrontOriginFacingPrefixes_FilteringAndFamilies(t *testing.T) {
	data := AWSIPRanges{
		Prefixes: []AWSIPv4Prefix{
			{IPPrefix: "203.0.113.0/24", Region: "GLOBAL", Service: "CLOUDFRONT_ORIGIN_FACING"},
			{IPPrefix: "198.51.100.0/24", Region: "us-east-1", Service: "CLOUDFRONT_ORIGIN_FACING"},
			{IPPrefix: "192.0.2.0/24", Region: "GLOBAL", Service: "AMAZON"},
		},
		IPv6Prefixes: []AWSIPv6Prefix{
			{IPv6Prefix: "2001:db8::/48", Region: "GLOBAL", Service: "CLOUDFRONT_ORIGIN_FACING"},
			{IPv6Prefix: "2001:db8:1::/48", Region: "eu-west-1", Service: "CLOUDFRONT_ORIGIN_FACING"},
			{IPv6Prefix: "2001:db8:2::/48", Region: "GLOBAL", Service: "S3"},
		},
	}

	ipv4Only, err := parseCloudFrontOriginFacingPrefixes(data, ipFamilyIPv4)
	require.NoError(t, err)
	require.Len(t, ipv4Only, 1)
	assert.True(t, ipv4Only[0].Addr().Is4())

	ipv6Only, err := parseCloudFrontOriginFacingPrefixes(data, ipFamilyIPv6)
	require.NoError(t, err)
	require.Len(t, ipv6Only, 1)
	assert.True(t, ipv6Only[0].Addr().Is6())

	dualStack, err := parseCloudFrontOriginFacingPrefixes(data, ipFamilyDualStack)
	require.NoError(t, err)
	require.Len(t, dualStack, 2)
}

func TestParseCloudFrontOriginFacingPrefixes_RealWorldIPv6ContainsAddress(t *testing.T) {
	data := AWSIPRanges{
		IPv6Prefixes: []AWSIPv6Prefix{
			{IPv6Prefix: "2600:9000:1000::/52", Region: "GLOBAL", Service: "CLOUDFRONT_ORIGIN_FACING"},
		},
	}

	prefixes, err := parseCloudFrontOriginFacingPrefixes(data, ipFamilyIPv6)
	require.NoError(t, err)
	require.Len(t, prefixes, 1)

	addr := netip.MustParseAddr("2600:9000:1000::1")
	assert.True(t, prefixes[0].Contains(addr), "expected parsed origin-facing prefix to contain representative IPv6 address")
}

func TestParseCloudFrontOriginFacingPrefixes_MissingFamilyInPayload(t *testing.T) {
	ipv4Data := AWSIPRanges{
		Prefixes: []AWSIPv4Prefix{{IPPrefix: "203.0.113.0/24", Region: "GLOBAL", Service: "CLOUDFRONT_ORIGIN_FACING"}},
	}
	ipv4Only, err := parseCloudFrontOriginFacingPrefixes(ipv4Data, ipFamilyIPv4)
	require.NoError(t, err)
	require.Len(t, ipv4Only, 1)

	ipv6OnlyFromIPv4Data, err := parseCloudFrontOriginFacingPrefixes(ipv4Data, ipFamilyIPv6)
	require.NoError(t, err)
	require.Len(t, ipv6OnlyFromIPv4Data, 0)

	ipv6Data := AWSIPRanges{
		IPv6Prefixes: []AWSIPv6Prefix{{IPv6Prefix: "2001:db8::/48", Region: "GLOBAL", Service: "CLOUDFRONT_ORIGIN_FACING"}},
	}
	ipv6Only, err := parseCloudFrontOriginFacingPrefixes(ipv6Data, ipFamilyIPv6)
	require.NoError(t, err)
	require.Len(t, ipv6Only, 1)

	dualFromIPv6Only, err := parseCloudFrontOriginFacingPrefixes(ipv6Data, ipFamilyDualStack)
	require.NoError(t, err)
	require.Len(t, dualFromIPv6Only, 1)
}

func testSyntax(config string) error {
	d := caddyfile.NewTestDispenser(config)
	c := &CaddyTrustedCloudFront{}
	err := c.UnmarshalCaddyfile(d)
	if err != nil {
		return fmt.Errorf("unmarshal error for %q: %v", config, err)
	}

	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.TODO()})
	defer cancel()

	err = c.Provision(ctx)
	if err != nil {
		return fmt.Errorf("provision error for %q: %v", config, err)
	}
	return nil
}

func testOriginFacingSyntax(config string) error {
	d := caddyfile.NewTestDispenser(config)
	c := &CaddyTrustedCloudFrontOriginFacing{}
	err := c.UnmarshalCaddyfile(d)
	if err != nil {
		return fmt.Errorf("unmarshal error for %q: %v", config, err)
	}

	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.TODO()})
	defer cancel()

	err = c.Provision(ctx)
	if err != nil {
		return fmt.Errorf("provision error for %q: %v", config, err)
	}
	return nil
}

func jsonDecodeString(payload string, target any) error {
	return json.NewDecoder(strings.NewReader(payload)).Decode(target)
}
