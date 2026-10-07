package app

import (
	"context"
	"testing"
	"time"
)

func BenchmarkServerChangedEvent(b *testing.B) {
	a := benchmarkIdleApp(b)
	now := a.options.Now()
	a.options.Now = func() time.Time { return now }
	p := &peer{out: make(chan outbound, 4), closed: make(chan struct{}), subscribed: true, presentationActive: true}
	s := &Server{app: a, peers: map[*peer]bool{p: true}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Microsecond)
		s.snapshotTo(true)
		<-p.out
	}
}

func BenchmarkSubscription(b *testing.B) {
	a := benchmarkIdleApp(b)
	s := &Server{app: a, peers: map[*peer]bool{}}
	p := &peer{out: make(chan outbound, 4), closed: make(chan struct{})}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reply, _ := a.handle(context.Background(), request("subscribe", nil), true)
		if err := s.subscribe(p, reply); err != nil {
			b.Fatal(err)
		}
		<-p.out
	}
}
