// Package blob is an S3-compatible object store client (R2, MinIO, SeaweedFS).
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("blob: not found")

// Config configures a Store.
type Config struct {
	Endpoint  string
	Region    string // default "auto"
	Bucket    string
	AccessKey string
	SecretKey string
}

// Store wraps an S3 bucket.
type Store struct {
	c      *s3.Client
	ps     *s3.PresignClient
	bucket string
}

// Info describes an object.
type Info struct {
	Key      string
	Size     int64
	Modified time.Time
	Metadata map[string]string
}

// New creates a Store using path-style addressing and a custom endpoint.
func New(cfg Config) *Store {
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	c := s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		// Third-party stores often lack the newer default checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Store{c: c, ps: s3.NewPresignClient(c), bucket: cfg.Bucket}
}

func notFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	if errors.As(err, &nsk) || errors.As(err, &nf) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

// Ping checks that the bucket answers with the configured credentials: it asks for a key that does not exist,
// so "not found" is success and anything else (network, credentials, missing bucket) is an error.
func (s *Store) Ping(ctx context.Context) error {
	if _, err := s.Head(ctx, ".readyz-probe"); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Put uploads a stream of known length.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, length int64, meta map[string]string) error {
	_, err := s.c.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          r,
		ContentLength: aws.Int64(length),
		Metadata:      meta,
	})
	if err != nil {
		return fmt.Errorf("blob put %s: %w", key, err)
	}
	return nil
}

// Get streams an object; caller must close the reader.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob get %s: %w", key, err)
	}
	return o.Body, nil
}

// Head returns object info.
func (s *Store) Head(ctx context.Context, key string) (*Info, error) {
	o, err := s.c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		if notFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("blob head %s: %w", key, err)
	}
	return &Info{Key: key, Size: aws.ToInt64(o.ContentLength), Modified: aws.ToTime(o.LastModified), Metadata: o.Metadata}, nil
}

// List returns all objects under prefix with key > after (lexicographic), paging internally.
func (s *Store) List(ctx context.Context, prefix, after string) ([]Info, error) {
	in := &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix}
	if after != "" {
		in.StartAfter = &after
	}
	var out []Info
	p := s3.NewListObjectsV2Paginator(s.c, in)
	for p.HasMorePages() {
		pg, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("blob list %s: %w", prefix, err)
		}
		for _, o := range pg.Contents {
			out = append(out, Info{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified)})
		}
	}
	return out, nil
}

// Delete removes one object (missing keys are not an error).
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key}); err != nil && !notFound(err) {
		return fmt.Errorf("blob delete %s: %w", key, err)
	}
	return nil
}

// DeleteMany removes keys in batches of 1000.
func (s *Store) DeleteMany(ctx context.Context, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), 1000)
		ids := make([]types.ObjectIdentifier, n)
		for i, k := range keys[:n] {
			ids[i] = types.ObjectIdentifier{Key: aws.String(k)}
		}
		res, err := s.c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: &s.bucket,
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("blob delete many: %w", err)
		}
		if len(res.Errors) > 0 {
			e := res.Errors[0]
			return fmt.Errorf("blob delete many: %s: %s (%d failures)", aws.ToString(e.Key), aws.ToString(e.Message), len(res.Errors))
		}
		keys = keys[n:]
	}
	return nil
}

// PresignGet returns a time-limited GET URL.
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	r, err := s.ps.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob presign %s: %w", key, err)
	}
	return r.URL, nil
}
