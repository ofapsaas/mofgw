// SPDX-FileCopyrightText: 2026 Pablo Manuel Rizzo
//
// SPDX-License-Identifier: GPL-3.0-or-later

package timeouts

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimeoutForThreeStates(t *testing.T) {
	global := 120 * time.Second

	// ausente (nil) -> global default
	if got := For(nil, global); got != global {
		t.Errorf("nil -> %v, want global %v", got, global)
	}
	// 0 explícito -> sin timeout
	zero := time.Duration(0)
	if got := For(&zero, global); got != 0 {
		t.Errorf("0 -> %v, want 0 (sin timeout)", got)
	}
	// N -> N
	n := 60 * time.Second
	if got := For(&n, global); got != n {
		t.Errorf("60s -> %v, want 60s", got)
	}
}

func TestAttemptContext(t *testing.T) {
	ctx := context.Background()

	// timeout N -> ctx con deadline
	n := 50 * time.Millisecond
	actx, cancel := Attempt(ctx, n)
	if _, ok := actx.Deadline(); !ok {
		t.Error("Attempt con timeout N debería tener deadline")
	}
	select {
	case <-actx.Done():
		if !IsTimeout(Classify(actx.Err())) {
			t.Errorf("Classify(Err()) = %v, want ErrTimeout", actx.Err())
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("ctx debería expirar con el timeout")
	}
	cancel()

	// timeout 0 -> sin deadline, nunca expira por sí solo
	zero := time.Duration(0)
	zctx, zcancel := Attempt(ctx, zero)
	if _, ok := zctx.Deadline(); ok {
		t.Error("Attempt con 0 no debería tener deadline")
	}
	select {
	case <-zctx.Done():
		t.Error("ctx con 0 no debería expirar")
	default:
	}
	zcancel()

	// timeout negativo -> tratado como sin timeout
	neg := -1 * time.Second
	nctx, ncancel := Attempt(ctx, neg)
	if _, ok := nctx.Deadline(); ok {
		t.Error("Attempt con negativo no debería tener deadline")
	}
	ncancel()
}

func TestErrTimeout(t *testing.T) {
	if !IsTimeout(ErrTimeout) {
		t.Error("IsTimeout(ErrTimeout) debería ser true")
	}
	if IsTimeout(context.Canceled) {
		t.Error("IsTimeout(context.Canceled) debería ser false")
	}
	if IsTimeout(nil) {
		t.Error("IsTimeout(nil) debería ser false")
	}
	if Classify(context.DeadlineExceeded) != ErrTimeout {
		t.Error("Classify(DeadlineExceeded) debería ser ErrTimeout")
	}
	if Classify(nil) != nil {
		t.Error("Classify(nil) debería ser nil")
	}
	orig := errors.New("boom")
	if Classify(orig) != orig {
		t.Error("Classify(error cualquiera) debería devolver el mismo error")
	}
}
