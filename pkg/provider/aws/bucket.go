package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// BucketClient manages S3 buckets via the AWS SDK.
type BucketClient struct {
	s3              *s3.Client
	region          string
	accessKeyID     string
	secretAccessKey string
	configErr       error // non-nil if LoadDefaultConfig failed
}

// NewBucket creates an AWS S3 bucket provider.
func NewBucket(creds map[string]string) *BucketClient {
	region := creds["region"]
	accessKeyID := creds["access_key_id"]
	secretAccessKey := creds["secret_access_key"]

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKeyID, secretAccessKey, "",
		)),
	)
	if err != nil {
		return &BucketClient{configErr: fmt.Errorf("aws: load s3 config: %w", err)}
	}
	return &BucketClient{
		s3:              s3.NewFromConfig(cfg),
		region:          region,
		accessKeyID:     accessKeyID,
		secretAccessKey: secretAccessKey,
	}
}

func (b *BucketClient) ValidateCredentials(ctx context.Context) error {
	if b.configErr != nil {
		return b.configErr
	}
	_, err := b.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return fmt.Errorf("aws s3: invalid credentials: %w", err)
	}
	return nil
}

func (b *BucketClient) EnsureBucket(ctx context.Context, name string) error {
	_, err := b.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(name),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(b.region),
		},
	})
	if err != nil {
		// BucketAlreadyOwnedByYou or BucketAlreadyExists = success
		// The SDK wraps these as operation errors
		if isS3BucketExists(err) {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", name, err)
	}
	return nil
}

func (b *BucketClient) EmptyBucket(ctx context.Context, name string) error {
	// List and delete all objects in batches
	paginator := s3.NewListObjectsV2Paginator(b.s3, &s3.ListObjectsV2Input{
		Bucket: aws.String(name),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list objects in %s: %w", name, err)
		}
		if len(page.Contents) == 0 {
			continue
		}

		objects := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}

		_, err = b.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(name),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("delete objects in %s: %w", name, err)
		}
	}
	return nil
}

func (b *BucketClient) DeleteBucket(ctx context.Context, name string) error {
	_, err := b.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(name),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("delete bucket %s: %w", name, err)
	}
	return nil
}

func (b *BucketClient) SetCORS(ctx context.Context, name string, origins, methods []string) error {
	if len(methods) == 0 {
		methods = []string{"GET", "PUT", "POST", "DELETE"}
	}

	corsOrigins := make([]string, len(origins))
	copy(corsOrigins, origins)
	corsMethods := make([]string, len(methods))
	copy(corsMethods, methods)

	_, err := b.s3.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(name),
		CORSConfiguration: &s3types.CORSConfiguration{
			CORSRules: []s3types.CORSRule{{
				AllowedOrigins: corsOrigins,
				AllowedMethods: corsMethods,
				AllowedHeaders: []string{"*"},
				ExposeHeaders:  []string{"ETag"},
				MaxAgeSeconds:  aws.Int32(3600),
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("set cors on %s: %w", name, err)
	}
	return nil
}

func (b *BucketClient) ClearCORS(ctx context.Context, name string) error {
	_, err := b.s3.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{
		Bucket: aws.String(name),
	})
	if err != nil && !isS3NotFound(err) {
		return fmt.Errorf("clear cors on %s: %w", name, err)
	}
	return nil
}

func (b *BucketClient) SetLifecycle(ctx context.Context, name string, expireDays int) error {
	_, err := b.s3.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(name),
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
			Rules: []s3types.LifecycleRule{{
				ID:     aws.String("nvoi-expire"),
				Status: s3types.ExpirationStatusEnabled,
				Filter: &s3types.LifecycleRuleFilter{Prefix: aws.String("")},
				Expiration: &s3types.LifecycleExpiration{
					Days: aws.Int32(int32(expireDays)),
				},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("set lifecycle on %s: %w", name, err)
	}
	return nil
}

func (b *BucketClient) Credentials(ctx context.Context) (provider.BucketCredentials, error) {
	return provider.BucketCredentials{
		Endpoint:        fmt.Sprintf("https://s3.%s.amazonaws.com", b.region),
		AccessKeyID:     b.accessKeyID,
		SecretAccessKey: b.secretAccessKey,
		Region:          b.region,
	}, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────────

func isS3BucketExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "BucketAlreadyOwnedByYou") || strings.Contains(msg, "BucketAlreadyExists")
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchBucket") || strings.Contains(msg, "NotFound") || strings.Contains(msg, "404")
}

func (b *BucketClient) ListResources(ctx context.Context) ([]provider.ResourceGroup, error) {
	resp, err := b.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	g := provider.ResourceGroup{Name: "S3 Buckets", Columns: []string{"Name", "Created"}}
	for _, bucket := range resp.Buckets {
		created := ""
		if bucket.CreationDate != nil {
			created = bucket.CreationDate.Format("2006-01-02")
		}
		g.Rows = append(g.Rows, []string{deref(bucket.Name), created})
	}
	return []provider.ResourceGroup{g}, nil
}

var _ provider.BucketProvider = (*BucketClient)(nil)
