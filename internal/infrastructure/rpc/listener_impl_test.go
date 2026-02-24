// Package rpc 监听器实现测试
package rpc

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jzhang405/NexKV/internal/domain/model"
	"github.com/jzhang405/NexKV/internal/domain/service"
)

// ==========================================
// funcListener 测试
// ==========================================

func TestFuncListener_OnSuccess(t *testing.T) {
	t.Run("basic success callback", func(t *testing.T) {
		called := atomic.Bool{}
		var receivedPeer model.PeerID

		listener := &funcListener{
			onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
				called.Store(true)
				receivedPeer = peer
			},
		}

		peer := model.PeerID("peer-1")
		resp := newTestMessage("response")
		stats := service.BroadcastStats{}

		listener.OnSuccess(peer, resp, stats)

		if !called.Load() {
			t.Fatal("OnSuccess callback was not called")
		}
		if receivedPeer != peer {
			t.Fatalf("expected peer %s, got %s", peer, receivedPeer)
		}
	})

	t.Run("nil callback does not panic", func(t *testing.T) {
		listener := &funcListener{}
		stats := service.BroadcastStats{}

		// 不应该 panic
		listener.OnSuccess("peer-1", newTestMessage("resp"), stats)
		listener.OnFailure("peer-1", errors.New("test"), stats)
		listener.OnMajority(stats)
		listener.OnComplete(stats)
	})
}

func TestFuncListener_OnFailure(t *testing.T) {
	t.Run("basic failure callback", func(t *testing.T) {
		called := atomic.Bool{}
		var receivedErr error

		listener := &funcListener{
			onFailure: func(peer model.PeerID, err error, stats service.BroadcastStats) {
				called.Store(true)
				receivedErr = err
			},
		}

		peer := model.PeerID("peer-1")
		testErr := errors.New("test error")
		stats := service.BroadcastStats{}

		listener.OnFailure(peer, testErr, stats)

		if !called.Load() {
			t.Fatal("OnFailure callback was not called")
		}
		if receivedErr != testErr {
			t.Fatal("received wrong error")
		}
	})
}

func TestFuncListener_OnMajority(t *testing.T) {
	t.Run("basic majority callback", func(t *testing.T) {
		called := atomic.Bool{}

		listener := &funcListener{
			onMajority: func(stats service.BroadcastStats) {
				called.Store(true)
			},
		}

		stats := service.BroadcastStats{}
		listener.OnMajority(stats)

		if !called.Load() {
			t.Fatal("OnMajority callback was not called")
		}
	})
}

func TestFuncListener_OnComplete(t *testing.T) {
	t.Run("basic complete callback", func(t *testing.T) {
		called := atomic.Bool{}

		listener := &funcListener{
			onFullDone: func(stats service.BroadcastStats) {
				called.Store(true)
			},
		}

		stats := service.BroadcastStats{}
		listener.OnComplete(stats)

		if !called.Load() {
			t.Fatal("OnComplete callback was not called")
		}
	})
}

// ==========================================
// multiListener 测试
// ==========================================

func TestMultiListener(t *testing.T) {
	t.Run("multiple callbacks", func(t *testing.T) {
		count1 := atomic.Int32{}
		count2 := atomic.Int32{}

		listener1 := &funcListener{
			onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
				count1.Add(1)
			},
		}
		listener2 := &funcListener{
			onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
				count2.Add(1)
			},
		}

		multi := &multiListener{
			callbacks: []service.BroadcastListener{listener1, listener2},
		}

		stats := service.BroadcastStats{}
		multi.OnSuccess("peer-1", newTestMessage("resp"), stats)

		if count1.Load() != 1 {
			t.Fatalf("expected count1=1, got %d", count1.Load())
		}
		if count2.Load() != 1 {
			t.Fatalf("expected count2=1, got %d", count2.Load())
		}
	})

	t.Run("all methods", func(t *testing.T) {
		count := atomic.Int32{}

		listener := &funcListener{
			onSuccess:  func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) { count.Add(1) },
			onFailure:  func(peer model.PeerID, err error, stats service.BroadcastStats) { count.Add(1) },
			onMajority: func(stats service.BroadcastStats) { count.Add(1) },
			onFullDone: func(stats service.BroadcastStats) { count.Add(1) },
		}

		multi := &multiListener{callbacks: []service.BroadcastListener{listener}}
		stats := service.BroadcastStats{}

		multi.OnSuccess("peer-1", newTestMessage("resp"), stats)
		multi.OnFailure("peer-1", errors.New("test"), stats)
		multi.OnMajority(stats)
		multi.OnComplete(stats)

		if count.Load() != 4 {
			t.Fatalf("expected count=4, got %d", count.Load())
		}
	})
}

