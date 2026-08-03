package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
)

func init() {
	rootCmd.AddCommand(storageCmd)
	storageCmd.AddCommand(storageTestCmd)
}

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Object storage commands",
	Long:  `Commands for testing access to the S3-compatible object storage.`,
}

var storageTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Test access to the object storage",
	Long:  `Checks connectivity and credentials against the S3-compatible object storage. If the configured bucket exists, also performs a round-trip write/read/delete of a probe object.`,
	Run: func(cmd *cobra.Command, args []string) {
		runStorageTest()
	},
}

func runStorageTest() {
	// NotifyContext returns a context canceled on Ctrl+C or SIGTERM, so
	// long-running HTTP calls can abort cleanly instead of hanging forever.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := getS3Config()

	if cfg.Endpoint == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		printError("object storage endpoint, access key, and secret key are required (flags --s3-*, env S3_*, or config)")
		os.Exit(1)
	}

	client, err := minio.New(archive.NormalizeEndpoint(cfg.Endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		printError("failed to create object storage client: %v", err)
		os.Exit(1)
	}

	buckets, err := client.ListBuckets(ctx)
	if err != nil {
		printError("access denied: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Endpoint: %s\n", cfg.Endpoint)
	fmt.Printf("Access:   OK\n")
	fmt.Printf("Buckets:  %d\n", len(buckets))

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		printError("failed to check bucket %q: %v", cfg.Bucket, err)
		os.Exit(1)
	}

	if !exists {
		fmt.Printf("Bucket:   %s (does not exist, not created by this test)\n", cfg.Bucket)
		return
	}

	fmt.Printf("Bucket:   %s (exists)\n", cfg.Bucket)

	probeKey := "_sklein-convarchive-test/" + uuid.NewString()
	payload := []byte("sklein-convarchive round-trip probe\n")

	if _, err := client.PutObject(ctx, cfg.Bucket, probeKey, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{}); err != nil {
		printError("round-trip write failed: %v", err)
		os.Exit(1)
	}

	obj, err := client.GetObject(ctx, cfg.Bucket, probeKey, minio.GetObjectOptions{})
	if err != nil {
		printError("round-trip read failed: %v", err)
		os.Exit(1)
	}
	read, err := io.ReadAll(obj)
	obj.Close()
	if err != nil {
		printError("round-trip read failed: %v", err)
		os.Exit(1)
	}
	if !bytes.Equal(read, payload) {
		printError("round-trip content mismatch: got %d bytes, want %d", len(read), len(payload))
		os.Exit(1)
	}

	if err := client.RemoveObject(ctx, cfg.Bucket, probeKey, minio.RemoveObjectOptions{}); err != nil {
		printError("round-trip cleanup failed: %v", err)
		os.Exit(1)
	}

	fmt.Println("Round-trip: OK")
}
