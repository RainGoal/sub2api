package videoprovider

import "strings"

const (
	ModelSeedance20     = "seedance-2.0"
	ModelSeedance20Fast = "seedance-2.0-fast"
	ModelSeedance20Mini = "seedance-2.0-mini"
	ModelSeedance25     = "seedance-2.5"
)

type ModelSpec struct {
	ID                       string
	MinDuration              int
	MaxDuration              int
	AllowedDurations         []int
	Resolutions              []string
	MaxImages                int
	MaxVideos                int
	MaxAudios                int
	MaxTotalAssets           int
	MaxReferenceVideoSeconds int
}

var modelSpecs = map[string]ModelSpec{
	ModelSeedance20: {
		ID: ModelSeedance20, MinDuration: 4, MaxDuration: 15,
		Resolutions: []string{"480p", "720p", "1080p", "4k"},
		MaxImages:   12, MaxVideos: 3, MaxAudios: 3, MaxTotalAssets: 18,
		MaxReferenceVideoSeconds: 15,
	},
	ModelSeedance20Fast: {
		ID: ModelSeedance20Fast, MinDuration: 4, MaxDuration: 15,
		Resolutions: []string{"480p", "720p"},
		MaxImages:   12, MaxVideos: 3, MaxAudios: 3, MaxTotalAssets: 18,
		MaxReferenceVideoSeconds: 15,
	},
	ModelSeedance20Mini: {
		ID: ModelSeedance20Mini, MinDuration: 4, MaxDuration: 12,
		Resolutions: []string{"480p", "720p", "1080p"},
		MaxImages:   2, MaxVideos: 0, MaxAudios: 0, MaxTotalAssets: 2,
	},
	ModelSeedance25: {
		ID: ModelSeedance25, MinDuration: 4, MaxDuration: 30,
		AllowedDurations: []int{4, 5, 6, 8, 10, 12, 15, 20, 25, 30},
		Resolutions:      []string{"480p", "720p"},
		MaxImages:        30, MaxVideos: 10, MaxAudios: 10, MaxTotalAssets: 50,
		MaxReferenceVideoSeconds: 30,
	},
}

func CanonicalModel(model string) (string, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case ModelSeedance20:
		return ModelSeedance20, true
	case ModelSeedance20Fast:
		return ModelSeedance20Fast, true
	case ModelSeedance20Mini:
		return ModelSeedance20Mini, true
	case ModelSeedance25, "bytedance/seedance-2.5":
		return ModelSeedance25, true
	default:
		return "", false
	}
}

func LookupModel(model string) (ModelSpec, bool) {
	canonical, ok := CanonicalModel(model)
	if !ok {
		return ModelSpec{}, false
	}
	spec, ok := modelSpecs[canonical]
	return spec, ok
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
