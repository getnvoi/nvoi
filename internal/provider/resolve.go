package provider

import (
	"fmt"
	"os"
)

func ResolveCompute(name string) (ComputeProvider, error) {
	factory, ok := computeProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported compute provider: %q", name)
	}
	return factory()
}

func ResolveDNS(name, zone string) (DNSProvider, error) {
	factory, ok := dnsProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported DNS provider: %q", name)
	}
	return factory(zone)
}

func ResolveBucket(name string) (BucketProvider, error) {
	factory, ok := bucketProviders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported bucket provider: %q", name)
	}
	return factory()
}

func ResolveBuilder(name string) (Builder, error) {
	factory, ok := builders[name]
	if !ok {
		return nil, fmt.Errorf("unsupported builder: %q", name)
	}
	return factory()
}

// Provider registries — populated by init() in provider packages.

var computeProviders = map[string]func() (ComputeProvider, error){}
var dnsProviders = map[string]func(zone string) (DNSProvider, error){}
var bucketProviders = map[string]func() (BucketProvider, error){}
var builders = map[string]func() (Builder, error){}

func RegisterCompute(name string, factory func() (ComputeProvider, error)) { computeProviders[name] = factory }
func RegisterDNS(name string, factory func(string) (DNSProvider, error))   { dnsProviders[name] = factory }
func RegisterBucket(name string, factory func() (BucketProvider, error))   { bucketProviders[name] = factory }
func RegisterBuilder(name string, factory func() (Builder, error))         { builders[name] = factory }

func Env(key string) string { return os.Getenv(key) }
