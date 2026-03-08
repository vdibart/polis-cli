package dm

import "testing"

func TestRateLimiter_AllowGlobal(t *testing.T) {
	rl := NewRateLimiter(10, 3) // global limit = 3

	if !rl.AllowGlobal() {
		t.Error("first request should be allowed")
	}
	if !rl.AllowGlobal() {
		t.Error("second request should be allowed")
	}
	if !rl.AllowGlobal() {
		t.Error("third request should be allowed")
	}
	if rl.AllowGlobal() {
		t.Error("fourth request should be denied (limit=3)")
	}
}

func TestRateLimiter_AllowSender(t *testing.T) {
	rl := NewRateLimiter(2, 100) // per-sender limit = 2

	if !rl.AllowSender("alice.com") {
		t.Error("first from alice should be allowed")
	}
	if !rl.AllowSender("alice.com") {
		t.Error("second from alice should be allowed")
	}
	if rl.AllowSender("alice.com") {
		t.Error("third from alice should be denied (limit=2)")
	}

	// Different sender should be independent
	if !rl.AllowSender("bob.com") {
		t.Error("first from bob should be allowed")
	}
}

func TestRateLimiter_Defaults(t *testing.T) {
	rl := NewRateLimiter(0, 0)
	if rl.perSenderLimit != DefaultPerSenderRate {
		t.Errorf("expected default per-sender %d, got %d", DefaultPerSenderRate, rl.perSenderLimit)
	}
	if rl.globalLimit != DefaultGlobalRate {
		t.Errorf("expected default global %d, got %d", DefaultGlobalRate, rl.globalLimit)
	}
}
