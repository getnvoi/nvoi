package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// DNSClient manages Route53 DNS records.
type DNSClient struct {
	r53          *route53.Client
	zone         string // domain name (e.g. "myapp.com")
	hostedZoneID string // resolved lazily from zone
	configErr    error  // non-nil if LoadDefaultConfig failed
}

// NewDNS creates an AWS Route53 DNS provider.
func NewDNS(creds map[string]string) *DNSClient {
	r53, err := newRoute53Client(creds)
	return &DNSClient{
		r53:       r53,
		zone:      creds["zone"],
		configErr: err,
	}
}

func (d *DNSClient) ValidateCredentials(ctx context.Context) error {
	if d.configErr != nil {
		return d.configErr
	}
	if d.zone == "" {
		return fmt.Errorf("aws dns: zone is required (env: DNS_ZONE)")
	}
	_, err := d.resolveHostedZone(ctx)
	if err != nil {
		return fmt.Errorf("aws dns: %w", err)
	}
	return nil
}

func (d *DNSClient) EnsureARecord(ctx context.Context, domain, ip string) error {
	zoneID, err := d.resolveHostedZone(ctx)
	if err != nil {
		return err
	}

	rtype := provider.RecordType(ip)
	_, err = d.r53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: aws.String(domain),
					Type: r53types.RRType(rtype),
					TTL:  aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: aws.String(ip)},
					},
				},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("upsert %s record for %s: %w", rtype, domain, err)
	}
	return nil
}

func (d *DNSClient) DeleteARecord(ctx context.Context, domain string) error {
	zoneID, err := d.resolveHostedZone(ctx)
	if err != nil {
		return err
	}

	// List existing records to get current values (required for DELETE)
	for _, rtype := range []r53types.RRType{r53types.RRTypeA, r53types.RRTypeAaaa} {
		resp, err := d.r53.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
			HostedZoneId:    aws.String(zoneID),
			StartRecordName: aws.String(domain),
			StartRecordType: rtype,
			MaxItems:        aws.Int32(1),
		})
		if err != nil {
			continue
		}
		for _, rrs := range resp.ResourceRecordSets {
			if strings.TrimSuffix(deref(rrs.Name), ".") == domain && rrs.Type == rtype {
				d.r53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String(zoneID),
					ChangeBatch: &r53types.ChangeBatch{
						Changes: []r53types.Change{{
							Action:            r53types.ChangeActionDelete,
							ResourceRecordSet: &rrs,
						}},
					},
				})
			}
		}
	}
	return nil
}

func (d *DNSClient) ListARecords(ctx context.Context) ([]provider.DNSRecord, error) {
	zoneID, err := d.resolveHostedZone(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := d.r53.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}

	var out []provider.DNSRecord
	for _, rrs := range resp.ResourceRecordSets {
		if rrs.Type != r53types.RRTypeA && rrs.Type != r53types.RRTypeAaaa {
			continue
		}
		domain := strings.TrimSuffix(deref(rrs.Name), ".")
		for _, rr := range rrs.ResourceRecords {
			out = append(out, provider.DNSRecord{
				Domain: domain,
				IP:     deref(rr.Value),
				Type:   string(rrs.Type),
			})
		}
	}
	return out, nil
}

// resolveHostedZone finds the hosted zone ID from the zone domain name.
// Cached after first call.
func (d *DNSClient) resolveHostedZone(ctx context.Context) (string, error) {
	if d.hostedZoneID != "" {
		return d.hostedZoneID, nil
	}

	resp, err := d.r53.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName: aws.String(d.zone),
	})
	if err != nil {
		return "", fmt.Errorf("list hosted zones: %w", err)
	}

	for _, hz := range resp.HostedZones {
		hzName := strings.TrimSuffix(deref(hz.Name), ".")
		if hzName == d.zone {
			// ID comes as "/hostedzone/Z1234" — strip prefix
			id := deref(hz.Id)
			if idx := strings.LastIndex(id, "/"); idx >= 0 {
				id = id[idx+1:]
			}
			d.hostedZoneID = id
			return id, nil
		}
	}
	return "", fmt.Errorf("hosted zone %q not found in Route53", d.zone)
}

func (d *DNSClient) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	records, err := d.ListARecords(ctx)
	if err != nil {
		return nil, err
	}
	g := provider.ResourceGroup{Name: "DNS Records", Columns: []string{"Type", "Domain", "IP"}}
	for _, r := range records {
		g.Rows = append(g.Rows, []string{r.Type, r.Domain, r.IP})
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.DNSProvider = (*DNSClient)(nil)
