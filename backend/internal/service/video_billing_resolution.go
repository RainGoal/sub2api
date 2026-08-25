package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
)

const (
	VideoBillingResolution480P  = "480p"
	VideoBillingResolution720P  = "720p"
	VideoBillingResolution1080P = "1080p"
	VideoBillingResolution4K    = "4k"
)

// xAI 视频生成按秒计费，duration 请求参数允许 1-15 秒；未指定时上游默认生成 8 秒。
// 计费时长必须与上游实际消耗对齐，否则用户可通过拉长 duration 套利（提交时长由用户控制）。
const (
	VideoBillingMinDurationSeconds     = 1
	VideoBillingMaxDurationSeconds     = 15
	VideoBillingDefaultDurationSeconds = 8
)

// NormalizeVideoBillingDurationSecondsOrDefault 归一化计费用视频时长：
// 未指定（<=0）按上游默认 8 秒计，超出上游允许区间按边界收敛。
func NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds int) int {
	if durationSeconds <= 0 {
		return VideoBillingDefaultDurationSeconds
	}
	if durationSeconds < VideoBillingMinDurationSeconds {
		return VideoBillingMinDurationSeconds
	}
	if durationSeconds > VideoBillingMaxDurationSeconds {
		return VideoBillingMaxDurationSeconds
	}
	return durationSeconds
}

func NormalizeVideoBillingDurationForProvider(provider, model string, durationSeconds int) int {
	if provider != PlatformSeedance {
		return NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	}
	canonical, ok := videoprovider.CanonicalModel(model)
	if !ok {
		return 0
	}
	spec, ok := videoprovider.LookupModel(canonical)
	if !ok {
		return 0
	}
	if durationSeconds <= 0 {
		return spec.MinDuration
	}
	if durationSeconds < spec.MinDuration {
		return spec.MinDuration
	}
	if durationSeconds > spec.MaxDuration {
		return spec.MaxDuration
	}
	return durationSeconds
}

func NormalizeVideoBillingUnitsDuration(provider, model string, outputSeconds, billingSeconds int) int {
	outputSeconds = NormalizeVideoBillingDurationForProvider(provider, model, outputSeconds)
	if provider != PlatformSeedance {
		return outputSeconds
	}
	canonical, ok := videoprovider.CanonicalModel(model)
	if !ok || outputSeconds <= 0 {
		return 0
	}
	if canonical == videoprovider.ModelSeedance20 || canonical == videoprovider.ModelSeedance20Fast || canonical == videoprovider.ModelSeedance20Mini {
		return outputSeconds
	}
	if billingSeconds < outputSeconds {
		billingSeconds = outputSeconds
	}
	return billingSeconds
}

// NormalizeVideoCostDuration keeps provider-normalized Seedance billing units
// (which may include Seedance-2.5 reference-video seconds) while preserving the
// legacy xAI 1-15 second clamp for every other video model.
func NormalizeVideoCostDuration(model string, durationSeconds int) int {
	if canonical, ok := videoprovider.CanonicalModel(model); ok {
		spec, _ := videoprovider.LookupModel(canonical)
		if durationSeconds <= 0 {
			return spec.MinDuration
		}
		return durationSeconds
	}
	return NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
}

// LookupVideoBillingResolution 归一化分辨率并报告是否为已知档位。
// 配置解析路径必须用它而不是 OrDefault：把无法识别的档位（如拼错的
// "1080i"）静默折算成 480p，会让管理员配的高分辨率单价被挂到低分辨率档上。
func LookupVideoBillingResolution(resolution string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480", "480p", "sd":
		return VideoBillingResolution480P, true
	case "720", "720p", "hd":
		return VideoBillingResolution720P, true
	case "1080", "1080p", "full_hd", "full-hd", "fhd":
		return VideoBillingResolution1080P, true
	case "2160", "2160p", "4k", "uhd":
		return VideoBillingResolution4K, true
	default:
		return "", false
	}
}

// NormalizeVideoBillingResolutionOrDefault 用于运行时计费：上游回传的分辨率
// 缺失或无法识别时按最低档兜底，保证请求仍可计费。
func NormalizeVideoBillingResolutionOrDefault(resolution string) string {
	if normalized, ok := LookupVideoBillingResolution(resolution); ok {
		return normalized
	}
	return VideoBillingResolution480P
}
