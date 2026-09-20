package appbinding

import (
	"reflect"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Built-in system capability registrations. Each block registers one
// capability plus its SDK-facing callIDs. Adding a new system capability =
// one RegisterCapability call + one RegisterHostCall call per callID. New
// subsystems go in their own cap_*.go file (see cap_voice.go for the
// pattern); the blocks here are the historical built-ins kept colocated
// because they are the host's core surface.

func init() {
	// llm.invoke — LLM completion/chat over the aiaggregator. The wire is
	// adapted (LLMReq/LLMResp), the backing callable is aliased.
	RegisterCapability(CapabilityDef{
		ID:                 CapLLMInvoke,
		Title:              "调用大模型",
		Description:        "允许应用调用已配置的大模型服务（消耗 Token）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.llm.invoke.title",
		I18nDescriptionKey: "capability.llm.invoke.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "调用大模型", Description: "允许应用调用已配置的大模型服务（消耗 Token）。"},
			"en-US": {Title: "Invoke large language model", Description: "Allows the app to call configured large language model services (consumes tokens)."},
		},
		CallPrefixes: []string{"llm."},
	})
	RegisterHostCall(HostCallDef{
		CallID:          "llm.complete",
		Capability:      CapLLMInvoke,
		Target:          "aiaggregator.dispatch",
		ReqType:         reflect.TypeOf(LLMReq{}),
		RespType:        reflect.TypeOf(LLMResp{}),
		StreamChunkKind: "LLMChunk",
	})
	RegisterHostCall(HostCallDef{
		CallID:          "llm.chat",
		Capability:      CapLLMInvoke,
		Target:          "aiaggregator.dispatch",
		ReqType:         reflect.TypeOf(LLMReq{}),
		RespType:        reflect.TypeOf(LLMResp{}),
		StreamChunkKind: "LLMChunk",
	})

	// fs.read / fs.write — project file access via the filesystem service.
	// Pass-through: the wire IS the backing callable schema.
	RegisterCapability(CapabilityDef{
		ID:                 CapFSRead,
		Title:              "读取文件系统",
		Description:        "允许应用读取宿主文件系统中的指定内容（只读）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.fs.read.title",
		I18nDescriptionKey: "capability.fs.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取文件系统", Description: "允许应用读取宿主文件系统中的指定内容（只读）。"},
			"en-US": {Title: "Read file system", Description: "Allows the app to read designated content from the host file system (read-only)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "project.read_file",
		Capability: CapFSRead,
		Target:     "filesystem.read",
	})
	RegisterCapability(CapabilityDef{
		ID:                 CapFSWrite,
		Title:              "写入文件系统",
		Description:        "允许应用在宿主文件系统中创建、修改或删除文件（高风险）。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.fs.write.title",
		I18nDescriptionKey: "capability.fs.write.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "写入文件系统", Description: "允许应用在宿主文件系统中创建、修改或删除文件（高风险）。"},
			"en-US": {Title: "Write file system", Description: "Allows the app to create, modify, or delete files on the host file system (high risk)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "project.write_file",
		Capability: CapFSWrite,
		Target:     "filesystem.write",
	})

	// shell.exec — host command execution. Open prefix family: any shell.*
	// callID in the manifest surface gates here.
	RegisterCapability(CapabilityDef{
		ID:                 CapShellExec,
		Title:              "执行系统命令（高风险）",
		Description:        "允许应用执行宿主操作系统的命令，可能导致数据丢失或安全风险。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.shell.exec.title",
		I18nDescriptionKey: "capability.shell.exec.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "执行系统命令（高风险）", Description: "允许应用执行宿主操作系统的命令，可能导致数据丢失或安全风险。"},
			"en-US": {Title: "Execute system commands (high risk)", Description: "Allows the app to execute host operating-system commands, which may cause data loss or security risks."},
		},
		CallPrefixes: []string{"shell."},
	})

	// config.read — host configuration (non-sensitive).
	RegisterCapability(CapabilityDef{
		ID:                 CapConfigRead,
		Title:              "读取宿主配置",
		Description:        "允许应用读取 sporemind 宿主配置（不含敏感凭据）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.config.read.title",
		I18nDescriptionKey: "capability.config.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取宿主配置", Description: "允许应用读取 sporemind 宿主配置（不含敏感凭据）。"},
			"en-US": {Title: "Read host configuration", Description: "Allows the app to read sporemind host configuration (excluding sensitive credentials)."},
		},
		CallPrefixes: []string{"config."},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "config.get",
		Capability: CapConfigRead,
		Local:      true,
		ReqType:    reflect.TypeOf(ConfigGetReq{}),
		Note:       "payload handled locally by the pluginhost; response is the raw config value (or null)",
	})

	// provider.read — model provider configuration. provider.get is a
	// pluginhost-side filter (local); provider.list is an alias.
	RegisterCapability(CapabilityDef{
		ID:                 CapProviderRead,
		Title:              "读取模型服务配置",
		Description:        "允许应用读取已配置的模型服务列表与公共参数（不含 API Key）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.provider.read.title",
		I18nDescriptionKey: "capability.provider.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取模型服务配置", Description: "允许应用读取已配置的模型服务列表与公共参数（不含 API Key）。"},
			"en-US": {Title: "Read model provider configuration", Description: "Allows the app to read configured model provider lists and public parameters (excluding API keys)."},
		},
		CallPrefixes: []string{"provider."},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "provider.list",
		Capability: CapProviderRead,
		Target:     "aimanager.provider_list",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "provider.get",
		Capability: CapProviderRead,
		Local:      true,
		ReqType:    reflect.TypeOf(ProviderGetReq{}),
		RespType:   reflect.TypeOf(gen.Provider{}),
		Note:       "served locally by the pluginhost (filters the aimanager provider list by Name); no backing callable",
	})

	// aggregator.read — model aggregator configuration. Aliased to the
	// aimanager callables.
	RegisterCapability(CapabilityDef{
		ID:                 CapAggregatorRead,
		Title:              "读取聚合器配置",
		Description:        "允许应用读取模型聚合器与路由配置（不含密钥）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.aggregator.read.title",
		I18nDescriptionKey: "capability.aggregator.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取聚合器配置", Description: "允许应用读取模型聚合器与路由配置（不含密钥）。"},
			"en-US": {Title: "Read aggregator configuration", Description: "Allows the app to read model aggregator and routing configuration (excluding secrets)."},
		},
		CallPrefixes: []string{"aggregator."},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "aggregator.list",
		Capability: CapAggregatorRead,
		Target:     "aimanager.aggregator_list",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "aggregator.get",
		Capability: CapAggregatorRead,
		Target:     "aimanager.aggregator_get",
	})

	// ssh.invoke — SSH remote command execution. Open prefix family.
	RegisterCapability(CapabilityDef{
		ID:                 CapSSHInvoke,
		Title:              "执行 SSH 远程命令（高风险）",
		Description:        "允许应用通过已配置的 SSH 连接在远程主机上执行命令，可能导致数据丢失或安全风险。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.ssh.invoke.title",
		I18nDescriptionKey: "capability.ssh.invoke.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "执行 SSH 远程命令（高风险）", Description: "允许应用通过已配置的 SSH 连接在远程主机上执行命令，可能导致数据丢失或安全风险。"},
			"en-US": {Title: "Execute SSH remote commands (high risk)", Description: "Allows the app to execute commands on remote hosts over configured SSH connections, which may cause data loss or security risks."},
		},
		CallPrefixes: []string{"sshmanager."},
	})

	// app.state — per-app key-value storage, served locally.
	RegisterCapability(CapabilityDef{
		ID:                 CapAppState,
		Title:              "持久化存储（应用私有键值）",
		Description:        "允许应用保存自己的账户数据、偏好设置等私有键值数据。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.app.state.title",
		I18nDescriptionKey: "capability.app.state.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "持久化存储（应用私有键值）", Description: "允许应用保存自己的账户数据、偏好设置等私有键值数据。"},
			"en-US": {Title: "Persistent storage (app-private key-value)", Description: "Allows the app to persist its own account data, preferences, and other app-private key-value data."},
		},
		CallPrefixes: []string{"state."},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "state.get",
		Capability: CapAppState,
		Local:      true,
		ReqType:    reflect.TypeOf(StateKeyReq{}),
		RespType:   reflect.TypeOf(StateGetResp{}),
		Note:       "per-app key-value storage routed by the pluginhost; the host injects the calling plugin's identity",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "state.set",
		Capability: CapAppState,
		Local:      true,
		ReqType:    reflect.TypeOf(StateSetReq{}),
		Note:       "per-app key-value storage routed by the pluginhost; Value is base64-encoded []byte on the wire",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "state.delete",
		Capability: CapAppState,
		Local:      true,
		ReqType:    reflect.TypeOf(StateKeyReq{}),
		RespType:   reflect.TypeOf(StateDeleteResp{}),
		Note:       "per-app key-value storage routed by the pluginhost; the host injects the calling plugin's identity",
	})

	// app.data — per-app private writable data directory (load-time grant).
	// No host callID backs it: declaring the capability is what makes the
	// host include a dataDir in the OnLoad config (appmanager), pointing at
	// <appDir>/.sporecode/appdata. The plugin owns that directory exclusively
	// (files, embedded databases such as goleveldb); it is never HTTP-served
	// and survives unload/reload/re-register.
	RegisterCapability(CapabilityDef{
		ID:                 CapAppData,
		Title:              "私有数据目录（应用自写文件/数据库）",
		Description:        "允许应用获得一个专属可写数据目录（appdata），可在其中自建文件或嵌入式数据库（如 LevelDB）。目录不对外提供 HTTP 访问。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.app.data.title",
		I18nDescriptionKey: "capability.app.data.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "私有数据目录（应用自写文件/数据库）", Description: "允许应用获得一个专属可写数据目录（appdata），可在其中自建文件或嵌入式数据库（如 LevelDB）。目录不对外提供 HTTP 访问。"},
			"en-US": {Title: "Private data directory (app-owned files/databases)", Description: "Grants the app an exclusive writable data directory (appdata) for its own files or embedded databases (e.g. LevelDB). The directory is never served over HTTP."},
		},
	})

	// agent.observe — receive the host agent event stream (step /
	// agent_message_received) via listen blocks. Load-time grant like
	// app.data: declaring the capability is the authorization; delivery is
	// gated per plugin in pluginhost's handleEventDeliver against the
	// manifest's declared permissions. High risk: step payloads carry full
	// conversation content (LLM deltas, tool calls, usage).
	RegisterCapability(CapabilityDef{
		ID:                 CapAgentObserve,
		Title:              "监听 Agent 消息流（高风险）",
		Description:        "允许应用实时接收宿主 Agent 的对话与执行事件流（step、收到的消息），包含完整对话内容。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.agent.observe.title",
		I18nDescriptionKey: "capability.agent.observe.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "监听 Agent 消息流（高风险）", Description: "允许应用实时接收宿主 Agent 的对话与执行事件流（step、收到的消息），包含完整对话内容。"},
			"en-US": {Title: "Observe agent event stream (high risk)", Description: "Allows the app to receive the host agent's live conversation and execution event stream (step events, received messages), including full conversation content."},
		},
	})

	// app.emit — publish app-declared events, served locally.
	RegisterCapability(CapabilityDef{
		ID:                 CapAppEmit,
		Title:              "发布应用事件",
		Description:        "允许应用后端发布自己在 appdef 中声明的事件，推送给订阅方（应用前端 on 订阅、监听 app_event 的应用）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.app.emit.title",
		I18nDescriptionKey: "capability.app.emit.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "发布应用事件", Description: "允许应用后端发布自己在 appdef 中声明的事件，推送给订阅方（应用前端 on 订阅、监听 app_event 的应用）。"},
			"en-US": {Title: "Publish app events", Description: "Allows the app backend to publish events it declared in its appdef, delivering them to subscribers (the app frontend on-subscriptions and apps listening to app_event)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "app.emit",
		Capability: CapAppEmit,
		Local:      true,
		ReqType:    reflect.TypeOf(AppEmitReq{}),
		RespType:   reflect.TypeOf(AppEmitResp{}),
		Note:       "publishes an app-declared event; served locally by the pluginhost (injects the calling plugin's identity, forwards to appmanager.plugin_emit which validates the event against the app manifest)",
	})

	// registry.read — callable metadata discovery, served locally.
	RegisterCapability(CapabilityDef{
		ID:                 CapRegistryRead,
		Title:              "查询 callable 注册表",
		Description:        "允许应用查询宿主 callable 注册表（调用元数据：callID、服务、参数、权限、效果、schema ID），只读，不含凭据值。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.registry.read.title",
		I18nDescriptionKey: "capability.registry.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "查询 callable 注册表", Description: "允许应用查询宿主 callable 注册表（调用元数据：callID、服务、参数、权限、效果、schema ID），只读，不含凭据值。"},
			"en-US": {Title: "Query callable registry", Description: "Allows the app to query the host's callable registry (callable metadata: callID, service, params, permission, effect, schema IDs) — read-only, no credential values."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "registry.query",
		Capability: CapRegistryRead,
		Local:      true,
		ReqType:    reflect.TypeOf(RegistryQueryReq{}),
		RespType:   reflect.TypeOf(RegistryQueryResp{}),
		Note:       "discovers callable metadata across the running actor tree; served locally by the pluginhost (filters on Service/Callable, offset cursor pagination; metadata only, no credential values)",
	})
}
