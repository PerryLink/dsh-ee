package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeTaskClaimCancelAndArtifact(t *testing.T) {
	s, dsn := openTokenTestStoreDSN(t)
	ctx := context.Background()
	other, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	owner := tokenTestUser(t, s, false).ID
	otherOwner := owner + 1
	spec := RuntimeTask{OwnerID: owner, RunAsID: owner, Prompt: "summary", IdempotencyKey: "slot-1", TimeoutSeconds: 30}
	task, err := s.EnqueueRuntimeTask(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := other.EnqueueRuntimeTask(ctx, spec)
	if err != nil || duplicate.ID != task.ID {
		t.Fatalf("idempotent enqueue: %+v %v", duplicate, err)
	}
	if _, err := s.RuntimeTask(ctx, task.ID, otherOwner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner lookup: %v", err)
	}
	claim, err := other.ClaimRuntimeTask(ctx, 2, time.Minute)
	if err != nil || claim.ID != task.ID || claim.Attempts != 1 {
		t.Fatalf("initial claim: %+v %v", claim, err)
	}
	if _, err := s.ClaimRuntimeTask(ctx, 2, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate claim: %v", err)
	}
	if err := s.CancelRuntimeTask(ctx, task.ID, owner); err != nil {
		t.Fatal(err)
	}
	artifacts := []RuntimeArtifact{{Name: "summary.txt", StorageKey: "attempt-1", Size: 3}}
	if err := other.FinishRuntimeTask(ctx, claim, RuntimeSucceeded, "yes", "", artifacts); !errors.Is(err, ErrConflict) {
		t.Fatalf("canceled attempt published a result: %v", err)
	}
	spec.IdempotencyKey = "slot-2"
	if _, err := s.EnqueueRuntimeTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	claim, err = s.ClaimRuntimeTask(ctx, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRuntimeTask(ctx, claim, RuntimeSucceeded, "yes", "", artifacts); err != nil {
		t.Fatal(err)
	}
	got, err := other.RuntimeArtifacts(ctx, claim.ID, owner)
	if err != nil || len(got) != 1 || got[0].Name != "summary.txt" {
		t.Fatalf("artifact lookup: %+v %v", got, err)
	}
	hidden, err := other.RuntimeArtifacts(ctx, claim.ID, otherOwner)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("cross-owner artifact lookup: %+v %v", hidden, err)
	}
}

func TestRuntimeQueueLimitAcrossStores(t *testing.T) {
	s, dsn := openTokenTestStoreDSN(t)
	ctx := context.Background()
	other, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	owner := tokenTestUser(t, s, false).ID
	spec := RuntimeTask{OwnerID: owner, RunAsID: owner, Prompt: "summary", TimeoutSeconds: 30}
	start := make(chan struct{})
	results := make(chan error, RuntimeTaskQueueLimit*2)
	for i := 0; i < cap(results); i++ {
		go func(st *Store) {
			<-start
			_, err := st.EnqueueRuntimeTask(ctx, spec)
			results <- err
		}([]*Store{s, other}[i%2])
	}
	close(start)
	var accepted, rejected int
	for i := 0; i < cap(results); i++ {
		switch err := <-results; {
		case err == nil:
			accepted++
		case errors.Is(err, ErrRuntimeQueueFull):
			rejected++
		default:
			t.Errorf("unexpected enqueue error: %v", err)
		}
	}
	if accepted != RuntimeTaskQueueLimit || rejected != RuntimeTaskQueueLimit {
		t.Fatalf("accepted %d, rejected %d, cap %d", accepted, rejected, RuntimeTaskQueueLimit)
	}
}
