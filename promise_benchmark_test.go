package workshop

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func BenchmarkNewPromise(b *testing.B) {
	pool := NewPool(runtime.GOMAXPROCS(0))
	time.Sleep(time.Second)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		var pc = Process{
			Process: func(ctx context.Context, last interface{}) (interface{}, error) {
				return nil, nil
			},
		}

		for pb.Next() {
			p := NewPromise(pool, pc)
			_, err := p.Get(context.Background(), true)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkNewPartitionPromise(b *testing.B) {
	pool := NewPool(runtime.GOMAXPROCS(0))
	time.Sleep(time.Second)
	b.ResetTimer()

	var i = 0

	b.RunParallel(func(pb *testing.PB) {
		var pc = Process{
			Partition: true,
			EventKey: i,
			Process: func(ctx context.Context, last interface{}) (interface{}, error) {
				return nil, nil
			},
		}

		for pb.Next() {
			p := NewPromise(pool, pc)
			_, err := p.Get(context.Background(), true)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}