package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const presignTTL = 5 * time.Minute

// S3Client wraps the AWS S3 presign client for media uploads.
type S3Client struct {
	client   *s3.Client
	presign  *s3.PresignClient
	bucket   string
	endpoint string // public-facing base URL, e.g. "https://s3.example.com"
}

// Config holds the values read from environment variables.
type Config struct {
	Endpoint        string // e.g. "https://s3.example.com"
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// NewS3Client constructs an S3Client configured for an S3-compatible endpoint.
func NewS3Client(cfg Config) (*S3Client, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
		config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(
				func(service, region string, options ...interface{}) (aws.Endpoint, error) {
					return aws.Endpoint{
						URL:               cfg.Endpoint,
						HostnameImmutable: true, // required for path-style MinIO URLs
					}, nil
				},
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true // MinIO requires path-style: endpoint/bucket/key
	})

	return &S3Client{
		client:   client,
		presign:  s3.NewPresignClient(client),
		bucket:   cfg.Bucket,
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
	}, nil
}

// PresignPut returns a presigned PUT URL the browser can use to upload directly
// to S3. The URL expires after 5 minutes.
func (c *S3Client) PresignPut(ctx context.Context, key, contentType string, contentLength int64) (string, error) {
	req, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(contentLength),
	}, s3.WithPresignExpires(presignTTL))
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}
	return req.URL, nil
}

// PublicURL returns the public URL for a stored object.
func (c *S3Client) PublicURL(key string) string {
	return c.endpoint + "/" + c.bucket + "/" + key
}

// KeyFromURL extracts the object key from a public URL produced by PublicURL.
// Returns the key and true on success, or empty string and false if the URL
// does not belong to this client's bucket.
func (c *S3Client) KeyFromURL(publicURL string) (string, bool) {
	prefix := c.endpoint + "/" + c.bucket + "/"
	if !strings.HasPrefix(publicURL, prefix) {
		return "", false
	}
	return strings.TrimPrefix(publicURL, prefix), true
}

// DeleteObjects removes one or more objects from the bucket.
// Keys that do not exist are silently ignored (S3 delete is idempotent).
// If keys is empty the call is a no-op.
// Per-object failures (returned in the response body even on HTTP 200) are
// collected and returned as a combined error.
func (c *S3Client) DeleteObjects(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	objects := make([]s3types.ObjectIdentifier, len(keys))
	for i, k := range keys {
		objects[i] = s3types.ObjectIdentifier{Key: aws.String(k)}
	}
	out, err := c.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(c.bucket),
		Delete: &s3types.Delete{
			Objects: objects,
			// Quiet=false so that per-object errors are included in the response.
			// (With Quiet=true, failed objects are still reported but successes
			// are suppressed — we use false here to be explicit and future-proof.)
			Quiet: aws.Bool(false),
		},
	})
	if err != nil {
		return fmt.Errorf("storage: delete objects: %w", err)
	}
	// S3 returns HTTP 200 even when individual objects fail; errors are only
	// visible in the response body.
	if out != nil && len(out.Errors) > 0 {
		msgs := make([]string, len(out.Errors))
		for i, e := range out.Errors {
			msgs[i] = fmt.Sprintf("key=%q code=%s message=%s",
				aws.ToString(e.Key), aws.ToString(e.Code), aws.ToString(e.Message))
		}
		return fmt.Errorf("storage: delete objects: %d object(s) failed: %s",
			len(out.Errors), strings.Join(msgs, "; "))
	}
	return nil
}

// ExtForContentType returns the canonical file extension (without leading dot)
// for an accepted media MIME type, e.g. "image/jpeg" → "jpg".
// Returns "bin" for unknown types.
func ExtForContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	default:
		return "bin"
	}
}

// MediaKey builds the object key for a media upload:
//
//	rooms/{roomID}/{msgPrefix}/{name}
//
// name should already be safe (e.g. a hex string + extension).
func MediaKey(roomID, msgPrefix, name string) string {
	return "rooms/" + roomID + "/" + msgPrefix + "/" + name
}
