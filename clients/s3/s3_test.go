package s3client_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	s3client "github.com/theizzatbek/gokit/clients/s3"
	"github.com/theizzatbek/gokit/errs"
)

var (
	mOnce sync.Once
	mCfg  s3client.Config
	mErr  error
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func initContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c, err := tcminio.Run(ctx, "minio/minio:latest",
		tcminio.WithUsername("minio"),
		tcminio.WithPassword("minio12345"),
	)
	if err != nil {
		mErr = err
		return
	}
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		mErr = err
		return
	}
	mCfg = s3client.Config{
		Endpoint:        "http://" + endpoint,
		Region:          "us-east-1",
		AccessKeyID:     "minio",
		SecretAccessKey: "minio12345",
		Bucket:          "test-bucket",
		ForcePathStyle:  true,
	}
}

// newClient builds a connected client and ensures the test bucket
// exists. The bucket name comes from the per-test value to keep
// concurrent tests isolated.
func newClient(t *testing.T, bucket string) *s3client.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("requires Docker for MinIO testcontainer")
	}
	mOnce.Do(initContainer)
	if mErr != nil {
		t.Fatalf("container: %v", mErr)
	}
	cfg := mCfg
	cfg.Bucket = bucket
	cli, err := s3client.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure bucket exists; ignore "already exists" errors so the
	// test is idempotent across reruns.
	_, _ = cli.API().CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	return cli
}

func TestConnect_MissingBucket(t *testing.T) {
	_, err := s3client.Connect(context.Background(), s3client.Config{Region: "us-east-1"})
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != s3client.CodeMissingBucket {
		t.Errorf("err = %v, want CodeMissingBucket", err)
	}
}

func TestPutGet_RoundTrip(t *testing.T) {
	cli := newClient(t, "put-get")
	ctx := context.Background()
	body := []byte("hello world")
	if err := cli.Put(ctx, "greet.txt", bytes.NewReader(body),
		s3client.WithPutContentType("text/plain")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	r, err := cli.Get(ctx, "greet.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if string(got) != string(body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestGet_NotFoundMapsToErrsNotFound(t *testing.T) {
	cli := newClient(t, "get-404")
	_, err := cli.Get(context.Background(), "missing.txt")
	if err == nil {
		t.Fatal("expected NotFound error")
	}
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != s3client.CodeNotFound {
		t.Errorf("err = %v, want CodeNotFound", err)
	}
}

func TestExists(t *testing.T) {
	cli := newClient(t, "exists")
	ctx := context.Background()
	if err := cli.Put(ctx, "present.txt", strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}

	ok, err := cli.Exists(ctx, "present.txt")
	if err != nil || !ok {
		t.Errorf("Exists(present) = (%v, %v), want (true, nil)", ok, err)
	}

	ok, err = cli.Exists(ctx, "absent.txt")
	if err != nil || ok {
		t.Errorf("Exists(absent) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestDelete_RemovesObject(t *testing.T) {
	cli := newClient(t, "delete")
	ctx := context.Background()
	_ = cli.Put(ctx, "x.txt", strings.NewReader("x"))
	if err := cli.Delete(ctx, "x.txt"); err != nil {
		t.Fatal(err)
	}
	ok, _ := cli.Exists(ctx, "x.txt")
	if ok {
		t.Error("object should be gone after Delete")
	}
}

func TestPresignGet_DownloadsContent(t *testing.T) {
	cli := newClient(t, "presign-get")
	ctx := context.Background()
	_ = cli.Put(ctx, "p.txt", strings.NewReader("presigned"),
		s3client.WithPutContentType("text/plain"))
	url, err := cli.PresignGet(ctx, "p.txt", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "presigned" {
		t.Errorf("body = %q", body)
	}
}

func TestPresignPut_UploadsContent(t *testing.T) {
	cli := newClient(t, "presign-put")
	ctx := context.Background()
	url, err := cli.PresignPut(ctx, "upload.txt", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("PUT", url, strings.NewReader("uploaded"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, body = %s", resp.StatusCode, body)
	}
	// Verify the upload landed.
	r, err := cli.Get(ctx, "upload.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if string(got) != "uploaded" {
		t.Errorf("body = %q", got)
	}
}

func TestList_IteratesPrefix(t *testing.T) {
	cli := newClient(t, "list")
	ctx := context.Background()
	for _, k := range []string{"prefix/a", "prefix/b", "other"} {
		_ = cli.Put(ctx, k, strings.NewReader("x"))
	}
	got := map[string]bool{}
	for obj, err := range cli.List(ctx, "prefix/") {
		if err != nil {
			t.Fatalf("iter err: %v", err)
		}
		got[obj.Key] = true
	}
	if !got["prefix/a"] || !got["prefix/b"] {
		t.Errorf("got = %v, want prefix/a + prefix/b", got)
	}
	if got["other"] {
		t.Error("prefix filter ignored")
	}
}

func TestList_EarlyBreak(t *testing.T) {
	cli := newClient(t, "list-break")
	ctx := context.Background()
	for _, k := range []string{"x/1", "x/2", "x/3"} {
		_ = cli.Put(ctx, k, strings.NewReader("x"))
	}
	count := 0
	for obj, err := range cli.List(ctx, "x/") {
		if err != nil {
			t.Fatal(err)
		}
		count++
		_ = obj
		if count == 1 {
			break
		}
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (early break should stop iteration)", count)
	}
}
