package repository

import (
	"context"
	"io"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3VideoAssetStorage struct {
	storage *S3ImageStorage
}

func ProvideVideoAssetStorageFactory() service.VideoAssetStorageFactory {
	return func(ctx context.Context, cfg *config.ImageStorageConfig) (service.VideoAssetStorage, error) {
		storage, err := NewS3ImageStorage(ctx, cfg)
		if err != nil {
			return nil, err
		}
		// Inputs must remain readable while the upstream queues a task. S3
		// signatures support at most seven days with these static credentials.
		if storage.presignExpiry < 24*time.Hour {
			storage.presignExpiry = 24 * time.Hour
		}
		if storage.presignExpiry > 7*24*time.Hour {
			storage.presignExpiry = 7 * 24 * time.Hour
		}
		return &S3VideoAssetStorage{storage: storage}, nil
	}
}

func (s *S3VideoAssetStorage) Save(ctx context.Context, key, contentType string, file io.ReadSeeker, size int64) (string, *time.Time, error) {
	_, err := s.storage.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.storage.bucket, Key: &key, Body: file,
		ContentLength: &size, ContentType: &contentType,
	})
	if err != nil {
		return "", nil, err
	}
	presign := s3.NewPresignClient(s.storage.client)
	result, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.storage.bucket, Key: &key,
	}, s3.WithPresignExpires(s.storage.presignExpiry))
	if err != nil {
		return "", nil, err
	}
	expiresAt := time.Now().UTC().Add(s.storage.presignExpiry)
	return result.URL, &expiresAt, nil
}
