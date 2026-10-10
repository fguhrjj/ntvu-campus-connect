// Package notify bridges OS callbacks (which carry only an opaque context
// value) to Go functions, and guarantees each OS registration is released
// exactly once.
package notify

import (
	"context"
	"sync"
)

// Registry maps callback context ids to subscribers. The zero value is ready.
type Registry[T any] struct {
	mu   sync.Mutex
	next uintptr
	subs map[uintptr]*sub[T]
}

type sub[T any] struct {
	fn     func(T)
	mu     sync.RWMutex // held for reading while fn runs
	closed bool
}

// Subscribe adds fn, then calls register with the id the OS callback will
// pass back to Deliver. The returned unsubscribe is idempotent, waits for an
// in-flight callback to finish, and calls release once. Cancelling ctx
// unsubscribes too.
func (r *Registry[T]) Subscribe(ctx context.Context, fn func(T), register func(id uintptr) (release func(), err error)) (func(), error) {
	s := &sub[T]{fn: fn}
	r.mu.Lock()
	if r.subs == nil {
		r.subs = map[uintptr]*sub[T]{}
	}
	r.next++
	id := r.next
	r.subs[id] = s
	r.mu.Unlock()

	release, err := register(id)
	if err != nil {
		r.remove(id, s)
		return nil, err
	}
	var once sync.Once
	stop := make(chan struct{})
	unsubscribe := func() {
		once.Do(func() {
			close(stop)
			r.remove(id, s)
			release()
		})
	}
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				unsubscribe()
			case <-stop:
			}
		}()
	}
	return unsubscribe, nil
}

func (r *Registry[T]) remove(id uintptr, s *sub[T]) {
	r.mu.Lock()
	delete(r.subs, id)
	r.mu.Unlock()
	s.mu.Lock() // waits for a running fn
	s.closed = true
	s.mu.Unlock()
}

// Deliver runs the subscriber for id; false if it is gone.
func (r *Registry[T]) Deliver(id uintptr, v T) bool {
	r.mu.Lock()
	s := r.subs[id]
	r.mu.Unlock()
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false
	}
	s.fn(v)
	return true
}
