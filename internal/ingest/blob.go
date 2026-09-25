package ingest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Blob stores raw and transformed files under slash-separated keys.
type Blob interface {
	PutFile(ctx context.Context, key, path string) error
	GetFile(ctx context.Context, key, path string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// FSBlob keeps files under a local directory (dev and tests).
type FSBlob struct{ Root string }

func (b FSBlob) PutFile(_ context.Context, key, path string) error {
	return copyFile(path, filepath.Join(b.Root, filepath.FromSlash(key)))
}

func (b FSBlob) GetFile(_ context.Context, key, path string) error {
	return copyFile(filepath.Join(b.Root, filepath.FromSlash(key)), path)
}

func (b FSBlob) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(b.Root, filepath.FromSlash(key)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// S3Blob stores files in an S3-compatible bucket.
type S3Blob struct {
	c      *minio.Client
	bucket string
}

func NewS3Blob(endpoint, bucket, accessKey, secretKey string, useSSL bool) (*S3Blob, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: useSSL})
	if err != nil {
		return nil, err
	}
	return &S3Blob{c: c, bucket: bucket}, nil
}

func (b *S3Blob) PutFile(ctx context.Context, key, path string) error {
	_, err := b.c.FPutObject(ctx, b.bucket, key, path, minio.PutObjectOptions{})
	return err
}

func (b *S3Blob) GetFile(ctx context.Context, key, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return b.c.FGetObject(ctx, b.bucket, key, path, minio.GetObjectOptions{})
}

func (b *S3Blob) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.c.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	return false, err
}
