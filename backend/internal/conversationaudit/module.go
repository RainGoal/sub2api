package conversationaudit

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewRepository,
	NewConfigManager,
	NewCaptureService,
	wire.Bind(new(Recorder), new(*CaptureService)),
)
