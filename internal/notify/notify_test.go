package notify

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnsubscribeStopsDeliveryAndReleasesOnce(t *testing.T) {
	var r Registry[string]
	var got []string
	var id uintptr
	var released atomic.Int32
	unsubscribe, err := r.Subscribe(context.Background(), func(v string) { got = append(got, v) }, func(i uintptr) (func(), error) {
		id = i
		return func() { released.Add(1) }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Deliver(id, "NTVU-R") {
		t.Fatal("Deliver to live subscription returned false")
	}
	unsubscribe()
	unsubscribe()
	if r.Deliver(id, "late") {
		t.Fatal("Deliver after unsubscribe returned true")
	}
	if len(got) != 1 || got[0] != "NTVU-R" {
		t.Fatalf("delivered = %v", got)
	}
	if released.Load() != 1 {
		t.Fatalf("release calls = %d, want 1", released.Load())
	}
}

func TestContextCancelUnsubscribes(t *testing.T) {
	var r Registry[struct{}]
	ctx, cancel := context.WithCancel(context.Background())
	released := make(chan struct{})
	var id uintptr
	unsubscribe, err := r.Subscribe(ctx, func(struct{}) {}, func(i uintptr) (func(), error) {
		id = i
		return func() { close(released) }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("context cancel did not release the OS registration")
	}
	if r.Deliver(id, struct{}{}) {
		t.Fatal("delivered after context cancel")
	}
	unsubscribe() // still safe; must not release twice (close would panic)
}

func TestRegisterFailureLeavesNothingSubscribed(t *testing.T) {
	var r Registry[int]
	var id uintptr
	_, err := r.Subscribe(context.Background(), func(int) { t.Fatal("delivered to failed subscription") }, func(i uintptr) (func(), error) {
		id = i
		return nil, errors.New("WLAN service not running")
	})
	if err == nil {
		t.Fatal("Subscribe ignored register error")
	}
	if r.Deliver(id, 1) {
		t.Fatal("failed subscription still receives callbacks")
	}
}

func TestUnsubscribeWaitsForInFlightDelivery(t *testing.T) {
	var r Registry[int]
	var id uintptr
	entered, finish := make(chan struct{}), make(chan struct{})
	var done atomic.Bool
	unsubscribe, _ := r.Subscribe(context.Background(), func(int) {
		close(entered)
		<-finish
		done.Store(true)
	}, func(i uintptr) (func(), error) { id = i; return func() {}, nil })
	go r.Deliver(id, 1)
	<-entered
	returned := make(chan struct{})
	go func() { unsubscribe(); close(returned) }()
	select {
	case <-returned:
		t.Fatal("unsubscribe returned while a callback was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	<-returned
	if !done.Load() {
		t.Fatal("callback did not finish")
	}
}