// ==========================================
// asyncListenerWrapper 测试
// ==========================================

func TestAsyncListenerWrapper(t *testing.T) {
	t.Run("async execution", func(t *testing.T) {
		called := make(chan struct{})
		listener := &funcListener{
			onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
				close(called)
			},
		}

		provider := newMockGoroutineProvider()
		wrapper := &asyncListenerWrapper{
			callbacks:         []service.BroadcastListener{listener},
			goroutineProvider: provider,
		}

		stats := service.BroadcastStats{}
		wrapper.OnSuccess("peer-1", newTestMessage("resp"), stats)

		select {
		case <-called:
			// 成功
		case <-time.After(time.Second):
			t.Fatal("async callback timeout")
		}
	})

	t.Run("panic recovery", func(t *testing.T) {
		listener := &funcListener{
			onSuccess: func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
				panic("test panic")
			},
		}

		provider := newMockGoroutineProvider()
		wrapper := &asyncListenerWrapper{
			callbacks:         []service.BroadcastListener{listener},
			goroutineProvider: provider,
		}

		stats := service.BroadcastStats{}
		// 不应该 panic
		wrapper.OnSuccess("peer-1", newTestMessage("resp"), stats)

		// 给 panic 恢复一点时间
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("all methods async", func(t *testing.T) {
		count := atomic.Int32{}
		listener := &funcListener{
			onSuccess:  func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) { count.Add(1) },
			onFailure:  func(peer model.PeerID, err error, stats service.BroadcastStats) { count.Add(1) },
			onMajority: func(stats service.BroadcastStats) { count.Add(1) },
			onFullDone: func(stats service.BroadcastStats) { count.Add(1) },
		}

		provider := newMockGoroutineProvider()
		wrapper := &asyncListenerWrapper{
			callbacks:         []service.BroadcastListener{listener},
			goroutineProvider: provider,
		}

		stats := service.BroadcastStats{}
		wrapper.OnSuccess("peer-1", newTestMessage("resp"), stats)
		wrapper.OnFailure("peer-1", errors.New("test"), stats)
		wrapper.OnMajority(stats)
		wrapper.OnComplete(stats)

		// 等待异步执行
		time.Sleep(100 * time.Millisecond)

		if count.Load() != 4 {
			t.Fatalf("expected count=4, got %d", count.Load())
		}
	})
}

// ==========================================
// 选项函数测试
// ==========================================

func TestOnMajority(t *testing.T) {
	called := atomic.Bool{}
	opt := OnMajority(func(stats service.BroadcastStats) {
		called.Store(true)
	})

	cfg := &service.BroadcastConfig{}
	opt(cfg)

	callbacks := cfg.GetCallbacks()
	if len(callbacks) != 1 {
		t.Fatal("expected 1 callback")
	}

	callbacks[0].OnMajority(service.BroadcastStats{})
	if !called.Load() {
		t.Fatal("callback was not called")
	}
}

func TestOnComplete(t *testing.T) {
	called := atomic.Bool{}
	opt := OnComplete(func(stats service.BroadcastStats) {
		called.Store(true)
	})

	cfg := &service.BroadcastConfig{}
	opt(cfg)

	callbacks := cfg.GetCallbacks()
	if len(callbacks) != 1 {
		t.Fatal("expected 1 callback")
	}

	callbacks[0].OnComplete(service.BroadcastStats{})
	if !called.Load() {
		t.Fatal("callback was not called")
	}
}

