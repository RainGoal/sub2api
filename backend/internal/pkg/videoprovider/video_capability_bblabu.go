package videoprovider

import (
	_ "embed"
	"encoding/json"
)

// Presets verified against BBLabu's public video-catalog version 2026-10-16.1
// and the official-model merge in its public media documentation on 2026-10-17.
// These describe capabilities only; supplier prices are deliberately omitted.
//
//go:embed video_capability_bblabu.json
var bblabuVideoCapabilityJSON []byte

var bblabuVideoCapabilities = func() []VideoModelCapability {
	var capabilities []VideoModelCapability
	if err := json.Unmarshal(bblabuVideoCapabilityJSON, &capabilities); err != nil {
		panic(err)
	}
	if err := ValidateVideoModelCapabilities(capabilities); err != nil {
		panic(err)
	}
	return capabilities
}()
