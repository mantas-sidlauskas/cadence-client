// Copyright (c) 2017-2021 Uber Technologies Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package testlogger

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.uber.org/cadence/internal/common"

	"go.uber.org/zap/zaptest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var (
	done   = make(chan struct{})
	logged = make(chan struct{})
)

func TestMain(m *testing.M) {
	code := m.Run()
	// ensure synchronization between t.done and t.logf, else this test is extremely flaky.
	// for details see: https://github.com/golang/go/issues/67701
	close(done)
	select {
	case <-logged:
		os.Exit(code)
	case <-time.After(time.Millisecond): // should be MUCH faster
		_, _ = fmt.Fprintln(os.Stderr, "timed out waiting for test to log")
		os.Exit(1)
	}
}

// Unfortunately a moderate hack, to work around our faulty lifecycle management,
// and some libraries with issues as well.
// Ideally this test WOULD fail, but that's much harder to assert "safely".
func TestLoggerShouldNotFailIfLoggedLate(t *testing.T) {
	origLogger := NewZap(t)
	// if With does not defer core selection, this will fail the test
	// by sending the logs to t.Logf
	withLogger := origLogger.With(zap.String("test", "testing")) // literally any tag
	origLogger.Info("before is fine, orig")
	withLogger.Info("before is fine, with")
	go func() {
		<-done
		origLogger.Info("too late, orig")
		withLogger.Info("too late, with")
		close(logged)
	}()
}

func TestSubtestShouldNotFail(t *testing.T) {
	// when complete, a subtest's too-late logs just get pushed to the parent,
	// and do not fail any tests.  they only fail when no running parent exists.
	//
	// if Go changes this behavior, this test could fail, otherwise AFAICT it
	// should be stable.
	assertDoesNotFail := func(name string, setup, log func(t *testing.T)) {
		// need to wrap in something that will out-live the "real" test,
		// to ensure there is a running parent test to push logs toward.
		t.Run(name, func(t *testing.T) {
			// same setup as TestMain but contained within this sub-test
			var (
				done   = make(chan struct{})
				logged = make(chan struct{})
			)
			t.Run("inner", func(t *testing.T) {
				setup(t)
				go func() {
					<-done
					// despite being too late, the parent test is still running
					// so this does not fail the test.
					log(t)
					close(logged)
				}()
				time.AfterFunc(10*time.Millisecond, func() {
					close(done)
				})
			})
			<-logged
		})
	}

	assertDoesNotFail("real", func(t *testing.T) {
		// no setup needed
	}, func(t *testing.T) {
		t.Logf("too late but allowed")
	})

	var l *zap.Logger
	assertDoesNotFail("wrapped", func(t *testing.T) {
		l = NewZap(t)
	}, func(t *testing.T) {
		l.Info("too late but allowed")
	})
}

func TestObserver(t *testing.T) {
	l, obs := NewObserved(t)
	l.Info("a log")
	l.Info("some unrelated log")
	assert.Len(t, obs.FilterMessage("a log").All(), 1, "did not find a log that was logged")
}

func TestFallbackTestCore_Enabled(t *testing.T) {
	fallbackCfg := zap.NewDevelopmentConfig()
	fallbackCfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	fallbackLogger, err := fallbackCfg.Build()
	require.NoError(t, err)

	core := &fallbackTestCore{
		mu:        &sync.RWMutex{},
		t:         t,
		fallback:  fallbackLogger.Core(),
		testing:   zaptest.NewLogger(t).Core(),
		completed: common.PtrOf(false),
	}
	// Debug is enabled in zaptest.Logger
	assert.True(t, core.Enabled(zap.DebugLevel))
	core.UseFallback()
	// Debug is disabled in fallbackLogger.
	assert.False(t, core.Enabled(zap.DebugLevel))
}

func TestFallbackTestCore_Sync(t *testing.T) {
	core := NewZap(t).Core().(*fallbackTestCore)
	core.fallback = zap.NewNop().Core()
	// Sync for testing logger must not fail.
	assert.NoError(t, core.Sync(), "normal sync must not fail")
	core.UseFallback()
	// Sync for fallback logger must not fail.
	assert.NoError(t, core.Sync(), "fallback sync must not fail")
}
