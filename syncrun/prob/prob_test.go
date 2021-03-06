package prob

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbRunAndStop(t *testing.T) {
	var status int32

	prob := New(func(ctx context.Context) {
		atomic.StoreInt32(&status, 1)
		<- ctx.Done()
		atomic.StoreInt32(&status, 2)
	})

	prob.Start()

	time.Sleep(50 * time.Millisecond)
	if !prob.IsRunning() {
		t.Fatal("expect prob is running")
	}
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&status) != 1 {
		t.Fatal("expect status is 1")
	}
	prob.Stop()

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&status) != 2 {
		t.Fatal("expect status is 2")
	}
	if prob.IsRunning() {
		t.Fatal("expect prob is stopped")
	}

	if !prob.IsStopped() {
		t.Fatal("expect prob is stopped")
	}
}

func TestProbWaitStopAndRun(t *testing.T) {
	var status int32

	prob := New(func(ctx context.Context) {
		atomic.StoreInt32(&status, 1)
		<- ctx.Done()
	})

	go func() {
		time.Sleep(100 * time.Millisecond)
		prob.Start()
	}()

	err := prob.StopAndWait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&status) != 0 {
		t.Fatal("expect status is 0")
	}
}