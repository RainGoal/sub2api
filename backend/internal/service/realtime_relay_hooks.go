package service

// RealtimeRelayHooks exposes only successfully forwarded text events. Hooks
// are optional and must never be used to control relay behavior.
type RealtimeRelayHooks struct {
	AfterUpstreamWrite func(payload []byte)
	AfterClientWrite   func(payload []byte)
}

func runRealtimeRelayHook(hook func([]byte), payload []byte) {
	defer func() { _ = recover() }()
	if hook != nil {
		hook(payload)
	}
}
