// Package remotestate safely checks individual friend SQLite files in and out
// of Cloud Storage.
package remotestate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
)

type Repository struct {
	client *storage.Client
	bucket *storage.BucketHandle
	prefix string
}

type Checkout struct {
	Path       string
	FriendID   string
	Generation int64
	tempDir    string
	objectName string
}

func New(ctx context.Context, bucket, prefix string) (*Repository, error) {
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("state bucket is required")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Repository{
		client: client,
		bucket: client.Bucket(bucket),
		prefix: strings.Trim(strings.TrimSpace(prefix), "/"),
	}, nil
}

func (r *Repository) Close() error {
	return r.client.Close()
}

func (r *Repository) Checkout(
	ctx context.Context,
	friendID string,
) (*Checkout, error) {
	if strings.TrimSpace(friendID) == "" {
		return nil, fmt.Errorf("friend id is required")
	}
	tempDir, err := os.MkdirTemp("", "daimon-friend-"+friendID+"-")
	if err != nil {
		return nil, err
	}
	checkout := &Checkout{
		Path:       filepath.Join(tempDir, friendID+".sqlite"),
		FriendID:   friendID,
		tempDir:    tempDir,
		objectName: r.objectName(friendID),
	}
	object := r.bucket.Object(checkout.objectName)
	attrs, err := object.Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return checkout, nil
	}
	if err != nil {
		checkout.Discard()
		return nil, err
	}
	checkout.Generation = attrs.Generation
	reader, err := object.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		checkout.Discard()
		return nil, err
	}
	defer reader.Close()
	file, err := os.OpenFile(checkout.Path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		checkout.Discard()
		return nil, err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		checkout.Discard()
		return nil, copyErr
	}
	if closeErr != nil {
		checkout.Discard()
		return nil, closeErr
	}
	return checkout, nil
}

func (r *Repository) Commit(ctx context.Context, checkout *Checkout) error {
	if checkout == nil || checkout.Path == "" || checkout.objectName == "" {
		return fmt.Errorf("valid checkout is required")
	}
	file, err := os.Open(checkout.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	object := r.bucket.Object(checkout.objectName)
	if checkout.Generation == 0 {
		object = object.If(storage.Conditions{DoesNotExist: true})
	} else {
		object = object.If(storage.Conditions{
			GenerationMatch: checkout.Generation,
		})
	}
	writer := object.NewWriter(ctx)
	writer.ContentType = "application/vnd.sqlite3"
	writer.CacheControl = "no-store"
	if _, err := io.Copy(writer, file); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	checkout.Discard()
	return nil
}

func (c *Checkout) Discard() {
	if c == nil || c.tempDir == "" {
		return
	}
	_ = os.RemoveAll(c.tempDir)
	c.tempDir = ""
}

func (r *Repository) objectName(friendID string) string {
	name := friendID + ".sqlite"
	if r.prefix == "" {
		return name
	}
	return r.prefix + "/" + name
}