func TestOnSuccess(t *testing.T) {
	called := atomic.Bool{}
	opt := OnSuccess(func(peer model.PeerID, resp model.Message, stats service.BroadcastStats) {
		called.Store(true)
	})

	cfg := &service.BroadcastConfig{}
	opt(cfg)

	callbacks := cfg.GetCallbacks()
	if len(callbacks) != 1 {
		t.Fatal("expected 1 callback")
	}

	callbacks[0].OnSuccess("peer-1", newTestMessage("resp"), service.BroadcastStats{})
	if !called.Load() {
		t.Fatal("callback was not called")
	}
}

func TestOnFailure(t *testing.T) {
	called := atomic.Bool{}
	opt := OnFailure(func(peer model.PeerID, err error, stats service.BroadcastStats) {
		called.Store(true)
	})

	cfg := &service.BroadcastConfig{}
	opt(cfg)

	callbacks := cfg.GetCallbacks()
	if len(callbacks) != 1 {
		t.Fatal("expected 1 callback")
	}

	callbacks[0].OnFailure("peer-1", errors.New("test"), service.BroadcastStats{})
	if !called.Load() {
		t.Fatal("callback was not called")
	}
}

// ==========================================
// ApplyBroadcastOptions 测试
// ==========================================

func TestApplyBroadcastOptions(t *testing.T) {
	t.Run("no options", func(t *testing.T) {
		listener := ApplyBroadcastOptions(nil, nil)
		if listener != nil {
			t.Fatal("expected nil listener")
		}
	})

	t.Run("empty options", func(t *testing.T) {
		listener := ApplyBroadcastOptions([]service.BroadcastOption{}, nil)
		if listener != nil {
			t.Fatal("expected nil listener")
		}
	})

	t.Run("single callback without provider", func(t *testing.T) {
		called := atomic.Bool{}
		opt := OnComplete(func(stats service.BroadcastStats) {
			called.Store(true)
		})

		listener := ApplyBroadcastOptions([]service.BroadcastOption{opt}, nil)
		if listener == nil {
			t.Fatal("expected listener")
		}

		listener.OnComplete(service.BroadcastStats{})
		if !called.Load() {
			t.Fatal("callback was not called")
		}
	})

	t.Run("multiple callbacks without provider", func(t *testing.T) {
		count := atomic.Int32{}
		opt1 := OnComplete(func(stats service.BroadcastStats) { count.Add(1) })
		opt2 := OnComplete(func(stats service.BroadcastStats) { count.Add(1) })

		listener := ApplyBroadcastOptions([]service.BroadcastOption{opt1, opt2}, nil)
		if listener == nil {
			t.Fatal("expected listener")
		}

		listener.OnComplete(service.BroadcastStats{})
		if count.Load() != 2 {
			t.Fatalf("expected count=2, got %d", count.Load())
		}
	})

	t.Run("with goroutine provider", func(t *testing.T) {
		called := make(chan struct{})
		opt := OnComplete(func(stats service.BroadcastStats) {
			close(called)
		})

		provider := newMockGoroutineProvider()
		listener := ApplyBroadcastOptions([]service.BroadcastOption{opt}, provider)
		if listener == nil {
			t.Fatal("expected listener")
		}

		listener.OnComplete(service.BroadcastStats{})

		select {
		case <-called:
			// 成功
		case <-time.After(time.Second):
			t.Fatal("async callback timeout")
		}
	})
}

// ==========================================
// safeListenerExec 测试
// ==========================================

func TestSafeListenerExec(t *testing.T) {
	t.Run("normal execution", func(t *testing.T) {
		called := atomic.Bool{}
		safeListenerExec(func() {
			called.Store(true)
		})

		if !called.Load() {
			t.Fatal("function was not called")
		}
	})

	t.Run("panic recovery", func(t *testing.T) {
		// 不应该 panic
		safeListenerExec(func() {
			panic("test panic")
		})
	})
}
