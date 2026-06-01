package sessionsredis_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/theizzatbek/gokit/auth/sessions"
	"github.com/theizzatbek/gokit/auth/sessionsredis"
)

var testRDB *redis.Client

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	flag.Parse()
	if testing.Short() {
		return 0
	}
	ctx := context.Background()
	c, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		fmt.Println("testcontainers redis start failed:", err.Error())
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(c) }()

	url, err := c.ConnectionString(ctx)
	if err != nil {
		fmt.Println("conn string:", err.Error())
		return 1
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		fmt.Println("parse:", err.Error())
		return 1
	}
	testRDB = redis.NewClient(opts)
	if err := testRDB.Ping(ctx).Err(); err != nil {
		fmt.Println("ping:", err.Error())
		return 1
	}
	defer testRDB.Close()
	return m.Run()
}

func flushRedis(t *testing.T) {
	t.Helper()
	if err := testRDB.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func freshStore() *sessionsredis.Store {
	return sessionsredis.New(testRDB, "t:")
}

func sampleSession() *sessions.Session {
	now := time.Now()
	return &sessions.Session{
		ID:         "session-id-43-chars-long-aaaaaaaaaaaaaaaaaaa",
		Subject:    "u-1",
		Claims:     []byte(`{"plan":"pro"}`),
		Scopes:     []string{"read", "write"},
		Roles:      []string{"admin"},
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(time.Hour),
	}
}

func TestCreateGetRoundTrip(t *testing.T) {
	flushRedis(t)
	s := freshStore()
	ctx := context.Background()
	in := sampleSession()
	if err := s.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	out, err := s.Get(ctx, in.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out == nil {
		t.Fatal("Get returned nil")
	}
	if out.Subject != in.Subject {
		t.Errorf("Subject = %q, want %q", out.Subject, in.Subject)
	}
	if string(out.Claims) != string(in.Claims) {
		t.Errorf("Claims = %q, want %q", out.Claims, in.Claims)
	}
	if len(out.Scopes) != 2 || out.Scopes[0] != "read" {
		t.Errorf("Scopes = %v, want [read write]", out.Scopes)
	}
}

func TestGet_MissingReturnsNil(t *testing.T) {
	flushRedis(t)
	out, err := freshStore().Get(context.Background(), "nope")
	if err != nil {
		t.Errorf("Get unknown returned err: %v", err)
	}
	if out != nil {
		t.Errorf("Get unknown = %+v, want nil", out)
	}
}

func TestTouch_AdvancesExpiry(t *testing.T) {
	flushRedis(t)
	s := freshStore()
	ctx := context.Background()
	in := sampleSession()
	_ = s.Create(ctx, in)

	newExp := in.ExpiresAt.Add(time.Hour).Truncate(time.Second)
	if err := s.Touch(ctx, in.ID, time.Now(), newExp); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	out, _ := s.Get(ctx, in.ID)
	if !out.ExpiresAt.Equal(newExp.UTC()) {
		t.Errorf("ExpiresAt = %v, want %v", out.ExpiresAt, newExp.UTC())
	}
}

func TestDelete_RemovesSession(t *testing.T) {
	flushRedis(t)
	s := freshStore()
	ctx := context.Background()
	in := sampleSession()
	_ = s.Create(ctx, in)
	if err := s.Delete(ctx, in.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	out, _ := s.Get(ctx, in.ID)
	if out != nil {
		t.Errorf("Get after Delete = %+v, want nil", out)
	}
}

func TestDeleteForSubject_DropsAll(t *testing.T) {
	flushRedis(t)
	s := freshStore()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		sess := sampleSession()
		sess.ID = fmt.Sprintf("%s-%d", sess.ID[:30], i)
		// pad to ≥1 char so ID still uniquely identifies the session
		_ = s.Create(ctx, sess)
	}
	if err := s.DeleteForSubject(ctx, "u-1"); err != nil {
		t.Fatalf("DeleteForSubject: %v", err)
	}
	// All three should be gone.
	keys, _ := testRDB.Keys(ctx, "t:session:*").Result()
	if len(keys) != 0 {
		t.Errorf("residual keys after DeleteForSubject: %v", keys)
	}
}
