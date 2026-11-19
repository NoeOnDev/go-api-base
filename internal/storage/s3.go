package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config is where the bucket is and how to sign the requests (the S3_* variables).
type S3Config struct {
	Bucket string
	Region string
	// Endpoint is the address of a compatible service (Cloudflare R2, MinIO, DigitalOcean Spaces).
	// Empty, Amazon S3.
	Endpoint string
	// AccessKeyID and SecretAccessKey are fixed credentials. Empty, the SDK looks for them where AWS
	// leaves them: the role of the machine (EC2, ECS, EKS) or the AWS_* environment variables.
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle puts the bucket in the path (https://host/bucket/key) instead of in the name of the
	// host. MinIO needs it.
	PathStyle bool
}

// S3 keeps the objects in a bucket of Amazon S3 or of a compatible service, with the official SDK.
// The bucket is private: the files reach the browser through the API, which checks the permissions.
type S3 struct {
	client *s3.Client
	bucket string
}

// NewS3 creates the client. It does not call the service: a wrong bucket or key shows on the first
// upload.
func NewS3(ctx context.Context, c S3Config) (*S3, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(c.Region)}
	if c.AccessKeyID != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.Endpoint != "" {
			o.BaseEndpoint = aws.String(c.Endpoint)
		}
		o.UsePathStyle = c.PathStyle
		// The SDK adds a checksum of its own to every request by default, which some compatible
		// services reject. Only when the operation requires it: the signature already covers the body.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &S3{client: client, bucket: c.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

// Open returns the body of the object as it arrives: it is not an io.Seeker.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	var noKey *types.NoSuchKey
	if errors.As(err, &noKey) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: get %s: %w", key, err)
	}
	return out.Body, nil
}

// Delete removes the object. S3 answers the same for a key that does not exist.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}
