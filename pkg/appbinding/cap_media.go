package appbinding

import (
	"reflect"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Media generation capabilities. Image and video generation are separate
// capabilities: both consume paid quota and can run minutes, but video is the
// heavier class (long-poll LRO backends, larger artifacts), so the install
// preview can grant them independently. All four callIDs are media-aware:
//
//   - media.list_units is a pass-through alias to aimanager.list_units
//     (Modality filter selects "image" | "video" pools; the response is the
//     public ManualCallableUnit list — no auth tokens), so plugins can render
//     a unit picker before spending quota.
//   - media.accounts.list is a pass-through alias to media.list_accounts
//     (Kind filter: "image" | "video"): redacted account views (HasAPIKey
//     flag, never the key) so plugins can see the accounts that serve the
//     active-account default path of image/video.generate — mirroring
//     voice.accounts.list.
//   - image.generate / video.generate are pluginhost-local host services: the
//     host resolves the serving unit (active media account, or the pinned
//     Provider+Model pair, or aggregator auto-pick), runs the provider HTTP
//     call itself, and writes the artifact into the calling app's media/
//     runtime directory. Credentials never cross the bridge — the response is
//     AppMediaGenResp (path + mime + size + provenance), and the panel loads
//     the artifact as a same-origin URL from the plugin's own listener.
//
// Unit semantics mirror llm.complete: Provider+Model both set = strict pin
// (resolve failure is an error, no soft fallback); both empty = the kind's
// active media account, then aggregator pool auto-pick; model-only is
// rejected (Unit is the only selection primitive — no naked models).

// mediaBudget matches the agent-side mediaGenerationTimeout so a plugin
// generation is never cut shorter than the equivalent agent tool call.
const (
	imageGenBudget = 2 * time.Minute
	videoGenBudget = 12 * time.Minute
)

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapMediaRead,
		Title:              "枚举媒体 unit 与账户",
		Description:        "允许应用枚举宿主图像/视频生成 unit（按 modality 过滤）与媒体账户（脱敏，不含密钥）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.media.read.title",
		I18nDescriptionKey: "capability.media.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "枚举媒体 unit 与账户", Description: "允许应用枚举宿主图像/视频生成 unit（按 modality 过滤）与媒体账户（脱敏，不含密钥）。"},
			"en-US": {Title: "List media units and accounts", Description: "Allows the app to enumerate host image/video generation units (modality-filtered) and media accounts (redacted, never credentials)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "media.list_units",
		Capability: CapMediaRead,
		Target:     "aimanager.list_units",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "media.accounts.list",
		Capability: CapMediaRead,
		Target:     "media.list_accounts",
	})

	RegisterCapability(CapabilityDef{
		ID:                 CapImageGen,
		Title:              "图像生成",
		Description:        "允许应用按 unit 生成图像（宿主代调用并落盘，消耗账户额度；产物经应用自身 URL 提供）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.image.gen.title",
		I18nDescriptionKey: "capability.image.gen.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "图像生成", Description: "允许应用按 unit 生成图像（宿主代调用并落盘，消耗账户额度；产物经应用自身 URL 提供）。"},
			"en-US": {Title: "Image generation", Description: "Allows the app to generate images via a unit (host-mediated call and persistence, consumes account quota; artifacts served from the app's own URL)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "image.generate",
		Capability: CapImageGen,
		Local:      true,
		ReqType:    reflect.TypeOf(gen.ImageGenerateReq{}),
		RespType:   reflect.TypeOf(gen.AppMediaGenResp{}),
		Budget:     imageGenBudget,
		Note:       "Generate an image; Provider+Model both set pins the unit strictly, both empty uses the kind's active account then aggregator auto-pick. The artifact lands in the app's media/ dir; Path is a same-origin URL.",
	})

	RegisterCapability(CapabilityDef{
		ID:                 CapVideoGen,
		Title:              "视频生成",
		Description:        "允许应用按 unit 生成视频（宿主代调用并落盘，消耗账户额度；长任务可运行数分钟，产物经应用自身 URL 提供）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.video.gen.title",
		I18nDescriptionKey: "capability.video.gen.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "视频生成", Description: "允许应用按 unit 生成视频（宿主代调用并落盘，消耗账户额度；长任务可运行数分钟，产物经应用自身 URL 提供）。"},
			"en-US": {Title: "Video generation", Description: "Allows the app to generate video via a unit (host-mediated call and persistence, consumes account quota; runs minutes for long tasks; artifacts served from the app's own URL)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "video.generate",
		Capability: CapVideoGen,
		Local:      true,
		ReqType:    reflect.TypeOf(gen.VideoGenerateReq{}),
		RespType:   reflect.TypeOf(gen.AppMediaGenResp{}),
		Budget:     videoGenBudget,
		Note:       "Generate a video clip; Provider+Model both set pins the unit strictly, both empty uses the kind's active account then aggregator auto-pick. The artifact lands in the app's media/ dir; Path is a same-origin URL.",
	})
}
